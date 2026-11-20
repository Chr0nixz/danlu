package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danlu/internal/config"
	"danlu/internal/retention"
	"danlu/internal/settings"
	"danlu/internal/store"
)

func newSettingsServer(t *testing.T) *Server {
	t.Helper()
	p := config.PathsFor(t.TempDir())
	if err := p.MkdirAll(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(p.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	c := config.Default()
	rt := settings.New(st, settings.Defaults(c))
	pr := &retention.Pruner{Store: st, Paths: p, OutputRoot: filepath.Join(t.TempDir(), "outputs"),
		Live: func() (int64, int64) { v := rt.Current(); return v.KeepDays, v.MaxTotalGB << 30 }}
	return &Server{Cfg: c, Paths: p, Store: st, Settings: rt, Pruner: pr}
}

func postJSON(t *testing.T, h http.HandlerFunc, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h(w, r)
	return w
}

func lastAudit(t *testing.T, st *store.Store) store.AuditEntry {
	t.Helper()
	entries, err := st.ListAudit(1, 0)
	if err != nil || len(entries) == 0 {
		t.Fatalf("没有操作记录: %v", err)
	}
	return entries[0]
}

func TestUpdateRuntimeSettings(t *testing.T) {
	s := newSettingsServer(t)
	h := s.updateRuntime(settings.GroupRuntime)
	w := postJSON(t, h, "/api/settings/runtime", `{"fail_fast": 0, "kill_grace": 5}`)
	if w.Code != 200 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	var resp struct {
		Runtime map[string]settings.Item `json:"runtime"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if it := resp.Runtime["fail_fast"]; it.Value != 0 || !it.Custom || it.Default != 120 {
		t.Fatalf("fail_fast = %+v", it)
	}
	if s.Settings.Current().KillGrace != 5*time.Second {
		t.Fatal("新值应当场生效")
	}
	if e := lastAudit(t, s.Store); e.Action != "settings_change" || !strings.Contains(e.Detail, "快速失败暂停 2 分钟 → 关闭") {
		t.Fatalf("操作记录 = %+v", e)
	}

	for _, body := range []string{`{"kill_grace": 0}`, `{"keep_days": 3}`, `{"nope": 1}`, `{"kill_grace": 1.5}`, `[1]`} {
		if w := postJSON(t, h, "/api/settings/runtime", body); w.Code != 400 {
			t.Errorf("%s: status = %d，应为 400", body, w.Code)
		}
	}
	if w := postJSON(t, h, "/api/settings/runtime", `{"fail_fast": null}`); w.Code != 200 || s.Settings.Current().FailFast != 2*time.Minute {
		t.Fatalf("null 应恢复成配置文件的值: %d %s", w.Code, w.Body)
	}
}

func TestSettingsViewIncludesPanelSettings(t *testing.T) {
	s := newSettingsServer(t)
	resp := map[string]any{}
	if err := s.panelSettings(resp); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"runtime", "retention", "prefs"} {
		if resp[k] == nil {
			t.Errorf("缺少 %s", k)
		}
	}
	if _, ok := resp["prune"]; ok {
		t.Error("还没自动清理过时不应有 prune")
	}
	if got := resp["retention"].(map[string]settings.Item)["keep_days"].Value; got != 90 {
		t.Fatalf("keep_days = %d", got)
	}
}

func TestEffectiveConfigForDoctor(t *testing.T) {
	s := newSettingsServer(t)
	if _, _, err := s.Settings.Update(settings.GroupRuntime, settings.Patch{"disk_warn_gb": ptr(int64(50))}); err != nil {
		t.Fatal(err)
	}
	if c := s.effectiveConfig(); c.Runtime.DiskWarnGB != 50 || c == s.Cfg {
		t.Fatalf("体检应拿到面板上的值，且不能改 s.Cfg: %d", c.Runtime.DiskWarnGB)
	}
	if s.Cfg.Runtime.DiskWarnGB != 20 {
		t.Fatal("s.Cfg 被改了")
	}
}

func ptr[T any](v T) *T { return &v }

func TestRetentionPreviewAndSave(t *testing.T) {
	s := newSettingsServer(t)
	j := &store.Job{Name: "旧任务", Workdir: "/w", Cmd: "x", Status: store.StatusSucceeded}
	if err := s.Store.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().Add(-40 * 24 * time.Hour).Unix()
	j.EndedAt = &ended
	if err := s.Store.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(s.Paths.Logs, fmt.Sprintf("%d.log", j.ID))
	if err := os.WriteFile(logPath, []byte("日志"), 0o600); err != nil {
		t.Fatal(err)
	}
	w := postJSON(t, s.previewRetention, "/api/settings/retention/preview", `{"keep_days": 30}`)
	if w.Code != 200 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	var preview struct {
		Jobs  int               `json:"jobs"`
		Bytes int64             `json:"bytes"`
		Items []retention.Entry `json:"items"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.Jobs != 1 || preview.Items[0].JobID != j.ID || preview.Bytes == 0 {
		t.Fatalf("预览 = %+v", preview)
	}
	if _, err := os.Stat(logPath); err != nil {
		t.Fatal("预览删除了日志")
	}
	if s.Settings.Current().KeepDays != 90 {
		t.Fatal("预览不应保存")
	}
	if w := postJSON(t, s.previewRetention, "/api/settings/retention/preview", `{"keep_days": -1}`); w.Code != 400 {
		t.Fatalf("坏值预览应返回 400: %d", w.Code)
	}
	w = postJSON(t, s.updateRuntime(settings.GroupRetention), "/api/settings/retention", `{"keep_days": 30}`)
	if w.Code != 200 || s.Settings.Current().KeepDays != 30 {
		t.Fatalf("保存失败: %d %s", w.Code, w.Body)
	}
	if e := lastAudit(t, s.Store); e.Action != "retention_change" {
		t.Fatalf("操作记录 = %+v", e)
	}
}

func TestUpdatePrefs(t *testing.T) {
	s := newSettingsServer(t)
	w := postJSON(t, s.updatePrefs, "/api/settings/prefs", `{"jobs_page_size": 50, "new_job_conda": "none"}`)
	if w.Code != 200 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	var resp struct {
		Prefs map[string]any `json:"prefs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Prefs["jobs_page_size"] != float64(50) || resp.Prefs["new_job_conda"] != "none" || resp.Prefs["log_wrap"] != true {
		t.Fatalf("prefs = %v", resp.Prefs)
	}
	if e := lastAudit(t, s.Store); e.Action != "prefs_change" {
		t.Fatalf("操作记录 = %+v", e)
	}
	if w := postJSON(t, s.updatePrefs, "/api/settings/prefs", `{"jobs_page_size": 33}`); w.Code != 400 {
		t.Fatalf("坏值应返回 400: %d", w.Code)
	}
}
