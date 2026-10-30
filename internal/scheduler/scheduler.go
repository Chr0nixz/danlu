// Package scheduler 管理任务的整个生命周期：提交、排队分卡、启动、监控、取消、收尾。
//
// 所有修改任务运行状态的操作都经过 Manager 并持有同一把锁，避免和调度循环互相踩踏。
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
	"danlu/internal/procfs"
	"danlu/internal/runner"
	"danlu/internal/snapshot"
	"danlu/internal/store"
)

type Options struct {
	SnapshotDir     string
	Snapshot        snapshot.Options
	SnapshotTimeout time.Duration
	KillGrace       time.Duration
}

type Manager struct {
	mu       sync.Mutex
	st       *store.Store
	gpus     *gpu.Monitor
	run      *runner.Runner
	opt      Options
	wake     chan struct{}
	cancelAt map[int64]time.Time
}

func New(st *store.Store, mon *gpu.Monitor, run *runner.Runner, opt Options) *Manager {
	return &Manager{st: st, gpus: mon, run: run, opt: opt, wake: make(chan struct{}, 1), cancelAt: map[int64]time.Time{}}
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
	Name       string            `json:"name"`
	Workdir    string            `json:"workdir"`
	CondaEnv   string            `json:"conda_env"`
	Cmd        string            `json:"cmd"`
	Env        map[string]string `json:"env"`
	GPUs       int               `json:"gpus"`
	PinnedGPUs []int             `json:"pinned_gpus"`
	RerunOf    *int64            `json:"rerun_of"`
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
		Env: req.Env, GPUsReq: req.GPUs, GPUsPinned: pinned, Status: store.StatusQueued, RerunOf: req.RerunOf}
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
	switch j.Status {
	case store.StatusQueued:
		now := time.Now().Unix()
		j.Status, j.EndedAt = store.StatusCancelled, &now
		if err := m.st.SaveJob(j); err != nil {
			return nil, err
		}
	case store.StatusRunning:
		if !j.CancelRequested {
			j.CancelRequested = true
			if err := m.st.SaveJob(j); err != nil {
				return nil, err
			}
			runner.Terminate(j.ID, j.PID)
			m.cancelAt[j.ID] = time.Now()
		}
	default:
		return nil, invalid("任务已经结束")
	}
	m.Wake()
	return j, nil
}

// ---------- 主循环 ----------

// Recover 在启动时调用：仍在运行的任务继续监控，已经结束的补做收尾。
func (m *Manager) Recover() {
	m.mu.Lock()
	defer m.mu.Unlock()
	running, err := m.st.JobsByStatus(store.StatusRunning)
	if err != nil {
		log.Printf("读取运行中任务失败: %v", err)
		return
	}
	for _, j := range running {
		if procfs.Alive(j.PID, j.PIDStart) {
			log.Printf("接管运行中的任务 #%d %s（pid %d）", j.ID, j.Name, j.PID)
			if j.CancelRequested {
				m.cancelAt[j.ID] = time.Now().Add(-m.opt.KillGrace) // 之前已经请求取消，直接强杀
			}
			continue
		}
		log.Printf("任务 #%d 在 danlu 停止期间已结束，补做收尾", j.ID)
		m.finalize(j)
	}
}

func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for n := 1; ; n++ {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.mu.Lock()
			m.checkRunning(n%5 == 0)
			if n%2 == 0 {
				m.schedule()
			}
			m.mu.Unlock()
		case <-m.wake:
			m.mu.Lock()
			m.checkRunning(false)
			m.schedule()
			m.mu.Unlock()
		}
	}
}

func (m *Manager) checkRunning(scanTracker bool) {
	running, err := m.st.JobsByStatus(store.StatusRunning)
	if err != nil {
		log.Printf("读取运行中任务失败: %v", err)
		return
	}
	for _, j := range running {
		if !procfs.Alive(j.PID, j.PIDStart) {
			m.finalize(j)
			continue
		}
		if j.CancelRequested {
			at, ok := m.cancelAt[j.ID]
			if !ok {
				m.cancelAt[j.ID] = time.Now()
				runner.Terminate(j.ID, j.PID)
			} else if time.Since(at) > m.opt.KillGrace {
				runner.KillAll(j.ID, j.PID)
			}
		}
		if scanTracker && j.TrackerURL == "" {
			if u := logs.FindTracker(m.run.LogPath(j.ID), 1<<20); u != "" {
				j.TrackerURL = u
				m.save(j)
			}
		}
	}
}

func (m *Manager) finalize(j *store.Job) {
	now := time.Now().Unix()
	j.EndedAt = &now
	code, ok := m.run.ReadExitCode(j.ID)
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
	default:
		j.Status, j.FailReason = store.StatusFailed, "进程被外部终止（没有留下退出码）"
	}
	// 只按 DANLU_JOB_ID 标记清理残留进程，不按进程组：
	// 包装脚本退出后进程组号可能被别的进程复用
	if n := runner.KillAll(j.ID, 0); n > 0 {
		note := fmt.Sprintf("已清理 %d 个残留进程", n)
		if j.FailReason == "" {
			j.FailReason = note
		} else {
			j.FailReason += "；" + note
		}
	}
	if j.TrackerURL == "" {
		j.TrackerURL = logs.FindTracker(m.run.LogPath(j.ID), 1<<20)
	}
	delete(m.cancelAt, j.ID)
	m.save(j)
	log.Printf("任务 #%d %s 结束：%s %s", j.ID, j.Name, j.Status, j.FailReason)
}

func (m *Manager) save(j *store.Job) {
	if err := m.st.SaveJob(j); err != nil {
		log.Printf("保存任务 #%d 失败: %v", j.ID, err)
	}
}

func (m *Manager) failQueued(j *store.Job, reason string) {
	now := time.Now().Unix()
	j.Status, j.FailReason, j.EndedAt = store.StatusFailed, reason, &now
	m.save(j)
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
			return // GPU 信息暂时拿不到，等下一轮
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
		for _, g := range sel {
			busy[g] = true
		}
		m.start(j, sel, ports)
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

func (m *Manager) start(j *store.Job, gpus []int, ports map[int]bool) {
	port := freePort(ports)
	ports[port] = true

	ctx, cancel := context.WithTimeout(context.Background(), m.opt.SnapshotTimeout)
	meta, err := snapshot.Take(ctx, j.Workdir, m.opt.SnapshotDir, j.ID, m.opt.Snapshot)
	cancel()
	if err != nil {
		j.SnapshotNote = "快照失败: " + err.Error()
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
	j.StartedAt = &now
	pid, startTime, err := m.run.Start(runner.Spec{JobID: j.ID, Name: j.Name, Workdir: j.Workdir,
		CondaEnv: j.CondaEnv, Cmd: j.Cmd, Env: j.Env, GPUs: gpus, Port: port})
	if err != nil {
		j.Status, j.FailReason, j.EndedAt = store.StatusFailed, "启动失败: "+err.Error(), &now
		m.save(j)
		log.Printf("任务 #%d 启动失败: %v", j.ID, err)
		return
	}
	j.Status, j.PID, j.PIDStart, j.GPUsAssigned, j.MasterPort = store.StatusRunning, pid, startTime, gpus, port
	m.save(j)
	log.Printf("任务 #%d %s 已启动：GPU %v，pid %d，端口 %d", j.ID, j.Name, gpus, pid, port)
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
