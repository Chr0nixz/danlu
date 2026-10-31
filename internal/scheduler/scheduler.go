// Package scheduler 管理任务的整个生命周期：提交、排队分卡、启动、监控、取消、收尾。
//
// 所有修改任务运行状态的操作都经过 Manager 并持有同一把锁，避免和调度循环互相踩踏。
// 锁里只做毫秒级的事：做快照、加载 .bashrc 这些慢操作放在锁外（见 launch）。
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/logs"
	"danlu/internal/runner"
	"danlu/internal/snapshot"
	"danlu/internal/store"
)

// Executor 是调度器和操作系统之间的那一层：加载环境、启动、查存活、发信号、读退出码。
// 生产环境用 *runner.Runner，单测里换成假的。
type Executor interface {
	// LoadEnv 返回任务的基础环境，勾选了"Bash 环境"时要加载 ~/.bashrc，可能要好几秒
	LoadEnv(ctx context.Context, inheritBashrc bool) ([]string, error)
	// Start 写脚本并 fork/exec，毫秒级
	Start(s runner.Spec, env []string) (pid int, start uint64, err error)
	Alive(jobID int64, pid int, start uint64) bool
	// Adopt 找回启动途中 danlu 退出时留下的进程（读 pid 文件并核对 DANLU_JOB_ID）
	Adopt(jobID int64) (pid int, start uint64, ok bool)
	// Launched 表示包装脚本运行过（留下了 pid 文件），不管现在还在不在
	Launched(jobID int64) bool
	Terminate(jobID int64, pgid int)
	KillAll(jobID int64, pgid int) int
	ReadExitCode(jobID int64) (int, bool)
	LogPath(jobID int64) string
	BootID() string
}

// GPUSource 是 GPU 采样器的 Latest。数据过期或者查不到卡上的进程时返回错误，这时不分配 GPU。
type GPUSource interface {
	Latest() ([]gpu.GPU, time.Time, error)
}

// JobStore 是调度器用到的那部分存储。单测里包一层，模拟写库失败。
type JobStore interface {
	InsertJob(*store.Job) error
	SaveJob(*store.Job) error
	GetJob(int64) (*store.Job, error)
	JobsByStatus(string) ([]*store.Job, error)
	TouchWorkdir(string)
}

type Options struct {
	SnapshotDir     string
	Snapshot        snapshot.Options
	SnapshotTimeout time.Duration
	KillGrace       time.Duration
}

type Manager struct {
	mu       sync.Mutex
	st       JobStore
	gpus     GPUSource
	ex       Executor
	opt      Options
	bootID   string
	wake     chan struct{}
	cancelAt map[int64]time.Time
	// starting 是已经占位、还在锁外做准备的任务，值表示这期间有没有被取消
	starting map[int64]*launchState
	// pending 是内存里比库里新的任务：写库失败了，每一轮重试，库里的旧状态不再作数
	pending map[int64]*store.Job
	// ctx 在 Run 里设置；launch 用它派生快照和加载环境的超时，停止服务时立即放弃
	ctx      context.Context
	launches sync.WaitGroup
	// snap 做快照，单测里换掉，免得真的去打包目录
	snap func(ctx context.Context, j *store.Job) (*snapshot.Meta, error)
}

type launchState struct{ cancelled bool }

func New(st JobStore, gpus GPUSource, ex Executor, opt Options) *Manager {
	m := &Manager{st: st, gpus: gpus, ex: ex, opt: opt, bootID: ex.BootID(), wake: make(chan struct{}, 1),
		cancelAt: map[int64]time.Time{}, starting: map[int64]*launchState{}, pending: map[int64]*store.Job{},
		ctx: context.Background()}
	m.snap = func(ctx context.Context, j *store.Job) (*snapshot.Meta, error) {
		return snapshot.Take(ctx, j.Workdir, opt.SnapshotDir, j.ID, opt.Snapshot)
	}
	return m
}

// ValidationError 表示请求本身有问题（API 返回 400）。
type ValidationError struct{ Msg string }

func (e *ValidationError) Error() string { return e.Msg }

func invalid(format string, a ...any) error { return &ValidationError{Msg: fmt.Sprintf(format, a...)} }

func (m *Manager) Wake() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// ---------- 提交与取消 ----------

type SubmitReq struct {
	Name          string            `json:"name"`
	Workdir       string            `json:"workdir"`
	CondaEnv      string            `json:"conda_env"`
	Cmd           string            `json:"cmd"`
	Env           map[string]string `json:"env"`
	InheritBashrc bool              `json:"inherit_bashrc"`
	GPUs          int               `json:"gpus"`
	PinnedGPUs    []int             `json:"pinned_gpus"`
	RerunOf       *int64            `json:"rerun_of"`
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func (m *Manager) Submit(req SubmitReq) (*store.Job, error) {
	req.Workdir = strings.TrimSpace(req.Workdir)
	req.Cmd = strings.TrimSpace(req.Cmd)
	req.Name = strings.TrimSpace(req.Name)
	if req.Workdir == "" || !strings.HasPrefix(req.Workdir, "/") {
		return nil, invalid("工作目录必须是服务器上的绝对路径")
	}
	if fi, err := os.Stat(req.Workdir); err != nil || !fi.IsDir() {
		return nil, invalid("工作目录不存在: %s", req.Workdir)
	}
	if req.Cmd == "" {
		return nil, invalid("命令不能为空")
	}
	if req.Name == "" {
		req.Name = filepath.Base(req.Workdir) + "-" + time.Now().Format("0102-1504")
	}
	if len([]rune(req.Name)) > 100 {
		return nil, invalid("任务名太长")
	}
	for k := range req.Env {
		if !envKeyRe.MatchString(k) {
			return nil, invalid("环境变量名不合法: %q", k)
		}
		if runner.ReservedEnv(k) {
			return nil, invalid("环境变量 %s 由 danlu 管理，不能自定义", k)
		}
	}
	pinned := slices.Clone(req.PinnedGPUs)
	slices.Sort(pinned)
	pinned = slices.Compact(pinned)
	if len(pinned) > 0 {
		req.GPUs = len(pinned)
	}
	if req.GPUs < 0 || req.GPUs > 64 {
		return nil, invalid("GPU 数量不合法")
	}
	if gpus, _, err := m.gpus.Latest(); err == nil && len(gpus) > 0 {
		if req.GPUs > len(gpus) {
			return nil, invalid("需要 %d 张卡，但这台机器只有 %d 张", req.GPUs, len(gpus))
		}
		for _, p := range pinned {
			if !slices.ContainsFunc(gpus, func(g gpu.GPU) bool { return g.Index == p }) {
				return nil, invalid("GPU %d 不存在", p)
			}
		}
	}
	if req.RerunOf != nil {
		if _, err := m.st.GetJob(*req.RerunOf); err != nil {
			req.RerunOf = nil
		}
	}
	if req.Env == nil {
		req.Env = map[string]string{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	j := &store.Job{Name: req.Name, Workdir: req.Workdir, CondaEnv: strings.TrimSpace(req.CondaEnv), Cmd: req.Cmd,
		Env: req.Env, InheritBashrc: req.InheritBashrc, GPUsReq: req.GPUs, GPUsPinned: pinned,
		Status: store.StatusQueued, RerunOf: req.RerunOf}
	if err := m.st.InsertJob(j); err != nil {
		return nil, err
	}
	m.st.TouchWorkdir(req.Workdir)
	m.Wake()
	return j, nil
}

func (m *Manager) Cancel(id int64) (*store.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.st.GetJob(id)
	if err != nil {
		return nil, err
	}
	if p, ok := m.pending[id]; ok {
		j = p
	}
	switch {
	case j.Status == store.StatusQueued:
		now := time.Now().Unix()
		j.Status, j.EndedAt = store.StatusCancelled, &now
		if err := m.st.SaveJob(j); err != nil {
			return nil, err
		}
	case j.Status == store.StatusRunning && !j.CancelRequested:
		j.CancelRequested = true
		if err := m.st.SaveJob(j); err != nil {
			return nil, err
		}
		delete(m.pending, id)
		if ls, ok := m.starting[id]; ok {
			// 还在准备（快照、加载环境），launch 看到标记就不再启动
			ls.cancelled = true
		} else {
			m.ex.Terminate(j.ID, j.PID)
			m.cancelAt[j.ID] = time.Now()
		}
	case j.Status == store.StatusRunning:
	default:
		return nil, invalid("任务已经结束")
	}
	m.Wake()
	return j, nil
}

// ---------- 主循环 ----------

// Recover 在启动时调用：仍在运行的任务继续监控，已经结束的补做收尾，启动途中被打断的按 reconcile 处理。
func (m *Manager) Recover() {
	m.mu.Lock()
	defer m.mu.Unlock()
	running, err := m.st.JobsByStatus(store.StatusRunning)
	if err != nil {
		log.Printf("读取运行中任务失败: %v", err)
		return
	}
	for _, j := range running {
		switch {
		case j.PID == 0:
			m.reconcile(j)
		case m.ex.Alive(j.ID, j.PID, j.PIDStart):
			log.Printf("接管运行中的任务 #%d %s（pid %d）", j.ID, j.Name, j.PID)
			if j.CancelRequested {
				m.cancelAt[j.ID] = time.Now().Add(-m.opt.KillGrace) // 之前已经请求取消，直接强杀
			}
		default:
			log.Printf("任务 #%d 在 danlu 停止期间已结束，补做收尾", j.ID)
			m.finalize(j)
		}
	}
}

// Run 是调度循环。ctx 结束后，等正在启动的任务都落库才返回，调用方之后才能关数据库。
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.ctx = ctx
	m.mu.Unlock()
	defer m.launches.Wait()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.step(n%5 == 0, n%2 == 0)
		case <-m.wake:
			m.step(false, true)
		}
	}
}

func (m *Manager) step(scanTracker, sched bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.checkRunning(scanTracker)
	if sched {
		m.schedule()
	}
}

func (m *Manager) checkRunning(scanTracker bool) {
	running, err := m.st.JobsByStatus(store.StatusRunning)
	if err != nil {
		log.Printf("读取运行中任务失败: %v", err)
		return
	}
	for _, j := range running {
		if _, ok := m.starting[j.ID]; ok {
			continue // 还在 launch 里，pid 还没有
		}
		if p, ok := m.pending[j.ID]; ok {
			j = p
			m.save(j)
			if j.Status != store.StatusRunning {
				continue // 已经结束或者放回队列了，只是之前没写进库
			}
		}
		if j.PID == 0 {
			m.reconcile(j)
			continue
		}
		if !m.ex.Alive(j.ID, j.PID, j.PIDStart) {
			m.finalize(j)
			continue
		}
		if j.CancelRequested {
			at, ok := m.cancelAt[j.ID]
			if !ok {
				m.cancelAt[j.ID] = time.Now()
				m.ex.Terminate(j.ID, j.PID)
			} else if time.Since(at) > m.opt.KillGrace {
				m.ex.KillAll(j.ID, j.PID)
			}
		}
		if scanTracker && j.TrackerURL == "" {
			if u := logs.FindTracker(m.ex.LogPath(j.ID), 1<<20); u != "" {
				j.TrackerURL = u
				m.save(j)
			}
		}
	}
}

// reconcile 处理库里是 running、pid 还是 0、又不在 starting 里的任务：
// 占位以后 danlu 退出了，或者启动以后写库失败了。按实际情况认领、收尾或者放回队列，
// 绝不能再启动一次。
func (m *Manager) reconcile(j *store.Job) {
	if pid, start, ok := m.ex.Adopt(j.ID); ok {
		j.PID, j.PIDStart = pid, start
		m.save(j)
		log.Printf("认领启动途中的任务 #%d %s（pid %d）", j.ID, j.Name, pid)
		if j.CancelRequested {
			m.cancelAt[j.ID] = time.Now().Add(-m.opt.KillGrace)
		}
		return
	}
	if m.ex.Launched(j.ID) {
		m.finalize(j) // 跑过，已经结束了：按退出码收尾
		return
	}
	if j.CancelRequested {
		now := time.Now().Unix()
		j.Status, j.EndedAt = store.StatusCancelled, &now
		m.save(j)
		return
	}
	// 从没启动过：放回队列，下一轮重新分卡
	j.Status, j.StartedAt, j.GPUsAssigned, j.MasterPort, j.BootID = store.StatusQueued, nil, nil, 0, ""
	m.save(j)
	log.Printf("任务 #%d %s 启动途中被打断，放回队列", j.ID, j.Name)
}

func (m *Manager) finalize(j *store.Job) {
	now := time.Now().Unix()
	j.EndedAt = &now
	code, ok := m.ex.ReadExitCode(j.ID)
	if ok {
		j.ExitCode = &code
	}
	switch {
	case j.CancelRequested:
		j.Status = store.StatusCancelled
	case ok && code == 0:
		j.Status = store.StatusSucceeded
	case ok:
		j.Status, j.FailReason = store.StatusFailed, fmt.Sprintf("退出码 %d", code)
	case j.BootID != "" && m.bootID != "" && j.BootID != m.bootID:
		j.Status, j.FailReason = store.StatusFailed, "服务器重启，任务中断"
	default:
		j.Status, j.FailReason = store.StatusFailed, "进程被外部终止（没有留下退出码）"
	}
	// 只按 DANLU_JOB_ID 标记清理残留进程，不按进程组：
	// 包装脚本退出后进程组号可能被别的进程复用
	if n := m.ex.KillAll(j.ID, 0); n > 0 {
		note := fmt.Sprintf("已清理 %d 个残留进程", n)
		if j.FailReason == "" {
			j.FailReason = note
		} else {
			j.FailReason += "；" + note
		}
	}
	if j.TrackerURL == "" {
		j.TrackerURL = logs.FindTracker(m.ex.LogPath(j.ID), 1<<20)
	}
	delete(m.cancelAt, j.ID)
	m.save(j)
	log.Printf("任务 #%d %s 结束：%s %s", j.ID, j.Name, j.Status, j.FailReason)
}

// save 写库；失败时把任务记进 pending，下一轮 checkRunning 重试，这期间以内存里的为准。
func (m *Manager) save(j *store.Job) {
	if err := m.st.SaveJob(j); err != nil {
		log.Printf("保存任务 #%d 失败: %v", j.ID, err)
		m.pending[j.ID] = j
		return
	}
	delete(m.pending, j.ID)
}

func (m *Manager) failQueued(j *store.Job, reason string) {
	now := time.Now().Unix()
	j.Status, j.FailReason, j.EndedAt = store.StatusFailed, reason, &now
	if err := m.st.SaveJob(j); err != nil {
		log.Printf("保存任务 #%d 失败: %v", j.ID, err)
	}
}

// schedule 严格按提交顺序：队首放不下就停，后面的任务不插队。
func (m *Manager) schedule() {
	queued, err := m.st.JobsByStatus(store.StatusQueued)
	if err != nil || len(queued) == 0 {
		return
	}
	gpus, _, gpuErr := m.gpus.Latest()
	running, err := m.st.JobsByStatus(store.StatusRunning)
	if err != nil {
		return
	}
	busy := map[int]bool{}
	ports := map[int]bool{}
	assigned := map[int64][]int{}
	for _, r := range running {
		assigned[r.ID] = r.GPUsAssigned
		ports[r.MasterPort] = true
		for _, g := range r.GPUsAssigned {
			busy[g] = true
		}
	}
	var all []int
	for _, g := range gpus {
		all = append(all, g.Index)
		for _, p := range g.Procs {
			// 外部进程、已结束任务的残留进程、跑错卡的任务进程，都让这张卡不可分配
			if own, ok := assigned[p.JobID]; !ok || p.JobID == 0 || !slices.Contains(own, g.Index) {
				busy[g.Index] = true
			}
		}
	}
	gpuKnown := gpuErr == nil && len(gpus) > 0
	for _, j := range queued {
		needGPU := j.GPUsReq > 0 || len(j.GPUsPinned) > 0
		if needGPU && !gpuKnown {
			return // GPU 信息暂时拿不到（或者不可信），等下一轮
		}
		if needGPU {
			if j.GPUsReq > len(all) {
				m.failQueued(j, fmt.Sprintf("需要 %d 张卡，但这台机器只有 %d 张", j.GPUsReq, len(all)))
				continue
			}
			if p := missing(all, j.GPUsPinned); p >= 0 {
				m.failQueued(j, fmt.Sprintf("GPU %d 不存在", p))
				continue
			}
		}
		sel, ok := Allocate(all, busy, j.GPUsReq, j.GPUsPinned)
		if !ok {
			return
		}
		if !m.reserve(j, sel, ports) {
			return // 写库失败，这一轮停下，下一轮再试
		}
		for _, g := range sel {
			busy[g] = true
		}
	}
}

func missing(all, pinned []int) int {
	for _, p := range pinned {
		if !slices.Contains(all, p) {
			return p
		}
	}
	return -1
}

// Allocate 选卡：指定卡号时要求这些卡全部空闲；否则取编号最小的 n 张空闲卡。
func Allocate(all []int, busy map[int]bool, n int, pinned []int) ([]int, bool) {
	if len(pinned) > 0 {
		for _, p := range pinned {
			if busy[p] || !slices.Contains(all, p) {
				return nil, false
			}
		}
		return slices.Clone(pinned), true
	}
	sorted := slices.Clone(all)
	slices.Sort(sorted)
	sel := []int{}
	for _, g := range sorted {
		if len(sel) == n {
			break
		}
		if !busy[g] {
			sel = append(sel, g)
		}
	}
	return sel, len(sel) == n
}

// ---------- 启动：占位、准备、启动 ----------

// reserve 是启动的第一步（锁内）：选好的卡和端口先写进库，任务记为 running、pid 为 0，
// 再起 goroutine 做后面两步。先落库再启动：写库失败就不启动，同一个任务不会被启动两次。
func (m *Manager) reserve(j *store.Job, gpus []int, ports map[int]bool) bool {
	port := freePort(ports)
	now := time.Now().Unix()
	j.Status, j.PID, j.PIDStart, j.StartedAt, j.GPUsAssigned, j.MasterPort, j.BootID =
		store.StatusRunning, 0, 0, &now, gpus, port, m.bootID
	if err := m.st.SaveJob(j); err != nil {
		log.Printf("任务 #%d 占位失败，下一轮再试: %v", j.ID, err)
		return false
	}
	ports[port] = true
	m.starting[j.ID] = &launchState{}
	m.launches.Add(1)
	go m.launch(m.ctx, j)
	return true
}

// launch 是后两步。准备（锁外）：做快照、加载环境，都有超时，danlu 停止时立即放弃；
// 启动（锁内）：看一眼这期间有没有被取消、danlu 是不是在停止，都没有才 fork/exec 并落库。
func (m *Manager) launch(ctx context.Context, j *store.Job) {
	defer m.launches.Done()

	sctx, cancel := context.WithTimeout(ctx, m.opt.SnapshotTimeout)
	meta, snapErr := m.snap(sctx, j)
	cancel()
	env, envErr := m.ex.LoadEnv(ctx, j.InheritBashrc)

	m.mu.Lock()
	defer m.mu.Unlock()
	ls := m.starting[j.ID]
	delete(m.starting, j.ID)
	if snapErr != nil {
		j.SnapshotNote = "快照失败: " + snapErr.Error()
	} else {
		j.SnapshotOK = true
		if meta.Git != nil {
			j.GitCommit, j.GitBranch, j.GitDirty = meta.Git.Commit, meta.Git.Branch, meta.Git.Dirty
		}
		if meta.SkipCount > 0 {
			j.SnapshotNote = fmt.Sprintf("有 %d 个文件没有保存（超出大小限制或不是普通文件）", meta.SkipCount)
		}
	}
	now := time.Now().Unix()
	switch {
	case ls.cancelled:
		j.Status, j.CancelRequested, j.EndedAt = store.StatusCancelled, true, &now
		m.save(j)
		log.Printf("任务 #%d %s 在启动前被取消", j.ID, j.Name)
		return
	case ctx.Err() != nil:
		j.Status, j.StartedAt, j.GPUsAssigned, j.MasterPort, j.BootID = store.StatusQueued, nil, nil, 0, ""
		j.SnapshotOK, j.SnapshotNote, j.GitCommit, j.GitBranch, j.GitDirty = false, "", "", "", false
		m.save(j)
		log.Printf("danlu 正在停止，任务 #%d %s 放回队列", j.ID, j.Name)
		return
	case envErr != nil:
		j.Status, j.FailReason, j.EndedAt = store.StatusFailed, "启动失败: "+envErr.Error(), &now
		m.save(j)
		log.Printf("任务 #%d 启动失败: %v", j.ID, envErr)
		return
	}
	pid, startTime, err := m.ex.Start(runner.Spec{JobID: j.ID, Name: j.Name, Workdir: j.Workdir,
		CondaEnv: j.CondaEnv, Cmd: j.Cmd, Env: j.Env, GPUs: j.GPUsAssigned, Port: j.MasterPort}, env)
	if err != nil {
		j.Status, j.FailReason, j.EndedAt = store.StatusFailed, "启动失败: "+err.Error(), &now
		m.save(j)
		log.Printf("任务 #%d 启动失败: %v", j.ID, err)
		return
	}
	j.PID, j.PIDStart = pid, startTime
	// 写库失败时 save 把任务记进 pending：下一轮用内存里的 pid 继续监控，不会再启动一次
	m.save(j)
	log.Printf("任务 #%d %s 已启动：GPU %v，pid %d，端口 %d", j.ID, j.Name, j.GPUsAssigned, pid, j.MasterPort)
}

func freePort(used map[int]bool) int {
	for i := 0; i < 20; i++ {
		l, err := net.Listen("tcp", ":0")
		if err != nil {
			continue
		}
		p := l.Addr().(*net.TCPAddr).Port
		l.Close()
		if !used[p] {
			return p
		}
	}
	return 29500
}

func IsValidation(err error) bool {
	var v *ValidationError
	return errors.As(err, &v)
}
