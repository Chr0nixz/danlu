package store

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
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
		InheritBashrc: true, GPUsReq: 2, GPUsPinned: []int{0, 1}, Status: StatusQueued}
	if err := s.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	code := 3
	now := time.Now().Unix()
	traceback := int64(456)
	j.Status, j.ExitCode, j.GPUsAssigned, j.EndedAt, j.PIDStart = StatusFailed, &code, []int{0, 1}, &now, 123456789
	j.FailKind, j.ExtSeen, j.OOMBase, j.TracebackOffset = "cuda_oom", true, 12, &traceback
	j.Recipe = &Recipe{EnvHash: "abc123", Machine: &RecipeMachine{Hostname: "node-a", DriverVersion: "560.94",
		CUDAVersion: "12.6", GPUs: []RecipeGPU{{Index: 0, UUID: "GPU-abc", Name: "A100"}}}}
	j.OutputDir = "/outputs/project/1-a"
	if err := s.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetJob(j.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusFailed || *got.ExitCode != 3 || got.Env["K"] != "V" || !got.InheritBashrc ||
		len(got.GPUsAssigned) != 2 || got.PIDStart != 123456789 || got.EndedAt == nil ||
		got.FailKind != "cuda_oom" || !got.ExtSeen || got.OOMBase != 12 || got.TracebackOffset == nil || *got.TracebackOffset != traceback ||
		got.Recipe == nil || got.Recipe.EnvHash != "abc123" || got.Recipe.Machine == nil ||
		got.Recipe.Machine.DriverVersion != "560.94" || got.OutputDir != "/outputs/project/1-a" {
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

// withMigrations 临时换掉迁移列表，测试结束后换回来
func withMigrations(t *testing.T, m []string) {
	t.Helper()
	orig := migrations
	migrations = m
	t.Cleanup(func() { migrations = orig })
}

func userVersion(t *testing.T, s *Store) int {
	t.Helper()
	var v int
	if err := s.db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestMigrationIsAtomic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	n := len(migrations)
	// 第一条语句成功、第二条失败：整次迁移都不能生效
	withMigrations(t, append(slices.Clone(migrations), "CREATE TABLE half (a); CREATE TABLE jobs (b);"))
	if _, err := Open(path); err == nil {
		t.Fatal("迁移失败时 Open 应该返回错误")
	}
	migrations = migrations[:n]
	s = openAt(t, path)
	if v := userVersion(t, s); v != n {
		t.Fatalf("user_version = %d，应该还是 %d", v, n)
	}
	var cnt int
	s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name='half'").Scan(&cnt)
	if cnt != 0 {
		t.Fatal("失败的迁移留下了半张表")
	}
}

func TestRefuseNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s := openAt(t, path)
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(migrations)+1)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil || !strings.Contains(err.Error(), "新版本") {
		t.Fatalf("库比程序新时应该拒绝打开，err=%v", err)
	}
}

func TestBackupBeforeUpgrade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	full := migrations
	withMigrations(t, full[:len(full)-1])
	s := openAt(t, path)
	if _, err := s.db.Exec("INSERT INTO jobs(id,name,workdir,cmd,status,created_at) VALUES (1,'old','/w','x','queued',0)"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	backups, _ := filepath.Glob(filepath.Join(dir, "backup", "*.db"))
	if len(backups) != 0 {
		t.Fatalf("新建数据库时不应该备份: %v", backups)
	}

	migrations = full
	s = openAt(t, path)
	if v := userVersion(t, s); v != len(full) {
		t.Fatalf("升级后 user_version = %d", v)
	}
	backups, _ = filepath.Glob(filepath.Join(dir, "backup", "*.db"))
	if len(backups) != 1 || !strings.HasSuffix(backups[0], fmt.Sprintf("-v%d.db", len(full)-1)) {
		t.Fatalf("升级前应该备份一份旧版本的库: %v", backups)
	}
	old := openAt(t, backups[0])
	if got, err := old.GetJob(1); err != nil || got.Name != "old" || got.CodeFrom != nil {
		t.Fatalf("备份里读不到旧数据: %v %v", got, err)
	}

	// 只保留最近的 keepBackups 份
	for i := 0; i < keepBackups+2; i++ {
		os.WriteFile(filepath.Join(dir, "backup", fmt.Sprintf("danlu-2000010%d-000000-v1.db", i)), nil, 0o600)
	}
	if err := s.backup(filepath.Join(dir, "backup"), 9); err != nil {
		t.Fatal(err)
	}
	backups, _ = filepath.Glob(filepath.Join(dir, "backup", "*.db"))
	if len(backups) != keepBackups || !strings.HasSuffix(backups[len(backups)-1], "-v9.db") {
		t.Fatalf("备份清理不对: %v", backups)
	}
}

func TestRepeatedBackupSameVersion(t *testing.T) {
	s := openTest(t)
	dir := t.TempDir()
	for range 2 {
		if err := s.backup(dir, len(migrations)); err != nil {
			t.Fatalf("同版本连续备份失败: %v", err)
		}
	}
	backups, err := filepath.Glob(filepath.Join(dir, "danlu-*-v*.db"))
	if err != nil || len(backups) != 2 {
		t.Fatalf("连续备份应该保留两个独立文件: %v, %v", backups, err)
	}
}

func openAt(t *testing.T, path string) *Store {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSettings(t *testing.T) {
	s := openTest(t)
	if _, err := s.GetSetting("auth.password_hash"); err != ErrNotFound {
		t.Fatalf("没有设置时应返回 ErrNotFound，err=%v", err)
	}
	for _, v := range []string{"a", "b"} {
		if err := s.SetSetting("auth.password_hash", v); err != nil {
			t.Fatal(err)
		}
		if got, err := s.GetSetting("auth.password_hash"); err != nil || got != v {
			t.Fatalf("GetSetting = %q %v，期望 %q", got, err, v)
		}
	}
}

func TestBootIDRoundTrip(t *testing.T) {
	s := openTest(t)
	j := &Job{Name: "a", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	s.InsertJob(j)
	j.BootID = "boot-1"
	s.SaveJob(j)
	if got, _ := s.GetJob(j.ID); got.BootID != "boot-1" {
		t.Fatalf("BootID = %q", got.BootID)
	}
}
