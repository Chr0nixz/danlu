package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"danlu/internal/system"
)

func TestParseResourceRange(t *testing.T) {
	cases := []struct {
		name string
		want time.Duration
		ok   bool
	}{
		{"", 15 * time.Minute, true},
		{"1m", time.Minute, true},
		{"5m", 5 * time.Minute, true},
		{"15m", 15 * time.Minute, true},
		{"1h", time.Hour, true},
		{"3h", 0, false},
		{"1m ", 0, false},
	}
	for _, c := range cases {
		got, ok := parseResourceRange(c.name)
		if got != c.want || ok != c.ok {
			t.Errorf("parseResourceRange(%q) = %v, %v; want %v, %v", c.name, got, ok, c.want, c.ok)
		}
	}
}

type fakeResourceSource struct{ n int }

func (f *fakeResourceSource) Query(context.Context) (system.ResourceSnapshot, error) {
	f.n++
	v := 40 + f.n
	return system.ResourceSnapshot{
		At: time.Now().Unix(), Samples: f.n, ProcessSamples: 1,
		Host:      system.HostInfo{Hostname: "test-host", CPUCores: 2},
		CPU:       system.CPUInfo{Percent: &v, Cores: []int{v, 10}},
		Memory:    system.MemoryInfo{TotalBytes: 100, UsedBytes: 40, Percent: 40},
		Processes: []system.ProcessInfo{{PID: 1, Command: "python"}},
		RootDisk:  &system.Disk{Path: "/", Paths: []string{"/"}},
	}, nil
}

type scriptedResourceSource struct {
	at []int64 // 每次采样的时间；0 表示这一次失败
	n  int
}

func (f *scriptedResourceSource) Query(context.Context) (system.ResourceSnapshot, error) {
	at := f.at[min(f.n, len(f.at)-1)]
	f.n++
	if at == 0 {
		return system.ResourceSnapshot{}, errors.New("读取 CPU 数据失败: mock")
	}
	return system.ResourceSnapshot{At: at, Samples: f.n}, nil
}

// updated_at 是快照自己的采样时间：失败时 Sampler 的完成时间是新的，返回的却是旧快照或零值，
// 用完成时间会把旧数据（甚至一屏 0）标成"刚刚"。
func TestResourceUpdatedAtIsSampleTime(t *testing.T) {
	updatedAt := func(src *scriptedResourceSource, refreshes int) (float64, string) {
		mon := system.NewResourceMonitor(src)
		for range refreshes {
			mon.Refresh(context.Background())
		}
		rec := httptest.NewRecorder()
		(&Server{Resources: mon}).resources(rec, httptest.NewRequest(http.MethodGet, "/api/resources", nil))
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		errText, _ := body["error"].(string)
		return body["updated_at"].(float64), errText
	}
	if at, errText := updatedAt(&scriptedResourceSource{at: []int64{0}}, 1); at != 0 || errText == "" {
		t.Fatalf("从没成功过时 updated_at 应为 0 并带错误: %v %q", at, errText)
	}
	old := time.Now().Add(-time.Minute).Unix()
	if at, errText := updatedAt(&scriptedResourceSource{at: []int64{old, 0}}, 2); int64(at) != old || errText == "" {
		t.Fatalf("失败后应报上一份好快照的时间 %d: %v %q", old, at, errText)
	}
}

func TestResourceHandlers(t *testing.T) {
	mon := system.NewResourceMonitor(&fakeResourceSource{})
	mon.Refresh(context.Background())
	s := &Server{Resources: mon}

	rec := httptest.NewRecorder()
	s.resources(rec, httptest.NewRequest(http.MethodGet, "/api/resources", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("resources status = %d, body %s", rec.Code, rec.Body.String())
	}
	var current map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &current); err != nil {
		t.Fatal(err)
	}
	if _, ok := current["host"]; !ok {
		t.Fatalf("响应缺少 host: %s", rec.Body.String())
	}
	if _, ok := current["disks"]; !ok {
		t.Fatalf("响应缺少 disks: %s", rec.Body.String())
	}
	if current["updated_at"].(float64) <= 0 {
		t.Fatalf("updated_at 不正确: %v", current["updated_at"])
	}

	rec = httptest.NewRecorder()
	s.resourceHistory(rec, httptest.NewRequest(http.MethodGet, "/api/resources/history?range=1m", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("history status = %d, body %s", rec.Code, rec.Body.String())
	}
	var history map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history["range"] != "1m" || len(history["points"].([]any)) != 1 {
		t.Fatalf("历史响应不正确: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.resourceHistory(rec, httptest.NewRequest(http.MethodGet, "/api/resources/history?range=3h", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("非法范围应返回 400，实际 %d", rec.Code)
	}
}

// 自身占用：统计成功时输出 self 和 self_updated_at；没有监控器时省略；失败时带 self_error。
func TestResourceSelfUsage(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(home, "danlu.db")
	if err := os.WriteFile(db, make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "logs", "1.log"), make([]byte, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	self := system.NewSelfMonitor(home, db, func() (system.DBStats, error) {
		return system.DBStats{PageCount: 10, PageSize: 4096, FreePages: 2}, nil
	})
	self.Refresh(context.Background())
	mon := system.NewResourceMonitor(&fakeResourceSource{})
	mon.Refresh(context.Background())

	get := func(s *Server) map[string]any {
		t.Helper()
		rec := httptest.NewRecorder()
		s.resources(rec, httptest.NewRequest(http.MethodGet, "/api/resources", nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("resources status = %d, body %s", rec.Code, rec.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		return body
	}

	body := get(&Server{Resources: mon, Self: self})
	u, ok := body["self"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 self: %v", body)
	}
	if u["total_bytes"].(float64) != 1100 || body["self_updated_at"].(float64) <= 0 {
		t.Fatalf("self 内容不正确: %v", u)
	}
	dbStats := u["db"].(map[string]any)
	if dbStats["logical_bytes"].(float64) != 40960 || dbStats["free_bytes"].(float64) != 8192 {
		t.Fatalf("数据库大小不正确: %v", dbStats)
	}
	if _, ok := body["self_error"]; ok {
		t.Fatalf("成功时不应有 self_error: %v", body)
	}

	// 没有自身占用监控器（旧 wiring/测试）时字段省略
	body = get(&Server{Resources: mon})
	if _, ok := body["self"]; ok {
		t.Fatalf("没有监控器时应省略 self: %v", body)
	}

	// 统计失败：保留上一份好快照并带错误
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	self.Refresh(context.Background())
	body = get(&Server{Resources: mon, Self: self})
	if body["self_error"] == nil || body["self_error"] == "" {
		t.Fatalf("失败时应带 self_error: %v", body)
	}
	if u := body["self"].(map[string]any); u["total_bytes"].(float64) != 1100 {
		t.Fatalf("失败后应保留上一份好统计: %v", u)
	}
}
