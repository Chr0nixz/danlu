package settings

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danlu/internal/config"
	"danlu/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func newRuntime(t *testing.T) (*Runtime, *store.Store) {
	t.Helper()
	st := openStore(t)
	r := New(st, Defaults(config.Default()))
	if err := r.Reload(); err != nil {
		t.Fatal(err)
	}
	return r, st
}

func i64(v int64) *int64 { return &v }

func TestDefaultsFollowConfig(t *testing.T) {
	r, _ := newRuntime(t)
	v := r.Current()
	if v.KillGrace != 30*time.Second || v.FailFast != 2*time.Minute || v.TerminalLifetime != 15*time.Minute ||
		v.DiskMinGB != 2 || v.KeepDays != 90 || v.UnknownGPUMemoryMB != 1024 {
		t.Fatalf("库里没有时应取 config.toml 的值: %+v", v)
	}
	item := r.View(GroupRuntime)["fail_fast"]
	if item.Value != 120 || item.Default != 120 || item.Custom || !item.ZeroOff || item.Unit != "s" {
		t.Fatalf("fail_fast 视图不对: %+v", item)
	}
	if _, ok := r.View(GroupRuntime)["keep_days"]; ok {
		t.Fatal("保留天数属于 retention 组")
	}
}

// 只写改了的项；null 恢复成配置文件的值；改动说明写清前后
func TestUpdateWritesOnlyChangedKeys(t *testing.T) {
	r, st := newRuntime(t)
	v, changes, err := r.Update(GroupRuntime, Patch{"fail_fast": i64(0), "kill_grace": i64(30), "disk_min_gb": i64(5)})
	if err != nil {
		t.Fatal(err)
	}
	if v.FailFast != 0 || v.DiskMinGB != 5 {
		t.Fatalf("新值没生效: %+v", v)
	}
	want := "磁盘保护暂停线 2 GB → 5 GB；快速失败暂停 2 分钟 → 关闭"
	if got := strings.Join(changes, "；"); got != want {
		t.Fatalf("改动说明 = %q，应为 %q", got, want)
	}
	raw, _ := st.GetSettings(keys()...)
	if len(raw) != 2 || raw["runtime.fail_fast"] != "0" || raw["runtime.disk_min_gb"] != "5" {
		t.Fatalf("库里应只有改了的两项: %v", raw)
	}
	if !r.View(GroupRuntime)["fail_fast"].Custom || r.View(GroupRuntime)["kill_grace"].Custom {
		t.Fatal("custom 标记不对")
	}

	v, changes, err = r.Update(GroupRuntime, Patch{"fail_fast": nil, "kill_grace": nil})
	if err != nil {
		t.Fatal(err)
	}
	if v.FailFast != 2*time.Minute || len(changes) != 1 || changes[0] != "快速失败暂停 恢复为配置文件里的 2 分钟" {
		t.Fatalf("恢复默认不对: %+v %q", v, changes)
	}
	if raw, _ := st.GetSettings("runtime.fail_fast"); len(raw) != 0 {
		t.Fatal("恢复默认应删掉库里的项")
	}
}

func TestUpdateValidation(t *testing.T) {
	r, st := newRuntime(t)
	for name, p := range map[string]Patch{
		"未知项":     {"nope": i64(1)},
		"别的组":     {"keep_days": i64(1)},
		"超出范围":    {"kill_grace": i64(601)},
		"不能为 0":   {"kill_grace": i64(0)},
		"负数":      {"fail_fast": i64(-1)},
		"暂停线高于提示": {"disk_min_gb": i64(30)},
		"快照单文件过大": {"snapshot_max_file_mb": i64(100)},
	} {
		if _, _, err := r.Update(GroupRuntime, p); !IsInputError(err) {
			t.Errorf("%s 应返回 InputError，实际 %v", name, err)
		}
	}
	if raw, _ := st.GetSettings(keys()...); len(raw) != 0 {
		t.Fatalf("校验失败时不应写库: %v", raw)
	}
	// 两项一起改就能满足
	if _, _, err := r.Update(GroupRuntime, Patch{"disk_min_gb": i64(30), "disk_warn_gb": i64(50)}); err != nil {
		t.Fatal(err)
	}
	// 关闭提示线后暂停线不受它约束
	if _, _, err := r.Update(GroupRuntime, Patch{"disk_warn_gb": i64(0)}); err != nil {
		t.Fatal(err)
	}
}

// config.toml 里超出面板范围的旧值照样生效，也不妨碍改别的项
func TestOutOfRangeConfigValueStillHonoured(t *testing.T) {
	c := config.Default()
	c.Runtime.SilenceAlert.Duration = 20 * time.Second // e2e 就这么配
	c.Runtime.DiskMinGB = 50                           // 比提示线还高
	st := openStore(t)
	r := New(st, Defaults(c))
	if r.Current().SilenceAlert != 20*time.Second {
		t.Fatal("配置里的值应生效")
	}
	if _, _, err := r.Update(GroupRuntime, Patch{"kill_grace": i64(5), "silence_alert": i64(20)}); err != nil {
		t.Fatalf("没改的项不该被检查: %v", err)
	}
}

// 另一个进程改了库，运行中的服务几秒内跟上；库里的坏值按默认值算
func TestRefreshFromOtherProcessAndBadValues(t *testing.T) {
	r, st := newRuntime(t)
	if err := st.SetSettings(map[string]string{"runtime.kill_grace": "7", "runtime.disk_min_gb": "abc", "runtime.unknown_gpu_memory_mb": "0"}); err != nil {
		t.Fatal(err)
	}
	if r.Current().KillGrace != 30*time.Second {
		t.Fatal("刷新间隔内应沿用缓存")
	}
	r.mu.Lock()
	r.at = time.Time{}
	r.mu.Unlock()
	v := r.Current()
	if v.KillGrace != 7*time.Second || v.DiskMinGB != 2 || v.UnknownGPUMemoryMB != 1024 {
		t.Fatalf("应读到新值并忽略坏值: %+v", v)
	}
	if got := strings.Join(r.Overridden(), ","); got != "kill_grace=7 秒" {
		t.Fatalf("Overridden = %q", got)
	}
}

func TestPreviewDoesNotWrite(t *testing.T) {
	r, st := newRuntime(t)
	v, err := r.Preview(GroupRetention, Patch{"keep_days": i64(7), "max_total_gb": i64(0)})
	if err != nil || v.KeepDays != 7 || v.MaxTotalGB != 0 {
		t.Fatalf("预览结果不对: %+v %v", v, err)
	}
	if raw, _ := st.GetSettings(keys()...); len(raw) != 0 || r.Current().KeepDays != 90 {
		t.Fatal("预览不应写库或改缓存")
	}
	if _, err := r.Preview(GroupRetention, Patch{"keep_days": i64(40000)}); !IsInputError(err) {
		t.Fatal("预览也要校验")
	}
}

// Reset 只删运行设置：清理器自己的状态和面板偏好不动
func TestResetKeepsOtherKeys(t *testing.T) {
	r, st := newRuntime(t)
	if _, _, err := r.Update(GroupRetention, Patch{"keep_days": i64(30)}); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSettings(map[string]string{"retention.env_gc": "x", "ui.log_wrap": "false"}); err != nil {
		t.Fatal(err)
	}
	if err := Reset(st); err != nil {
		t.Fatal(err)
	}
	raw, _ := st.GetSettings(append(keys(), "retention.env_gc", "ui.log_wrap")...)
	if len(raw) != 2 || raw["retention.env_gc"] != "x" || raw["ui.log_wrap"] != "false" {
		t.Fatalf("Reset 删多了或删少了: %v", raw)
	}
}

func TestApplyTo(t *testing.T) {
	c := config.Default()
	v := Defaults(c)
	v.KeepDays, v.DiskWarnGB, v.TerminalLifetime = 3, 9, time.Hour
	v.ApplyTo(c)
	if c.Retention.KeepDays != 3 || c.Runtime.DiskWarnGB != 9 || c.Terminal.SessionLifetime.Duration != time.Hour {
		t.Fatalf("ApplyTo 不完整: %+v", c)
	}
}

func TestPrefs(t *testing.T) {
	st := openStore(t)
	p, err := LoadPrefs(st)
	if err != nil || p["jobs_page_size"] != int64(20) || p["log_wrap"] != true || p["new_job_conda"] != "auto" {
		t.Fatalf("默认偏好不对: %v %v", p, err)
	}
	patch := map[string]json.RawMessage{"jobs_page_size": []byte("50"), "log_wrap": []byte("false"), "new_job_gpus": []byte("1")}
	p, changes, err := UpdatePrefs(st, patch)
	if err != nil {
		t.Fatal(err)
	}
	if p["jobs_page_size"] != int64(50) || p["log_wrap"] != false || len(changes) != 2 {
		t.Fatalf("保存结果不对: %v %q", p, changes)
	}
	raw, _ := st.GetSettings(prefKeys()...)
	if len(raw) != 2 {
		t.Fatalf("和当前值相同的项不应写库: %v", raw)
	}
	for name, v := range map[string]string{"jobs_page_size": "30", "log_wrap": `"yes"`, "new_job_conda": `"base"`, "warn_gpu_temp_c": "200", "nope": "1"} {
		if _, _, err := UpdatePrefs(st, map[string]json.RawMessage{name: []byte(v)}); !IsInputError(err) {
			t.Errorf("%s=%s 应被拒绝: %v", name, v, err)
		}
	}
	p, changes, err = UpdatePrefs(st, map[string]json.RawMessage{"jobs_page_size": []byte("null"), "warn_cpu_percent": []byte("null")})
	if err != nil || p["jobs_page_size"] != int64(20) || len(changes) != 1 {
		t.Fatalf("恢复默认不对: %v %q %v", p, changes, err)
	}
	// 库里的坏值按默认值算
	if err := st.SetSetting("ui.warn_cpu_percent", "999"); err != nil {
		t.Fatal(err)
	}
	if p, _ := LoadPrefs(st); p["warn_cpu_percent"] != int64(90) {
		t.Fatalf("坏值应按默认值: %v", p["warn_cpu_percent"])
	}
}
