package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
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
	mu         sync.Mutex
	loadEnv    func(ctx context.Context) error // 为空时立即返回
	startErr   error
	nextPID    int
	started    []runner.Spec
	alive      map[int]bool
	pidFile    map[int64]int // 包装脚本写下的 pid 文件
	exit       map[int64]int
	terms      []int64
	kills      []int64
	boot       string
	leftovers  int
	logRoot    string
	oomCount   int64
	oomKnown   bool
	packages   []byte
	packageErr error
	packageFn  func(context.Context, runner.Spec, []string) ([]byte, error)
	manifestFn func(dir string) error // 为空时立即返回
}

func newExec() *fakeExec {
	return &fakeExec{nextPID: 1000, alive: map[int]bool{}, pidFile: map[int64]int{}, exit: map[int64]int{}, boot: "boot-1", oomKnown: true,
		packages: []byte("python==3.12\n")}
}

func TestSnapshotRerunDoesNotStartWhenRestoreFails(t *testing.T) {
	h := newHarness(t, 1)
	h.m.opt.SnapshotDir, h.m.opt.RunRoot = t.TempDir(), t.TempDir()
	source := h.job(h.add(0))
	at := time.Now().Unix()
	source.Status, source.SnapshotOK, source.EndedAt = store.StatusSucceeded, true, &at
	if err := h.st.SaveJob(source); err != nil {
		t.Fatal(err)
	}
	// 数据库仍记录有快照，但文件已经不在磁盘上。
	j, err := h.m.Submit(SubmitReq{Workdir: "/w", Cmd: "python train.py", CodeFrom: &source.ID})
	if err != nil {
		t.Fatal(err)
	}
	h.tick()
	got := h.job(j.ID)
	if got.Status != store.StatusFailed || got.FailKind != "start" || !strings.Contains(got.FailReason, "恢复任务") {
		t.Fatalf("恢复错误未记录为启动失败: %+v", got)
	}
	if len(h.ex.startedIDs()) != 0 {
		t.Fatal("恢复失败的任务启动了进程")
	}
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

func (f *fakeExec) Launched(id int64) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	pid, ok := f.pidFile[id]
	return pid, ok
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
		return 0
	}
	n := f.leftovers
	f.leftovers = 0
	return n
}

func (f *fakeExec) ReadExitCode(id int64) (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	c, ok := f.exit[id]
	return c, ok
}

func (f *fakeExec) OOMKills() (int64, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.oomCount, f.oomKnown
}

func (f *fakeExec) CapturePackages(ctx context.Context, s runner.Spec, env []string) ([]byte, error) {
	f.mu.Lock()
	fn, packages, err := f.packageFn, append([]byte(nil), f.packages...), f.packageErr
	f.mu.Unlock()
	if fn != nil {
		return fn(ctx, s, env)
	}
	return packages, err
}

func (f *fakeExec) StorePackages(packages []byte) (string, error) {
	f.mu.Lock()
	err := f.packageErr
	f.mu.Unlock()
	if err != nil {
		return "", err
	}
	return "fake-package-hash", nil
}

func (f *fakeExec) WriteManifest(dir string, _ []byte) error {
	if f.manifestFn != nil {
		return f.manifestFn(dir)
	}
	return nil
}

func (f *fakeExec) LogPath(id int64) string {
	if f.logRoot != "" {
		return filepath.Join(f.logRoot, fmt.Sprintf("%d.log", id))
	}
	return filepath.Join("no-such-dir", "x.log")
}
func (f *fakeExec) BootID() string { return f.boot }

// finish 模拟任务进程以 code 退出
func (f *fakeExec) finish(id int64, code int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive[f.pidFile[id]] = false
	f.exit[id] = code
}

func (f *fakeExec) crash(id int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.alive[f.pidFile[id]] = false
	delete(f.exit, id)
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
	mu             sync.Mutex
	failSave       func(j *store.Job) bool
	failQueue      bool
	failQueueRead  bool
	failQueuedRead bool
}

func (f *flakyStore) GetSetting(key string) (string, error) {
	f.mu.Lock()
	fail := f.failQueueRead
	f.mu.Unlock()
	if fail && key == queueStateKey {
		return "", errors.New("模拟队列读取失败")
	}
	return f.Store.GetSetting(key)
}

func (f *flakyStore) JobsByStatus(status string) ([]*store.Job, error) {
	f.mu.Lock()
	fail := f.failQueuedRead
	f.mu.Unlock()
	if fail && status == store.StatusQueued {
		return nil, errors.New("模拟排队任务读取失败")
	}
	return f.Store.JobsByStatus(status)
}

func (f *flakyStore) SetSettingWithEvent(key, value string, event *store.Event) error {
	f.mu.Lock()
	fail := f.failQueue
	f.mu.Unlock()
	if fail {
		return errors.New("模拟队列写库失败")
	}
	return f.Store.SetSettingWithEvent(key, value, event)
}

func (f *flakyStore) SaveJob(j *store.Job) error {
	if f.fails(j) {
		return errors.New("模拟写库失败")
	}
	return f.Store.SaveJob(j)
}

func (f *flakyStore) Transition(j *store.Job, events ...*store.Event) error {
	if f.fails(j) {
		return errors.New("模拟写库失败")
	}
	return f.Store.Transition(j, events...)
}

func (f *flakyStore) TransitionWithSetting(j *store.Job, key, value string, events ...*store.Event) error {
	f.mu.Lock()
	fail := f.failQueue
	f.mu.Unlock()
	if fail || f.fails(j) {
		return errors.New("模拟任务与队列写库失败")
	}
	return f.Store.TransitionWithSetting(j, key, value, events...)
}

func (f *flakyStore) fails(j *store.Job) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	fail := f.failSave != nil && f.failSave(j)
	return fail
}

func (f *flakyStore) setFail(fn func(j *store.Job) bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failSave = fn
}

func (f *flakyStore) setQueueFailure(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failQueue = fail
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
	ex := newExec()
	ex.logRoot = filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(ex.logRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, st: &flakyStore{Store: st}, ex: ex, gpus: newGPUs(nGPU)}
	h.m = h.manager()
	return h
}

// manager 新建一个 Manager，共用同一个库和执行层，相当于重启 danlu
func (h *harness) manager() *Manager {
	m := New(h.st, h.gpus, h.ex, Options{KillGrace: time.Hour, SnapshotTimeout: time.Second,
		OutputRoot: filepath.Join(h.t.TempDir(), "outputs")})
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
	h.m.captures.Wait()
	h.m.manifests.Wait()
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

func (h *harness) events(id int64, want ...string) []store.Event {
	h.t.Helper()
	all, err := h.st.EventsAfter(0, 1000)
	if err != nil {
		h.t.Fatal(err)
	}
	var events []store.Event
	var kinds []string
	for _, e := range all {
		if e.JobID != nil && *e.JobID == id {
			events = append(events, e)
			kinds = append(kinds, e.Kind)
		}
	}
	if !slices.Equal(kinds, want) {
		h.t.Fatalf("任务 #%d 的事件 %v，期望 %v", id, kinds, want)
	}
	return events
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

func TestQueuePausePersistsAndBlocksScheduleAcrossRestart(t *testing.T) {
	h := newHarness(t, 1)
	id := h.add(1)
	state, changed, err := h.m.PauseQueue("manual", "手动暂停", nil)
	if err != nil || !changed || !state.Paused || state.Kind != "manual" || state.Since == 0 {
		t.Fatalf("暂停队列 = %+v, changed=%t, err=%v", state, changed, err)
	}
	h.tick()
	h.expect(id, store.StatusQueued)
	if got := h.ex.startedIDs(); len(got) != 0 {
		t.Fatalf("暂停期间启动了任务: %v", got)
	}
	if _, changed, err := h.m.PauseQueue("manual", "重复暂停", nil); err != nil || changed {
		t.Fatalf("重复暂停应该幂等: changed=%t, err=%v", changed, err)
	}

	h.m = h.manager()
	state, err = h.m.QueueState()
	if err != nil || !state.Paused || state.Reason != "手动暂停" {
		t.Fatalf("重建 Manager 后队列暂停状态丢失: %+v, %v", state, err)
	}
	h.tick()
	h.expect(id, store.StatusQueued)
	state, changed, err = h.m.ResumeQueue()
	if err != nil || !changed || state.Paused {
		t.Fatalf("恢复队列 = %+v, changed=%t, err=%v", state, changed, err)
	}
	h.tick()
	h.expect(id, store.StatusRunning, 0)
	if got := h.ex.startedIDs(); !slices.Equal(got, []int64{id}) {
		t.Fatalf("恢复后启动任务 = %v", got)
	}

	events, err := h.st.EventsAfter(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var queueEvents []string
	for _, e := range events {
		if e.JobID == nil {
			queueEvents = append(queueEvents, e.Kind)
		}
	}
	if !slices.Equal(queueEvents, []string{store.EventQueuePaused, store.EventQueueResumed}) {
		t.Fatalf("队列事件 = %v", queueEvents)
	}
}

func TestQueuePauseKeepsMonitoringRunningJobs(t *testing.T) {
	h := newHarness(t, 1)
	a, b := h.add(1), h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 0)
	h.expect(b, store.StatusQueued)
	if _, _, err := h.m.PauseQueue("manual", "手动暂停", nil); err != nil {
		t.Fatal(err)
	}
	h.tick()
	h.expect(a, store.StatusRunning, 0)
	if len(h.ex.terms) > 0 || len(h.ex.kills) > 0 {
		t.Fatal("暂停队列不应终止运行中的任务")
	}
	h.ex.finish(a, 0)
	h.tick()
	h.expect(a, store.StatusSucceeded)
	h.expect(b, store.StatusQueued)
	if _, _, err := h.m.ResumeQueue(); err != nil {
		t.Fatal(err)
	}
	h.tick()
	h.expect(b, store.StatusRunning, 0)
}

func TestQueueWriteFailureDoesNotChangeState(t *testing.T) {
	h := newHarness(t, 1)
	id := h.add(1)
	h.st.setQueueFailure(true)
	if _, _, err := h.m.PauseQueue("manual", "手动暂停", nil); err == nil {
		t.Fatal("暂停写库失败应该返回错误")
	}
	if state, err := h.m.QueueState(); err != nil || state.Paused {
		t.Fatalf("写库失败不应改变暂停状态: %+v, %v", state, err)
	}
	h.st.setQueueFailure(false)
	if _, _, err := h.m.PauseQueue("manual", "手动暂停", nil); err != nil {
		t.Fatal(err)
	}
	h.st.setQueueFailure(true)
	if _, _, err := h.m.ResumeQueue(); err == nil {
		t.Fatal("恢复写库失败应该返回错误")
	}
	h.tick()
	h.expect(id, store.StatusQueued)
	if state, err := h.m.QueueState(); err != nil || !state.Paused {
		t.Fatalf("恢复失败后应保持暂停: %+v, %v", state, err)
	}
}

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
	h.events(a, store.EventJobFinished)
	h.events(b, store.EventJobFinished)
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
	if h.job(a).Recipe != nil {
		t.Fatal("启动失败的任务不应留下待采集配方")
	}
	h.events(a, store.EventJobFinished)
	if j := h.job(a); j.FailReason != "启动失败: fork 失败" || j.FailKind != "start" {
		t.Fatalf("启动失败原因或分类不正确: %+v", j)
	}
}

func TestFailureClassificationAndTraceback(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	h.tick()
	logText := "before failure\n[rank0]: Traceback (most recent call last):\n[rank0]: torch.OutOfMemoryError: CUDA out of memory\n"
	if err := os.WriteFile(h.ex.LogPath(a), []byte(logText), 0o600); err != nil {
		t.Fatal(err)
	}
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs = []gpu.Proc{{PID: 4242, User: "alice"}} })
	h.m.step(false, false)
	if !h.job(a).ExtSeen {
		t.Fatal("任务运行期间卡上出现了外部进程，应持久化 ext_seen")
	}
	h.ex.finish(a, 1)
	h.tick()
	j := h.job(a)
	wantOffset := int64(strings.Index(logText, "Traceback"))
	if j.FailKind != "cuda_oom" || j.FailReason != "CUDA 显存不足，运行期间卡上出现过外部进程" ||
		j.TracebackOffset == nil || *j.TracebackOffset != wantOffset {
		t.Fatalf("失败归类或 traceback 偏移不正确: %+v，期望偏移 %d", j, wantOffset)
	}
	events := h.events(a, store.EventJobStarted, store.EventJobFinished)
	var finished struct {
		Kind   string `json:"fail_kind"`
		Offset *int64 `json:"traceback_offset"`
	}
	if err := json.Unmarshal(events[1].Data, &finished); err != nil || finished.Kind != "cuda_oom" || finished.Offset == nil || *finished.Offset != wantOffset {
		t.Fatalf("结束事件没有记录失败分类和 traceback 偏移: %+v err=%v", finished, err)
	}
}

func TestHostOOMClassification(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(0)
	h.tick()
	if h.job(a).OOMBase != 0 {
		t.Fatalf("启动基线 OOM 计数 = %d，期望 0", h.job(a).OOMBase)
	}
	h.ex.mu.Lock()
	h.ex.oomCount = 1
	h.ex.mu.Unlock()
	h.ex.crash(a)
	h.tick()
	if j := h.job(a); j.FailKind != "host_oom" || j.FailReason != "主机内存不足，被系统杀掉" {
		t.Fatalf("主机 OOM 归类错误: %+v", j)
	}
}

// 读得到 oom_kill 计数且没有增加时，137 是别人发的 SIGKILL，不能推测成内存不足。
func TestExit137WithoutOOMEvent(t *testing.T) {
	h := newHarness(t, 1)
	a, b := h.add(0), h.add(0)
	h.tick()
	h.ex.finish(a, 137)
	h.tick()
	if j := h.job(a); j.FailKind != "signal" || j.FailReason != "被信号终止（SIGKILL）" {
		t.Fatalf("计数未增加时的 137 归类错误: %+v", j)
	}
	h.ex.mu.Lock()
	h.ex.oomKnown = false
	h.ex.mu.Unlock()
	h.ex.finish(b, 137)
	h.tick()
	if j := h.job(b); j.FailKind != "host_oom" || j.FailReason != "退出码 137，可能是主机内存不足" {
		t.Fatalf("读不到计数时应按 137 推测: %+v", j)
	}
}

func TestLoadEnvFailure(t *testing.T) {
	h := newHarness(t, 1)
	h.ex.loadEnv = func(context.Context) error { return errors.New("加载 .bashrc 超时（10 秒）") }
	a := h.add(0)
	h.tick()
	h.expect(a, store.StatusFailed)
	if h.job(a).Recipe != nil {
		t.Fatal("环境加载失败的任务不应留下待采集配方")
	}
	h.events(a, store.EventJobFinished)
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
	h.events(a, store.EventJobStarted)
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
	h.events(a)                      // 尚未启动进程，不记 job_started
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
	h.m.captures.Wait()
	h.expect(a, store.StatusCancelled)
	h.expect(b, store.StatusRunning)
	h.events(a, store.EventJobFinished)
	h.events(b, store.EventJobStarted)
	if ids := h.ex.startedIDs(); slices.Contains(ids, a) {
		t.Fatalf("启动前被取消的任务不应该启动: %v", ids)
	}
	if len(h.ex.terms) != 0 {
		t.Fatalf("还没有进程，不应该发信号: %v", h.ex.terms)
	}
}

func TestRecipeCaptureRunsInBackground(t *testing.T) {
	h := newHarness(t, 0)
	a := h.add(0)
	started, release := make(chan struct{}), make(chan struct{})
	h.ex.packageFn = func(ctx context.Context, _ runner.Spec, _ []string) ([]byte, error) {
		close(started)
		select {
		case <-release:
			return []byte("numpy==1.2\n"), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	h.m.step(false, true)
	h.m.launches.Wait()
	<-started
	j := h.job(a)
	if j.Status != store.StatusRunning || j.Recipe == nil || !j.Recipe.Pending || j.Recipe.EnvHash != "" {
		t.Fatalf("配方采集不应阻塞任务启动，且应保留待记录状态: %+v", j)
	}
	h.ex.mu.Lock()
	startedSpec := h.ex.started[0]
	h.ex.mu.Unlock()
	if j.OutputDir == "" || startedSpec.OutputDir != j.OutputDir || j.Recipe.Machine == nil || j.Recipe.Machine.Hostname == "" {
		t.Fatalf("输出目录或机器配方没有传给任务: job=%+v spec=%+v", j, startedSpec)
	}
	close(release)
	h.m.captures.Wait()
	if j := h.job(a); j.Recipe == nil || j.Recipe.EnvHash != "fake-package-hash" || j.Recipe.EnvError != "" {
		t.Fatalf("环境包配方没有保存: %+v", j)
	}
}

func TestRecipeCaptureFailureDoesNotFailJob(t *testing.T) {
	h := newHarness(t, 0)
	h.ex.packageErr = errors.New("conda list 超时")
	a := h.add(0)
	h.tick()
	if j := h.job(a); j.Status != store.StatusRunning || j.Recipe == nil || !strings.Contains(j.Recipe.EnvError, "conda list 超时") {
		t.Fatalf("环境记录失败不应影响任务，但要保存原因: %+v", j)
	}
}

func TestCancelDuringLaunchSaveFailureStillPreventsStart(t *testing.T) {
	h := newHarness(t, 1)
	release := make(chan struct{})
	h.ex.loadEnv = func(context.Context) error { <-release; return nil }
	a := h.add(1)
	h.m.step(false, true)
	h.st.setFail(func(j *store.Job) bool { return j.CancelRequested })
	if _, err := h.m.Cancel(a); err == nil {
		t.Fatal("持久化取消请求失败应返回错误")
	}
	close(release)
	h.m.launches.Wait()
	if len(h.ex.startedIDs()) != 0 {
		t.Fatal("取消状态写库失败时仍不应该启动任务")
	}
	h.expect(a, store.StatusRunning) // 数据库写失败；内存里的 cancelled 会重试
	h.events(a)
	h.st.setFail(nil)
	h.tick()
	h.expect(a, store.StatusCancelled)
	h.events(a, store.EventJobFinished)
}

// 占位时写库失败：不启动，下一轮再试
func TestReserveSaveFails(t *testing.T) {
	h := newHarness(t, 1)
	h.st.setFail(func(j *store.Job) bool { return j.Status == store.StatusRunning && j.PID == 0 })
	a := h.add(1)
	h.tick()
	h.expect(a, store.StatusQueued)
	h.events(a)
	if len(h.ex.startedIDs()) != 0 {
		t.Fatal("占位没写进库就不应该启动")
	}
	h.st.setFail(nil)
	h.tick()
	h.expect(a, store.StatusRunning)
	h.events(a, store.EventJobStarted)
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
	h.events(a, store.EventJobStarted, store.EventJobFinished)
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
	h.events(a, store.EventJobStarted)
	h.st.setFail(nil)
	h.tick()
	h.expect(a, store.StatusFailed)
	h.events(a, store.EventJobStarted, store.EventJobFinished)
	h.tick()
	h.events(a, store.EventJobStarted, store.EventJobFinished)
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
	h.events(a, store.EventJobRequeued)
}

// 库里的任务在准备期间被别的进程改回排队（比如同一数据目录的第二个 danlu 做了接管），
// 不会再占位启动一次，原来的启动照常完成
func TestRequeuedInDBWhileStartingDoesNotLaunchTwice(t *testing.T) {
	h := newHarness(t, 1)
	release := make(chan struct{})
	h.ex.loadEnv = func(context.Context) error { <-release; return nil }
	a := h.add(1)
	h.m.step(false, true)
	h.expect(a, store.StatusRunning)
	j := h.job(a)
	j.Status, j.GPUsAssigned, j.MasterPort, j.StartedAt = store.StatusQueued, nil, 0, nil
	if err := h.st.Store.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	h.m.step(false, true)
	close(release)
	h.m.launches.Wait()
	h.m.captures.Wait()
	h.m.manifests.Wait()
	if ids := h.ex.startedIDs(); !slices.Equal(ids, []int64{a}) {
		t.Fatalf("任务应该只启动一次: %v", ids)
	}
	h.expect(a, store.StatusRunning, 0)
}

// 一个输出目录卡住（比如网络存储），只让它自己的任务启动失败，别的任务照常启动
func TestStuckManifestFailsOnlyItsJob(t *testing.T) {
	h := newHarness(t, 0)
	h.m.opt.ManifestTimeout = 100 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	a, b := h.add(0), h.add(0)
	h.ex.manifestFn = func(dir string) error {
		if strings.HasPrefix(filepath.Base(dir), fmt.Sprintf("%d-", a)) {
			<-release
		}
		return nil
	}
	h.m.step(false, true)
	within(t, 2*time.Second, "启动", h.m.launches.Wait)
	h.m.captures.Wait()
	h.expect(b, store.StatusRunning)
	if j := h.job(a); j.Status != store.StatusFailed || j.FailKind != "start" || !strings.Contains(j.FailReason, "没有响应") {
		t.Fatalf("卡住的输出目录应该让任务启动失败: %s %s %s", j.Status, j.FailKind, j.FailReason)
	}
	if ids := h.ex.startedIDs(); !slices.Equal(ids, []int64{b}) {
		t.Fatalf("只应启动另一个任务: %v", ids)
	}
}

// 停止时清单写入卡住，danlu 最多等 ManifestTimeout 就退出
func TestShutdownDoesNotWaitForStuckManifest(t *testing.T) {
	h := newHarness(t, 0)
	h.m.opt.ManifestTimeout = 100 * time.Millisecond
	release := make(chan struct{})
	defer close(release)
	h.ex.manifestFn = func(string) error { <-release; return nil }
	a := h.add(0)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.m.Run(ctx); close(done) }()
	h.m.Wake()
	for i := 0; h.job(a).Status != store.StatusFailed; i++ {
		if i == 200 {
			t.Fatal("任务没有因清单超时失败")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	within(t, 2*time.Second, "Run", func() { <-done })
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
	h.events(a, store.EventJobFinished)
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
	h.events(a, store.EventJobStarted, store.EventJobFinished)
}

// ---------- 改名 ----------

func TestRenameQueuedStartsWithNewName(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	if _, err := h.m.Rename(a, "  vit-lr3e-4  "); err != nil {
		t.Fatal(err)
	}
	h.tick()
	j := h.job(a)
	if j.Name != "vit-lr3e-4" || filepath.Base(j.OutputDir) != fmt.Sprintf("%d-vit-lr3e-4", a) {
		t.Fatalf("改名后启动：名字 %q，输出目录 %q", j.Name, j.OutputDir)
	}
	h.ex.mu.Lock()
	spec := h.ex.started[0]
	h.ex.mu.Unlock()
	if spec.Name != "vit-lr3e-4" {
		t.Fatalf("DANLU_JOB_NAME 用的是 %q", spec.Name)
	}
}

func TestRenameRunningKeepsOutputDir(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	h.tick()
	dir := h.job(a).OutputDir
	if _, err := h.m.Rename(a, "新名字"); err != nil {
		t.Fatal(err)
	}
	h.ex.finish(a, 0)
	h.tick()
	j := h.job(a)
	if j.Status != store.StatusSucceeded || j.Name != "新名字" || j.OutputDir != dir {
		t.Fatalf("运行中改名后收尾: %+v", j)
	}
	events := h.events(a, store.EventJobStarted, store.EventJobFinished)
	var finished struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(events[1].Data, &finished); err != nil || finished.Name != "新名字" {
		t.Fatalf("结束事件里的名字 %q，err=%v", finished.Name, err)
	}
}

// 写库失败待重试的副本盖过库里的记录，改名也要改到它，结束事件才是新名字
func TestRenameUpdatesPendingCopy(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	h.tick()
	h.st.setFail(func(j *store.Job) bool { return j.CancelRequested && j.Status == store.StatusRunning })
	if _, err := h.m.Cancel(a); err == nil {
		t.Fatal("取消请求写库失败应返回错误")
	}
	got, err := h.m.Rename(a, "改过的名字")
	if err != nil {
		t.Fatal(err)
	}
	if !got.CancelRequested || got.Name != "改过的名字" {
		t.Fatalf("应该返回内存里较新的副本: %+v", got)
	}
	h.ex.finish(a, 143)
	h.tick()
	h.expect(a, store.StatusCancelled)
	events := h.events(a, store.EventJobStarted, store.EventJobFinished)
	var finished struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(events[1].Data, &finished); err != nil || finished.Name != "改过的名字" {
		t.Fatalf("结束事件里的名字 %q，err=%v", finished.Name, err)
	}
}

func TestRenameValidation(t *testing.T) {
	h := newHarness(t, 0)
	a := h.add(0)
	for _, name := range []string{"", "   ", strings.Repeat("名", maxNameLen+1)} {
		if _, err := h.m.Rename(a, name); !IsValidation(err) {
			t.Fatalf("任务名 %q 应该返回校验错误: %v", name, err)
		}
	}
	if _, err := h.m.Rename(a, strings.Repeat("名", maxNameLen)); err != nil {
		t.Fatalf("%d 个字的任务名应该可以: %v", maxNameLen, err)
	}
	if _, err := h.m.Rename(a+100, "x"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的任务应该返回 ErrNotFound: %v", err)
	}
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
			h.events(ids[i], store.EventJobStarted)
			if j.PID == 0 || j.PIDStart != startOf(j.PID) {
				t.Errorf("%s: 应该记下 pid 和启动时间: %d %d", r.name, j.PID, j.PIDStart)
			}
		case store.StatusQueued:
			h.events(ids[i], store.EventJobRequeued)
			if j.StartedAt != nil || len(j.GPUsAssigned) != 0 || j.MasterPort != 0 {
				t.Errorf("%s: 放回队列时应该清空占位: %+v", r.name, j)
			}
		default:
			if r.pidFile {
				h.events(ids[i], store.EventJobStarted, store.EventJobFinished)
			} else {
				h.events(ids[i], store.EventJobFinished)
			}
		}
	}
	if len(h.ex.startedIDs()) != 0 {
		t.Fatal("Recover 不应该启动任何任务")
	}
}

// 启动事件没写入时任务就已经结束，重试要把两件事按顺序一起补齐。
func TestFinishWhileStartedEventPending(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	h.st.setFail(func(j *store.Job) bool { return j.PID > 0 })
	h.tick()
	h.events(a)
	h.ex.finish(a, 7)
	h.tick()
	h.events(a)
	if j := h.m.pending[a]; j == nil || j.Status != store.StatusFailed {
		t.Fatal("收尾结果应留在内存中重试")
	}
	h.st.setFail(nil)
	h.tick()
	h.expect(a, store.StatusFailed)
	events := h.events(a, store.EventJobStarted, store.EventJobFinished)
	var started struct {
		PID  int   `json:"pid"`
		GPUs []int `json:"gpus"`
	}
	var finished struct {
		Status   string `json:"status"`
		ExitCode int    `json:"exit_code"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(events[0].Data, &started); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(events[1].Data, &finished); err != nil {
		t.Fatal(err)
	}
	if started.PID != 1001 || !slices.Equal(started.GPUs, []int{0}) || finished.Status != store.StatusFailed ||
		finished.ExitCode != 7 || finished.Reason != "退出码 7" {
		t.Fatalf("事件内容不完整: %+v %+v", started, finished)
	}
	if !slices.Equal(h.ex.startedIDs(), []int64{a}) {
		t.Fatal("写库失败后不应再次启动")
	}
	h.tick()
	h.m = h.manager()
	h.m.Recover()
	h.events(a, store.EventJobStarted, store.EventJobFinished)
}

// 库里仍是 queued 时，内存中的失败或取消也必须阻止任务被调度。
func TestQueuedTransitionRetried(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%t", cancel), func(t *testing.T) {
			h := newHarness(t, 1)
			a := h.add(2)
			h.st.setFail(func(j *store.Job) bool { return j.Finished() })
			if cancel {
				if _, err := h.m.Cancel(a); err == nil {
					t.Fatal("写库失败应返回错误")
				}
			} else {
				h.tick()
			}
			h.expect(a, store.StatusQueued)
			h.events(a)
			h.gpus.set(func(f *fakeGPUs) { f.gpus = newGPUs(2).gpus })
			h.tick()
			if len(h.ex.startedIDs()) > 0 {
				t.Fatal("尚未落库的失败或取消不应被再次启动")
			}
			h.st.setFail(nil)
			h.tick()
			want := store.StatusFailed
			if cancel {
				want = store.StatusCancelled
			}
			h.expect(a, want)
			h.events(a, store.EventJobFinished)
		})
	}
}

func TestCleanupNotesSeparateFromFailure(t *testing.T) {
	for _, code := range []int{0, 3} {
		t.Run(fmt.Sprintf("exit=%d", code), func(t *testing.T) {
			h := newHarness(t, 1)
			a := h.add(0)
			h.tick()
			h.ex.leftovers = 2
			h.ex.finish(a, code)
			h.tick()
			j := h.job(a)
			if j.Notes != "已清理 2 个残留进程" || strings.Contains(j.FailReason, "清理") || (code == 0 && j.FailReason != "") {
				t.Fatalf("原因和备注没有分开: %+v", j)
			}
			h.events(a, store.EventJobStarted, store.EventJobFinished)
		})
	}
}
