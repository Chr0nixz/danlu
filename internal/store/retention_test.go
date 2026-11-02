package store

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestRetentionFieldsIndependentFromSchedulerSave(t *testing.T) {
	s := openTest(t)
	j := &Job{Name: "j", Workdir: "/w", Cmd: "x", Status: StatusSucceeded}
	if err := s.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Unix()
	j.EndedAt = &at
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetStarred(j.ID, true); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimPrune(j.ID); err != nil || ok {
		t.Fatalf("标星任务被认领: %t %v", ok, err)
	}
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetJob(j.ID); !got.Starred {
		t.Fatal("调度器旧副本覆盖了标星")
	}
	jobs, total, err := s.ListJobs(JobFilter{Starred: true})
	if err != nil || total != 1 || len(jobs) != 1 {
		t.Fatalf("标星筛选错误: %+v %d %v", jobs, total, err)
	}
	if _, err := s.SetStarred(j.ID, false); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.ClaimPrune(j.ID); err != nil || !ok {
		t.Fatalf("不能认领: %t %v", ok, err)
	}
	if _, err := s.SetStarred(j.ID, true); err != ErrPruning {
		t.Fatalf("清理中不应允许标星: %v", err)
	}
	if err := s.FinishPrune(j.ID, &at); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetJob(j.ID); got.PrunedAt == nil {
		t.Fatal("调度器旧副本覆盖了清理时间")
	}
}

func TestUnusedEnvKeepsReferencesAndActiveCapture(t *testing.T) {
	s := openTest(t)
	j := &Job{Name: "j", Workdir: "/w", Cmd: "x", Status: StatusSucceeded, Recipe: &Recipe{EnvHash: "abc"}}
	if err := s.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	called := false
	remove := func() error { called = true; return nil }
	if removed, err := s.RemoveUnusedEnv("abc", remove); err != nil || removed || called {
		t.Fatalf("删除了仍被引用的环境清单: %t %v", removed, err)
	}
	j.Recipe = &Recipe{Pending: true}
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.RemoveUnusedEnv("orphan", remove); err != nil || removed || called {
		t.Fatalf("采集期间删除了清单: %t %v", removed, err)
	}
	j.Recipe = nil
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	if removed, err := s.RemoveUnusedEnv("orphan", remove); err != nil || !removed || !called {
		t.Fatalf("未删除孤儿清单: %t %v", removed, err)
	}
}

func TestRetentionMigration(t *testing.T) {
	full := slices.Clone(migrations)
	withMigrations(t, full[:9])
	path := filepath.Join(t.TempDir(), "v9.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	j := &Job{Name: "旧任务", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	if err := s.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	s.Close()
	migrations = full
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.GetJob(j.ID)
	if err != nil || got.Starred || got.PrunedAt != nil || got.Pruning {
		t.Fatalf("旧任务迁移错误: %+v %v", got, err)
	}
}
