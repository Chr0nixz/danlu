package store

import (
	"errors"
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

// 排队任务多于一页时，按 id 从旧到新取到的是队首那一页
func TestListJobsAscending(t *testing.T) {
	s := openTest(t)
	for range 5 {
		if err := s.InsertJob(&Job{Name: "j", Workdir: "/w", Cmd: "x", Status: StatusQueued}); err != nil {
			t.Fatal(err)
		}
	}
	list, total, err := s.ListJobs(JobFilter{Statuses: []string{StatusQueued}, Ascending: true, Limit: 2})
	if err != nil || total != 5 {
		t.Fatalf("total=%d err=%v", total, err)
	}
	if len(list) != 2 || list[0].ID != 1 || list[1].ID != 2 {
		t.Fatalf("应取队首的 #1 #2: %+v", list)
	}
	if list, _, _ := s.ListJobs(JobFilter{Statuses: []string{StatusQueued}, Limit: 2}); list[0].ID != 5 {
		t.Fatalf("默认仍是从新到旧: %+v", list)
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
	base := slices.IndexFunc(full, func(m string) bool { return strings.Contains(m, "ADD COLUMN memo ") })
	if base < 1 {
		t.Fatal("缺少 F1 之前的迁移基线")
	}
	withMigrations(t, full[:base])
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
	if len(backups) != 1 || !strings.HasSuffix(backups[0], fmt.Sprintf("-v%d.db", base)) {
		t.Fatalf("升级前应该备份一份旧版本的库: %v", backups)
	}
	old := openAt(t, backups[0])
	if got, err := old.GetJob(1); err != nil || got.Name != "old" || got.CodeFrom != nil || got.Memo != "" || got.NotifyDone || got.Profile != nil {
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

// 迁移持续失败时（systemd 每 3 秒重启一次）：库停在原来的版本，不留中间版本；库没变过就不重复备份，
// 唯一能给旧程序用的备份不会被轮换掉。旧程序又写过库之后，再升级时重新备份
func TestFailingMigrationKeepsBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	full := slices.Clone(migrations)
	base := len(full) - 2
	withMigrations(t, full[:base])
	s := openAt(t, path)
	s.Close()

	backups := func() []string {
		t.Helper()
		b, _ := filepath.Glob(filepath.Join(dir, "backup", "*.db"))
		return b
	}
	migrations = append(slices.Clone(full), "CREATE TABLE half (a); CREATE TABLE jobs (b);")
	for range keepBackups + 2 {
		if _, err := Open(path); err == nil {
			t.Fatal("迁移失败时 Open 应该返回错误")
		}
	}
	if b := backups(); len(b) != 1 || !strings.HasSuffix(b[0], fmt.Sprintf("-v%d.db", base)) {
		t.Fatalf("库没变过时应只备份一次，而且是原来的版本: %v", b)
	}
	migrations = full[:base]
	s = openAt(t, path)
	if v := userVersion(t, s); v != base {
		t.Fatalf("迁移失败后 user_version = %d，应该还是 %d（不能停在中间版本）", v, base)
	}
	// 旧程序接着用、写了库：下次升级前要重新备份
	time.Sleep(10 * time.Millisecond)
	if _, err := s.db.Exec("INSERT INTO jobs(id,name,workdir,cmd,status,created_at) VALUES (1,'new','/w','x','queued',0)"); err != nil {
		t.Fatal(err)
	}
	s.Close()
	migrations = full
	s = openAt(t, path)
	if v := userVersion(t, s); v != len(full) {
		t.Fatalf("升级后 user_version = %d", v)
	}
	if b := backups(); len(b) != 2 {
		t.Fatalf("库变过之后应该重新备份: %v", b)
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

// 命令行用的 OpenCurrent 从不迁移：旧库原样留着、不备份，由调用方决定能不能迁移
func TestOpenCurrent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.db")
	var old *OutdatedError
	if _, err := OpenCurrent(path); !errors.As(err, &old) || old.Have != 0 || old.Want != len(migrations) {
		t.Fatalf("还没建表的库也要先迁移: %v", err)
	}

	full := migrations
	withMigrations(t, full[:len(full)-1])
	openAt(t, path).Close()
	migrations = full
	if _, err := OpenCurrent(path); !errors.As(err, &old) || old.Have != len(full)-1 || !strings.Contains(err.Error(), "旧版本") {
		t.Fatalf("库比程序旧时应返回 OutdatedError: %v", err)
	}
	s := openAt(t, filepath.Join(dir, "t.db"))
	if v := userVersion(t, s); v != len(full) {
		t.Fatalf("Open 照常迁移: %d", v)
	}
	s.Close()
	if backups, _ := filepath.Glob(filepath.Join(dir, "backup", "*.db")); len(backups) != 1 {
		t.Fatalf("只有 Open 迁移时备份一次: %v", backups)
	}

	s, err := OpenCurrent(path)
	if err != nil {
		t.Fatalf("当前版本的库直接打开: %v", err)
	}
	if _, err := s.db.Exec(fmt.Sprintf("PRAGMA user_version = %d", len(full)+1)); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := OpenCurrent(path); err == nil || errors.As(err, &old) || !strings.Contains(err.Error(), "新版本") {
		t.Fatalf("库比程序新时拒绝打开: %v", err)
	}
}

func TestReruns(t *testing.T) {
	s := openTest(t)
	parent := &Job{Name: "p", Workdir: "/w", Cmd: "x", Status: StatusFailed}
	if err := s.InsertJob(parent); err != nil {
		t.Fatal(err)
	}
	var kids []int64
	for range 3 {
		k := &Job{Name: "k", Workdir: "/w", Cmd: "x", Status: StatusQueued, RerunOf: &parent.ID}
		if err := s.InsertJob(k); err != nil {
			t.Fatal(err)
		}
		kids = append(kids, k.ID)
	}
	got, err := s.Reruns(parent.ID, 2)
	if err != nil || len(got) != 2 || got[0].ID != kids[0] || got[1].ID != kids[1] || got[0].Status != StatusQueued {
		t.Fatalf("重跑列表不对: %+v %v", got, err)
	}
	if got, err := s.Reruns(kids[0], 5); err != nil || len(got) != 0 {
		t.Fatalf("没有重跑时应为空: %+v %v", got, err)
	}
}

// 提交幂等键：读写往返；同一个 key 只能插一次；不带 key 的任务互不影响
func TestSubmitKeyUnique(t *testing.T) {
	s := openTest(t)
	a := &Job{Name: "a", Workdir: "/w", Cmd: "x", Status: StatusQueued, SubmitKey: "k-1"}
	if err := s.InsertJob(a); err != nil {
		t.Fatal(err)
	}
	if err := s.InsertJob(&Job{Name: "b", Workdir: "/w", Cmd: "x", Status: StatusQueued, SubmitKey: "k-1"}); !errors.Is(err, ErrSubmitKeyExists) {
		t.Fatalf("重复的 key 应返回 ErrSubmitKeyExists，得到 %v", err)
	}
	for range 2 {
		if err := s.InsertJob(&Job{Name: "c", Workdir: "/w", Cmd: "x", Status: StatusQueued}); err != nil {
			t.Fatalf("不带 key 的任务不受唯一索引限制: %v", err)
		}
	}
	got, err := s.JobBySubmitKey("k-1")
	if err != nil || got.ID != a.ID || got.SubmitKey != "k-1" {
		t.Fatalf("按 key 查到 %+v %v", got, err)
	}
	if j, err := s.GetJob(a.ID + 1); err != nil || j.SubmitKey != "" {
		t.Fatalf("不带 key 的任务 SubmitKey 应为空: %+v %v", j, err)
	}
	if _, err := s.JobBySubmitKey("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("没有的 key 应返回 ErrNotFound，得到 %v", err)
	}
}

// 队列顺序（FEATURES 3.2）：升级时旧任务的 queue_order 等于 id；新任务插入时也等于 id；
// 只筛排队任务的升序列表按 queue_order 排
func TestQueueOrderMigrationAndListing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	full := migrations
	base := slices.IndexFunc(full, func(m string) bool { return strings.Contains(m, "ADD COLUMN queue_order") })
	if base < 1 {
		t.Fatal("缺少队列顺序迁移")
	}
	withMigrations(t, full[:base])
	s := openAt(t, path)
	for _, id := range []int{1, 2, 3} {
		if _, err := s.db.Exec("INSERT INTO jobs(id,name,workdir,cmd,status,created_at) VALUES (?,'old','/w','x','queued',0)", id); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
	migrations = full
	s = openAt(t, path)
	for _, id := range []int64{1, 2, 3} {
		if j, err := s.GetJob(id); err != nil || j.QueueOrder != id || j.Held {
			t.Fatalf("迁移后 #%d: %+v %v", id, j, err)
		}
	}
	j := &Job{Name: "new", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	if err := s.InsertJob(j); err != nil || j.QueueOrder != j.ID || j.ID != 4 {
		t.Fatalf("新任务: %+v %v", j, err)
	}
	if got, _ := s.GetJob(4); got.QueueOrder != 4 {
		t.Fatalf("库里的 queue_order = %d", got.QueueOrder)
	}
	if err := s.SetQueueOrder([]QueueOrder{{ID: 3, Order: 1}, {ID: 1, Order: 2}, {ID: 2, Order: 3}}); err != nil {
		t.Fatal(err)
	}
	ids := func(jobs []*Job) []int64 {
		var out []int64
		for _, j := range jobs {
			out = append(out, j.ID)
		}
		return out
	}
	queued, _ := s.JobsByStatus(StatusQueued)
	if got := ids(queued); !slices.Equal(got, []int64{3, 1, 2, 4}) {
		t.Fatalf("JobsByStatus 顺序 %v", got)
	}
	list, _, _ := s.ListJobs(JobFilter{Statuses: []string{StatusQueued}, Ascending: true})
	if got := ids(list); !slices.Equal(got, []int64{3, 1, 2, 4}) {
		t.Fatalf("ListJobs 顺序 %v", got)
	}
	// 混着别的状态时仍按 id
	list, _, _ = s.ListJobs(JobFilter{Statuses: []string{StatusQueued, StatusRunning}, Ascending: true})
	if got := ids(list); !slices.Equal(got, []int64{1, 2, 3, 4}) {
		t.Fatalf("混合状态的升序应按 id: %v", got)
	}
}

// 只剩暂缓的排队任务时，收尾记排空
func TestDrainIgnoresHeldJobs(t *testing.T) {
	s := openTest(t)
	running := &Job{Name: "r", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	held := &Job{Name: "h", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	for _, j := range []*Job{running, held} {
		if err := s.InsertJob(j); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetHeld([]int64{held.ID}, true); err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	running.Status, running.EndedAt = StatusSucceeded, &now
	id := running.ID
	finish := &Event{Kind: EventJobFinished, JobID: &id, DedupeKey: "f"}
	drain := &Event{Kind: EventQueueDrained, DedupeKey: "d"}
	if err := s.Transition(running, finish, drain); err != nil {
		t.Fatal(err)
	}
	events, _ := s.EventsAfter(0, 10)
	if len(events) != 2 || events[1].Kind != EventQueueDrained {
		t.Fatalf("只剩暂缓任务时应记排空: %+v", events)
	}
}
