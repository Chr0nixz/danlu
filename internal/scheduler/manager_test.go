package scheduler

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/runner"
	"danlu/internal/snapshot"
	"danlu/internal/store"
)

// ---------- 假的执行层、GPU 和会出错的存储 ----------

type fakeExec struct {
	mu       sync.Mutex
	loadEnv  func(ctx context.Context) error // 为空时立即返回
	startErr error
	nextPID  int
	started  []runner.Spec
	alive    map[int]bool
	pidFile  map[int64]int // 包装脚本写下的 pid 文件
	exit     map[int64]int
	terms    []int64
	kills    []int64
	boot     string
}

func newExec() *fakeExec {
	return &fakeExec{nextPID: 1000, alive: map[int]bool{}, pidFile: map[int64]int{}, exit: map[int64]int{}, boot: "boot-1"}
}

// 假进程的启动时间是 pid*10
func startOf(pid int) uint64 { return uint64(pid * 10) }

func (f *fakeExec) LoadEnv(ctx context.Context, _ bool) ([]string, error) {
	if f.loadEnv != nil {
		if err := f.loadEnv(ctx); err != nil {
			return nil, err
		}
	}
	return []string{"PATH=/bin"}, nil
}

func (f *fakeExec) Start(s runner.Spec, _ []string) (int, uint64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.startErr != nil {
		return 0, 0, f.startErr
	}
	f.nextPID++
	pid := f.nextPID
	f.alive[pid], f.pidFile[s.JobID] = true, pid
	f.started = append(f.started, s)
	return pid, startOf(pid), nil
}

func (f *fakeExec) Alive(_ int64, pid int, start uint64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.alive[pid] && (start == 0 || start == startOf(pid))
}

func (f *fakeExec) Adopt(id int64) (int, uint64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pid, ok := f.pidFile[id]; ok && f.alive[pid] {
		return pid, startOf(pid), true
	}
	return 0, 0, false
}

func (f *fakeExec) Launched(id int64) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.pidFile[id]
	return ok
}

func (f *fakeExec) Terminate(id int64, _ int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.terms = append(f.terms, id)
}

func (f *fakeExec) KillAll(id int64, pgid int) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	if pgid > 0 {
		f.kills = append(f.kills, id)
	}
	return 0
}

func (f *fakeExec) ReadExitCode(id int64) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.exit[id]
	return c, ok
}

func (f *fakeExec) LogPath(id int64) string { return filepath.Join("no-such-dir", "x.log") }
func (f *fakeExec) BootID() string          { return f.boot }

// finish 模拟任务进程以 code 退出
func (f *fakeExec) finish(id int64, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive[f.pidFile[id]] = false
	f.exit[id] = code
}

func (f *fakeExec) startedIDs() []int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []int64
	for _, s := range f.started {
		ids = append(ids, s.JobID)
	}
	return ids
}

type fakeGPUs struct {
	mu   sync.Mutex
	gpus []gpu.GPU
	err  error
}

func newGPUs(n int) *fakeGPUs {
	f := &fakeGPUs{}
	for i := 0; i < n; i++ {
		f.gpus = append(f.gpus, gpu.GPU{Index: i, Procs: []gpu.Proc{}})
	}
	return f
}

func (f *fakeGPUs) Latest() ([]gpu.GPU, time.Time, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]gpu.GPU, len(f.gpus))
	for i, g := range f.gpus {
		g.Procs = slices.Clone(g.Procs)
		out[i] = g
	}
	return out, time.Now(), f.err
}

func (f *fakeGPUs) set(fn func(f *fakeGPUs)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

type flakyStore struct {
	*store.Store
	mu       sync.Mutex
	failSave func(j *store.Job) bool
}

func (f *flakyStore) SaveJob(j *store.Job) error {
	f.mu.Lock()
	fail := f.failSave != nil && f.failSave(j)
	f.mu.Unlock()
	if fail {
		return errors.New("模拟写库失败")
	}
	return f.Store.SaveJob(j)
}

func (f *flakyStore) setFail(fn func(j *store.Job) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failSave = fn
}

// ---------- 测试骨架 ----------

type harness struct {
	t    *testing.T
	m    *Manager
	st   *flakyStore
	ex   *fakeExec
	gpus *fakeGPUs
}

func newHarness(t *testing.T, nGPU int) *harness {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	h := &harness{t: t, st: &flakyStore{Store: st}, ex: newExec(), gpus: newGPUs(nGPU)}
	h.m = h.manager()
	return h
}

// manager 新建一个 Manager，共用同一个库和执行层，相当于重启 danlu
func (h *harness) manager() *Manager {
	m := New(h.st, h.gpus, h.ex, Options{KillGrace: time.Hour, SnapshotTimeout: time.Second})
	m.snap = func(context.Context, *store.Job) (*snapshot.Meta, error) { return &snapshot.Meta{}, nil }
	return m
}

// add 直接往库里插一个排队任务（不经过 Submit，免得校验工作目录）
func (h *harness) add(gpus int, pinned ...int) int64 {
	h.t.Helper()
	if len(pinned) > 0 {
		gpus = len(pinned)
	}
	j := &store.Job{Name: "j", Workdir: "/w", Cmd: "x", Env: map[string]string{}, GPUsReq: gpus, GPUsPinned: pinned,
		Status: store.StatusQueued}
	if err := h.st.InsertJob(j); err != nil {
		h.t.Fatal(err)
	}
	return j.ID
}

// tick 跑一轮调度，并等这一轮启动的任务都落库
func (h *harness) tick() {
	h.m.step(false, true)
	h.m.launches.Wait()
}

func (h *harness) job(id int64) *store.Job {
	h.t.Helper()
	j, err := h.st.GetJob(id)
	if err != nil {
		h.t.Fatal(err)
	}
	return j
}

func (h *harness) expect(id int64, status string, gpus ...int) {
	h.t.Helper()
	j := h.job(id)
	if j.Status != status {
		h.t.Fatalf("任务 #%d 状态 %s，期望 %s（%s）", id, j.Status, status, j.FailReason)
	}
	if status == store.StatusRunning && gpus != nil && !slices.Equal(j.GPUsAssigned, gpus) {
		h.t.Fatalf("任务 #%d 分到 GPU %v，期望 %v", id, j.GPUsAssigned, gpus)
	}
}

// within 要求 fn 在 d 之内返回
func within(t *testing.T, d time.Duration, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() { fn(); close(done) }()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("%s 超过 %s 还没返回", what, d)
	}
}

// ---------- 调度 ----------

func TestStrictFIFO(t *testing.T) {
	h := newHarness(t, 4)
	a, b, c := h.add(2), h.add(3), h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 0, 1)
	h.expect(b, store.StatusQueued)
	h.expect(c, store.StatusQueued) // 只要 1 张卡、也有空卡，但排在放不下的 b 后面
	h.ex.finish(a, 0)
	h.tick()
	h.expect(a, store.StatusSucceeded)
	h.expect(b, store.StatusRunning, 0, 1, 2)
	h.expect(c, store.StatusRunning, 3)
	if p1, p2 := h.job(b).MasterPort, h.job(c).MasterPort; p1 == 0 || p1 == p2 {
		t.Fatalf("端口应该各不相同: %d %d", p1, p2)
	}
}

func TestPinnedWaitsForCard(t *testing.T) {
	h := newHarness(t, 2)
	a, b, c := h.add(0, 1), h.add(0, 1), h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 1)
	h.expect(b, store.StatusQueued)
	h.expect(c, store.StatusQueued)
	h.ex.finish(a, 0)
	h.tick()
	h.expect(b, store.StatusRunning, 1)
	h.expect(c, store.StatusRunning, 0)
}

func TestExternalProcessBlocksCard(t *testing.T) {
	h := newHarness(t, 2)
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs = []gpu.Proc{{PID: 4242, User: "alice"}} })
	a, b := h.add(1), h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 1)
	h.expect(b, store.StatusQueued)
}

func TestTooManyGPUsFails(t *testing.T) {
	h := newHarness(t, 2)
	a, b := h.add(3), h.add(0, 5)
	h.tick()
	h.expect(a, store.StatusFailed)
	h.expect(b, store.StatusFailed)
}

// GPU 数据过期或者查不到进程时不分卡；排在前面、不用卡的任务照常启动
func TestUntrustedGPUData(t *testing.T) {
	for _, err := range []error{gpu.ErrProcsUnknown, errors.New("GPU 数据已经 30 秒没有更新")} {
		h := newHarness(t, 2)
		h.gpus.set(func(f *fakeGPUs) { f.err = err })
		cpu, g := h.add(0), h.add(1)
		h.tick()
		h.expect(cpu, store.StatusRunning)
		h.expect(g, store.StatusQueued)
		h.gpus.set(func(f *fakeGPUs) { f.err = nil })
		h.tick()
		h.expect(g, store.StatusRunning, 0)
	}
}

// ---------- 启动 ----------

func TestStartFailure(t *testing.T) {
	h := newHarness(t, 1)
	h.ex.startErr = errors.New("fork 失败")
	a := h.add(1)
	h.tick()
	h.expect(a, store.StatusFailed)
	if r := h.job(a).FailReason; r != "启动失败: fork 失败" {
		t.Fatalf("FailReason = %q", r)
	}
}

func TestLoadEnvFailure(t *testing.T) {
	h := newHarness(t, 1)
	h.ex.loadEnv = func(context.Context) error { return errors.New("加载 .bashrc 超时（10 秒）") }
	a := h.add(0)
	h.tick()
	h.expect(a, store.StatusFailed)
	if len(h.ex.startedIDs()) != 0 {
		t.Fatal("环境没加载成功就不应该启动")
	}
}

func TestSnapshotFailureStillStarts(t *testing.T) {
	h := newHarness(t, 1)
	h.m.snap = func(context.Context, *store.Job) (*snapshot.Meta, error) { return nil, errors.New("快照超时") }
	a := h.add(0)
	h.tick()
	h.expect(a, store.StatusRunning)
	if j := h.job(a); j.SnapshotOK || !strings.Contains(j.SnapshotNote, "快照超时") || j.PID == 0 {
		t.Fatalf("快照失败应该记在任务上并照常启动: %+v", j)
	}
}

// 加载 .bashrc 卡住时，提交、取消和调度循环都不受影响（第 4.1 节）
func TestSlowLoadEnvDoesNotBlock(t *testing.T) {
	h := newHarness(t, 2)
	release := make(chan struct{})
	h.ex.loadEnv = func(context.Context) error { <-release; return nil }
	a, b := h.add(1), h.add(1)
	h.m.step(false, true)
	h.expect(a, store.StatusRunning) // 已经占位：卡和端口都记下了，pid 还是 0
	if j := h.job(a); j.PID != 0 || len(j.GPUsAssigned) != 1 || j.MasterPort == 0 || j.StartedAt == nil {
		t.Fatalf("占位不完整: %+v", j)
	}

	within(t, time.Second, "Submit", func() {
		if _, err := h.m.Submit(SubmitReq{Workdir: "/", Cmd: "true"}); err != nil {
			t.Error(err)
		}
	})
	within(t, time.Second, "Cancel", func() {
		if _, err := h.m.Cancel(a); err != nil {
			t.Error(err)
		}
	})
	within(t, time.Second, "调度循环", func() { h.m.step(true, true) })
	h.expect(a, store.StatusRunning)

	close(release)
	h.m.launches.Wait()
	h.expect(a, store.StatusCancelled)
	h.expect(b, store.StatusRunning)
	if ids := h.ex.startedIDs(); slices.Contains(ids, a) {
		t.Fatalf("启动前被取消的任务不应该启动: %v", ids)
	}
	if len(h.ex.terms) != 0 {
		t.Fatalf("还没有进程，不应该发信号: %v", h.ex.terms)
	}
}

// 占位时写库失败：不启动，下一轮再试
func TestReserveSaveFails(t *testing.T) {
	h := newHarness(t, 1)
	h.st.setFail(func(j *store.Job) bool { return j.Status == store.StatusRunning && j.PID == 0 })
	a := h.add(1)
	h.tick()
	h.expect(a, store.StatusQueued)
	if len(h.ex.startedIDs()) != 0 {
		t.Fatal("占位没写进库就不应该启动")
	}
	h.st.setFail(nil)
	h.tick()
	h.expect(a, store.StatusRunning)
	if ids := h.ex.startedIDs(); !slices.Equal(ids, []int64{a}) {
		t.Fatalf("启动记录 %v", ids)
	}
}

// 启动后写库失败：下一轮用内存里的 pid 继续监控，不会再启动一次；danlu 重启后靠 pid 文件认领（第 5.1 节）
func TestSaveAfterStartFails(t *testing.T) {
	h := newHarness(t, 1)
	h.st.setFail(func(j *store.Job) bool { return j.PID > 0 })
	a := h.add(1)
	h.tick()
	if j := h.job(a); j.Status != store.StatusRunning || j.PID != 0 {
		t.Fatalf("库里应该还是占位状态: %+v", j)
	}
	h.tick()
	h.tick()
	if ids := h.ex.startedIDs(); !slices.Equal(ids, []int64{a}) {
		t.Fatalf("写库失败后又启动了一次: %v", ids)
	}

	// danlu 在写进库之前就退出了：新的 Manager 从 pid 文件认领
	m2 := h.manager()
	m2.Recover()
	h.st.setFail(nil)
	h.m = m2
	h.tick()
	if j := h.job(a); j.Status != store.StatusRunning || j.PID != 1001 || j.PIDStart != startOf(1001) {
		t.Fatalf("应该认领 pid 1001: %+v", j)
	}
	if ids := h.ex.startedIDs(); !slices.Equal(ids, []int64{a}) {
		t.Fatalf("重启后又启动了一次: %v", ids)
	}
	h.ex.finish(a, 0)
	h.tick()
	h.expect(a, store.StatusSucceeded)
}

// 写库一直失败也在内存里接着跑，写成功后以内存里的为准
func TestPendingSaveRetried(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	h.tick()
	h.st.setFail(func(j *store.Job) bool { return j.Finished() })
	h.ex.finish(a, 3)
	h.tick()
	h.expect(a, store.StatusRunning) // 收尾没写进库
	h.st.setFail(nil)
	h.tick()
	h.expect(a, store.StatusFailed)
	if j := h.job(a); j.ExitCode == nil || *j.ExitCode != 3 {
		t.Fatalf("退出码 = %v", j.ExitCode)
	}
}

// danlu 停止时，还在准备的任务放回队列，不会只启动一半
func TestShutdownDuringLaunch(t *testing.T) {
	h := newHarness(t, 1)
	h.ex.loadEnv = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	a := h.add(1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.m.Run(ctx); close(done) }()
	h.m.Wake()
	for i := 0; h.job(a).Status != store.StatusRunning; i++ {
		if i == 100 {
			t.Fatal("任务没有占位")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	within(t, 2*time.Second, "Run", func() { <-done })
	j := h.job(a)
	if j.Status != store.StatusQueued || j.StartedAt != nil || len(j.GPUsAssigned) != 0 || j.MasterPort != 0 {
		t.Fatalf("应该放回队列并清空占位: %+v", j)
	}
	if len(h.ex.startedIDs()) != 0 {
		t.Fatal("停止时不应该再启动进程")
	}
}

// ---------- 取消 ----------

func TestCancelQueued(t *testing.T) {
	h := newHarness(t, 0)
	a := h.add(1) // 没有 GPU 信息，一直排队
	h.tick()
	if _, err := h.m.Cancel(a); err != nil {
		t.Fatal(err)
	}
	h.expect(a, store.StatusCancelled)
	if _, err := h.m.Cancel(a); !IsValidation(err) {
		t.Fatalf("取消已结束的任务应该返回校验错误: %v", err)
	}
}

func TestCancelRunningKillsAfterGrace(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	h.tick()
	if _, err := h.m.Cancel(a); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(h.ex.terms, []int64{a}) {
		t.Fatalf("应该先发 SIGTERM: %v", h.ex.terms)
	}
	h.tick()
	if len(h.ex.kills) != 0 {
		t.Fatal("宽限期内不应该强杀")
	}
	h.m.opt.KillGrace = time.Millisecond
	time.Sleep(5 * time.Millisecond)
	h.tick()
	if !slices.Equal(h.ex.kills, []int64{a}) {
		t.Fatalf("过了宽限期应该强杀: %v", h.ex.kills)
	}
	h.ex.finish(a, 143)
	h.tick()
	h.expect(a, store.StatusCancelled)
}

// ---------- 重启后接管（第 5.1 节的表） ----------

func TestRecover(t *testing.T) {
	h := newHarness(t, 4)
	h.ex.boot = "boot-2" // 服务器重启过
	h.m = h.manager()
	type row struct {
		name             string
		pid              int
		boot             string
		alive, pidFile   bool
		exit             *int
		cancel           bool
		want, wantReason string
	}
	one, zero := 1, 0
	rows := []row{
		{name: "进程还在", pid: 2001, alive: true, pidFile: true, want: store.StatusRunning},
		{name: "跑完了", pid: 2002, pidFile: true, exit: &zero, want: store.StatusSucceeded},
		{name: "开机标识变了", pid: 2003, boot: "boot-1", pidFile: true, want: store.StatusFailed, wantReason: "服务器重启"},
		{name: "被外部终止", pid: 2004, boot: "boot-2", pidFile: true, want: store.StatusFailed, wantReason: "进程被外部终止"},
		{name: "启动途中，进程在", pid: 0, alive: true, pidFile: true, want: store.StatusRunning},
		{name: "启动途中，已经跑完", pid: 0, pidFile: true, exit: &one, want: store.StatusFailed, wantReason: "退出码 1"},
		{name: "启动途中，没有 pid 文件", pid: 0, want: store.StatusQueued},
		{name: "启动途中被取消，没有 pid 文件", pid: 0, cancel: true, want: store.StatusCancelled},
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		id := h.add(1)
		ids[i] = id
		now := time.Now().Unix()
		j := h.job(id)
		j.Status, j.PID, j.StartedAt, j.GPUsAssigned, j.MasterPort, j.BootID, j.CancelRequested =
			store.StatusRunning, r.pid, &now, []int{i % 4}, 30000+i, r.boot, r.cancel
		realPID := r.pid
		if realPID == 0 {
			realPID = 3000 + i
		} else {
			j.PIDStart = startOf(realPID)
		}
		if err := h.st.SaveJob(j); err != nil {
			t.Fatal(err)
		}
		h.ex.alive[realPID] = r.alive
		if r.pidFile {
			h.ex.pidFile[id] = realPID
		}
		if r.exit != nil {
			h.ex.exit[id] = *r.exit
		}
	}
	h.m.Recover()
	for i, r := range rows {
		j := h.job(ids[i])
		if j.Status != r.want || !strings.Contains(j.FailReason, r.wantReason) {
			t.Errorf("%s: 状态 %s（%q），期望 %s（%q）", r.name, j.Status, j.FailReason, r.want, r.wantReason)
		}
		switch r.want {
		case store.StatusRunning:
			if j.PID == 0 || j.PIDStart != startOf(j.PID) {
				t.Errorf("%s: 应该记下 pid 和启动时间: %d %d", r.name, j.PID, j.PIDStart)
			}
		case store.StatusQueued:
			if j.StartedAt != nil || len(j.GPUsAssigned) != 0 || j.MasterPort != 0 {
				t.Errorf("%s: 放回队列时应该清空占位: %+v", r.name, j)
			}
		}
	}
	if len(h.ex.startedIDs()) != 0 {
		t.Fatal("Recover 不应该启动任何任务")
	}
}
