package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func openTest(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestJobRoundTrip(t *testing.T) {
	s := openTest(t)
	j := &Job{Name: "a", Workdir: "/w", Cmd: "python x.py", Env: map[string]string{"K": "V"},
		GPUsReq: 2, GPUsPinned: []int{0, 1}, Status: StatusQueued}
	if err := s.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	code := 3
	now := time.Now().Unix()
	j.Status, j.ExitCode, j.GPUsAssigned, j.EndedAt, j.PIDStart = StatusFailed, &code, []int{0, 1}, &now, 123456789
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || *got.ExitCode != 3 || got.Env["K"] != "V" ||
		len(got.GPUsAssigned) != 2 || got.PIDStart != 123456789 || got.EndedAt == nil {
		t.Fatalf("字段没有正确保存: %+v", got)
	}
	list, total, err := s.ListJobs(JobFilter{Statuses: []string{StatusFailed}, Query: "x.py"})
	if err != nil || total != 1 || len(list) != 1 {
		t.Fatalf("ListJobs = %d %d %v", len(list), total, err)
	}
	if _, total, _ := s.ListJobs(JobFilter{EndedAfter: now}); total != 1 {
		t.Fatalf("EndedAfter=now 应该包含刚结束的任务，total=%d", total)
	}
	if _, total, _ := s.ListJobs(JobFilter{EndedAfter: now + 1}); total != 0 {
		t.Fatalf("EndedAfter=now+1 不应包含任何任务，total=%d", total)
	}
}

func TestLockout(t *testing.T) {
	s := openTest(t)
	for i := 1; i <= 3; i++ {
		locked, err := s.RecordFailure("1.2.3.4", 3, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		if locked != (i == 3) {
			t.Fatalf("第 %d 次失败 locked=%v", i, locked)
		}
	}
	if s.LockedUntil("1.2.3.4").IsZero() {
		t.Fatal("应该被锁定")
	}
	if !s.LockedUntil("5.6.7.8").IsZero() {
		t.Fatal("其他 IP 不应被锁定")
	}
}

func TestEndedCursor(t *testing.T) {
	s := openTest(t)
	// 结束时间：#1=100 #2=300 #3=200 #4=200，#5 还在排队
	for _, end := range []int64{100, 300, 200, 200, 0} {
		j := &Job{Name: "j", Workdir: "/w", Cmd: "x", Status: StatusQueued}
		if err := s.InsertJob(j); err != nil {
			t.Fatal(err)
		}
		if end > 0 {
			j.Status, j.EndedAt = StatusSucceeded, &end
			s.SaveJob(j)
		}
	}
	ids := func(f JobFilter) []int64 {
		f.ByEnded, f.Limit = true, 2
		list, total, err := s.ListJobs(f)
		if err != nil || total != 4 {
			t.Fatalf("ListJobs total=%d err=%v（游标不应影响 total）", total, err)
		}
		var out []int64
		for _, j := range list {
			out = append(out, j.ID)
		}
		return out
	}
	if got := ids(JobFilter{}); !slices.Equal(got, []int64{2, 4}) {
		t.Fatalf("第一页 = %v，应为 [2 4]", got)
	}
	// 上一页最后一条是 #4（结束于 200）：同一秒的 #3 不能漏掉
	if got := ids(JobFilter{Before: 200, BeforeID: 4}); !slices.Equal(got, []int64{3, 1}) {
		t.Fatalf("第二页 = %v，应为 [3 1]", got)
	}
	if got := ids(JobFilter{Before: 200}); !slices.Equal(got, []int64{1}) {
		t.Fatalf("Before=200 = %v，应为 [1]", got)
	}
}

func TestDeviceTokens(t *testing.T) {
	s := openTest(t)
	id, err := s.CreateDeviceToken("laptop", "h1")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.DeviceTokenID("h1"); err != nil || got != id {
		t.Fatalf("DeviceTokenID = %d %v", got, err)
	}
	s.TouchDeviceToken(id, "1.2.3.4")
	toks, _ := s.ListDeviceTokens()
	if len(toks) != 1 || toks[0].LastUsedAt == nil || toks[0].LastIP != "1.2.3.4" {
		t.Fatalf("ListDeviceTokens = %+v", toks)
	}
	if name, err := s.RevokeDeviceToken(id); err != nil || name != "laptop" {
		t.Fatalf("RevokeDeviceToken = %q %v", name, err)
	}
	if _, err := s.DeviceTokenID("h1"); err != ErrNotFound {
		t.Fatalf("吊销后应查不到，err=%v", err)
	}
	if _, err := s.RevokeDeviceToken(id); err != ErrNotFound {
		t.Fatalf("重复吊销应返回 ErrNotFound，err=%v", err)
	}
}
