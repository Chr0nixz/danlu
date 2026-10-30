package store

import (
	"path/filepath"
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
