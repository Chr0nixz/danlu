package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
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
