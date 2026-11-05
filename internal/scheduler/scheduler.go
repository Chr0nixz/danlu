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
	"sync/atomic"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/logs"
	"danlu/internal/runner"
	"danlu/internal/snapshot"
	"danlu/internal/store"
	"danlu/internal/system"
)

// Executor 是调度器和操作系统之间的那一层：加载环境、启动、查存活、发信号、读退出码。
// 生产环境用 *runner.Runner，单测里换成假的。
type Executor interface {
	// LoadEnv 返回任务的基础环境，勾选了"Bash 环境"时要加载 ~/.bashrc，可能要好几秒
	LoadEnv(ctx context.Context, inheritBashrc bool) ([]string, error)
	// Start 写脚本并 fork/exec，毫秒级
	Start(s runner.Spec, env []string) (pid int, start uint64, err error)
	Alive(jobID int64, pid int, start uint64) bool
	// Adopt 找回启动途中 danlu 退出时留下的进程（读 pid 文件并核对 DANLU_RUN_DIR）
	Adopt(jobID int64) (pid int, start uint64, ok bool)
	// Launched 返回包装脚本留下的 pid，不要求进程仍存活；文件损坏时 pid 为 0、ok 为 true
	Launched(jobID int64) (pid int, ok bool)
	Terminate(jobID int64, pgid int)
	KillAll(jobID int64, pgid int) int
	ReadExitCode(jobID int64) (int, bool)
	OOMKills() (int64, bool)
	CapturePackages(context.Context, runner.Spec, []string) ([]byte, error)
	StorePackages([]byte) (string, error)
	WriteManifest(outputDir string, data []byte) error
	LogPath(jobID int64) string
	BootID() string
}

// GPUSource 是 GPU 采样器的 Latest。数据过期或者查不到卡上的进程时返回错误，这时不分配 GPU。
type GPUSource interface {
	Latest() ([]gpu.GPU, time.Time, error)
}

type DiskSource interface {
	Latest() ([]system.Disk, time.Time, error)
}

// JobStore 是调度器用到的那部分存储。单测里包一层，模拟写库失败。
type JobStore interface {
	InsertJob(*store.Job) error
	SaveJob(*store.Job) error
	RenameJob(int64, string) error
	Transition(*store.Job, ...*store.Event) error
	TransitionWithSetting(*store.Job, string, string, ...*store.Event) error
	GetSetting(string) (string, error)
	SetSettingWithEvent(string, string, *store.Event) error
	AppendEvents(...*store.Event) error
	LatestAlerts() (map[string]store.Event, error)
	GetJob(int64) (*store.Job, error)
	JobsByStatus(string) ([]*store.Job, error)
	JobsWithPendingRecipe() ([]*store.Job, error)
	TouchWorkdir(string)
}

type Options struct {
	SnapshotDir     string
	RunRoot         string
	OutputRoot      string
	Snapshot        snapshot.Options
	SnapshotTimeout time.Duration
	KillGrace       time.Duration
	FailFast        time.Duration
	Disks           DiskSource
	DiskMinBytes    uint64
	// ManifestTimeout 是写 danlu.json 最多等多久：启动前写不完按失败处理，停止时写不完不再等。为 0 时用 10 秒
	ManifestTimeout time.Duration
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
	// 和 pending 一起重试，保留发生顺序和去重键
	pendingEvents map[int64][]*store.Event
	// pendingQueue 和触发它的任务一起重试；落库前也必须阻止新任务启动。
	pendingQueue   *QueueState
	diskPause      *QueueState
	diskPauseEvent *store.Event
	diskError      string
	alerts         map[string]store.Event
	alertsReady    bool
	alertError     string
	gpuErrorSince  time.Time
	waitReasons    atomic.Pointer[map[int64]string]
	// ctx 在 Run 里设置；launch 用它派生快照和加载环境的超时，停止服务时立即放弃
	ctx       context.Context
	launches  sync.WaitGroup
	captures  sync.WaitGroup
	manifests sync.WaitGroup
	// snap 做快照，单测里换掉，免得真的去打包目录
	snap func(ctx context.Context, j *store.Job) (*snapshot.Meta, error)
}

type launchState struct{ cancelled bool }

func New(st JobStore, gpus GPUSource, ex Executor, opt Options) *Manager {
	if opt.ManifestTimeout <= 0 {
		opt.ManifestTimeout = 10 * time.Second
	}
	m := &Manager{st: st, gpus: gpus, ex: ex, opt: opt, bootID: ex.BootID(), wake: make(chan struct{}, 1),
		cancelAt: map[int64]time.Time{}, starting: map[int64]*launchState{}, pending: map[int64]*store.Job{},
		pendingEvents: map[int64][]*store.Event{},
		ctx:           context.Background()}
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
	CodeFrom      *int64            `json:"code_from"`
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const maxNameLen = 100

func (m *Manager) Submit(req SubmitReq) (*store.Job, error) {
	req.Workdir = strings.TrimSpace(req.Workdir)
	req.Cmd = strings.TrimSpace(req.Cmd)
	req.Name = strings.TrimSpace(req.Name)
	if req.Workdir == "" || !strings.HasPrefix(req.Workdir, "/") {
		return nil, invalid("工作目录必须是服务器上的绝对路径")
	}
	if req.CodeFrom != nil {
		source, err := m.st.GetJob(*req.CodeFrom)
		if errors.Is(err, store.ErrNotFound) {
			return nil, invalid("源任务 #%d 不存在", *req.CodeFrom)
		}
		if err != nil {
			return nil, err
		}
		if filepath.Clean(req.Workdir) != filepath.Clean(source.Workdir) {
			return nil, invalid("使用快照重跑时，工作目录必须与源任务一致")
		}
		req.Workdir = source.Workdir
	} else if fi, err := os.Stat(req.Workdir); err != nil || !fi.IsDir() {
		return nil, invalid("工作目录不存在: %s", req.Workdir)
	}
	if req.Cmd == "" {
		return nil, invalid("命令不能为空")
	}
	if req.Name == "" {
		// 先截短目录名再拼时间，目录名很长时也不会超过任务名长度限制
		suffix := "-" + time.Now().Format("0102-1504")
		base := []rune(filepath.Base(req.Workdir))
		if limit := maxNameLen - len([]rune(suffix)); len(base) > limit {
			base = base[:limit]
		}
		req.Name = string(base) + suffix
	}
	if len([]rune(req.Name)) > maxNameLen {
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
		Status: store.StatusQueued, RerunOf: req.RerunOf, CodeFrom: req.CodeFrom}
	if err := m.st.InsertJob(j); err != nil {
		if errors.Is(err, store.ErrSnapshotUnavailable) {
			return nil, invalid("源任务 #%d 的代码快照不可用（尚未生成、正在清理或已清理）", *req.CodeFrom)
		}
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
	defer m.Wake()
	switch {
	case j.Status == store.StatusQueued:
		if err := m.transition(j, store.StatusCancelled, "用户取消"); err != nil {
			return nil, err
		}
	case j.Status == store.StatusRunning && !j.CancelRequested:
		j.CancelRequested = true
		if ls, ok := m.starting[id]; ok {
			// 内存状态立即生效，即使持久化失败也不能继续启动。
			ls.cancelled = true
		}
		if err := m.save(j); err != nil {
			return nil, err
		}
		if _, preparing := m.starting[id]; !preparing {
			m.ex.Terminate(j.ID, j.PID)
			m.cancelAt[j.ID] = time.Now()
		}
	case j.Status == store.StatusRunning:
	default:
		return nil, invalid("任务已经结束")
	}
	return j, nil
}

// Rename 改任务名，任何状态都能改。只改记录：已经占位的任务，输出目录和 DANLU_JOB_NAME 仍是启动时的名字。
func (m *Manager) Rename(id int64, name string) (*store.Job, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, invalid("任务名不能为空")
	}
	if len([]rune(name)) > maxNameLen {
		return nil, invalid("任务名太长")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.st.RenameJob(id, name); err != nil {
		return nil, err
	}
	// 写库失败待重试的副本盖过库里的记录，也要改掉，否则之后的输出目录和结束通知还用旧名字
	if p, ok := m.pending[id]; ok {
		p.Name = name
		cp := *p
		return &cp, nil
	}
	return m.st.GetJob(id)
}

// ---------- 主循环 ----------

// Recover 在启动时调用：仍在运行的任务继续监控，已经结束的补做收尾，启动途中被打断的按 reconcile 处理。
func (m *Manager) Recover() {
	m.mu.Lock()
	defer m.mu.Unlock()
	// 上次被杀时已结束的任务也可能还在采集包；没有采集者了，不能永久挡住清理。
	if jobs, err := m.st.JobsWithPendingRecipe(); err != nil {
		log.Printf("读取未完成的环境记录失败: %v", err)
	} else {
		for _, j := range jobs {
			j.Recipe.Pending, j.Recipe.EnvError = false, "danlu 重启时环境记录未完成"
			m.save(j)
			m.queueManifest(j)
		}
	}
	running, err := m.jobsByStatus(store.StatusRunning)
	if err != nil {
		log.Printf("读取运行中任务失败: %v", err)
		return
	}
	for _, j := range running {
		switch {
		case j.PID == 0:
			m.reconcile(j)
		case m.ex.Alive(j.ID, j.PID, j.PIDStart):
			// 同一启动事件使用固定去重键，重启时补记也不会重复。
			m.transition(j, store.StatusRunning, "")
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
	defer func() {
		m.launches.Wait()
		m.captures.Wait()
		m.waitManifests()
	}()
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
	for _, j := range m.pending {
		m.save(j)
	}
	m.checkRunning(scanTracker)
	if sched {
		m.schedule()
		m.checkAlertsLocked(time.Now())
	}
}

func (m *Manager) checkRunning(scanTracker bool) {
	running, err := m.jobsByStatus(store.StatusRunning)
	if err != nil {
		log.Printf("读取运行中任务失败: %v", err)
		return
	}
	gpus, _, gpuErr := m.gpus.Latest()
	for _, j := range running {
		if _, ok := m.starting[j.ID]; ok {
			continue // 还在 launch 里，pid 还没有
		}
		if j.PID == 0 {
			m.reconcile(j)
			continue
		}
		if !m.ex.Alive(j.ID, j.PID, j.PIDStart) {
			m.finalize(j)
			continue
		}
		if gpuErr == nil && !j.ExtSeen && hasExternalProcess(gpus, j) {
			j.ExtSeen = true
			m.save(j)
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

func hasExternalProcess(gpus []gpu.GPU, j *store.Job) bool {
	for _, card := range gpus {
		if !slices.Contains(j.GPUsAssigned, card.Index) {
			continue
		}
		for _, p := range card.Procs {
			if p.JobID != j.ID {
				return true
			}
		}
	}
	return false
}

// reconcile 处理库里是 running、pid 还是 0、又不在 starting 里的任务：
// 占位以后 danlu 退出了，或者启动以后写库失败了。按实际情况认领、收尾或者放回队列，
// 绝不能再启动一次。
func (m *Manager) reconcile(j *store.Job) {
	if pid, start, ok := m.ex.Adopt(j.ID); ok {
		j.PID, j.PIDStart = pid, start
		m.transition(j, store.StatusRunning, "")
		log.Printf("认领启动途中的任务 #%d %s（pid %d）", j.ID, j.Name, pid)
		if j.CancelRequested {
			m.cancelAt[j.ID] = time.Now().Add(-m.opt.KillGrace)
		}
		return
	}
	if pid, launched := m.ex.Launched(j.ID); launched {
		j.PID = pid
		m.finalize(j) // 跑过，已经结束了：按退出码收尾
		return
	}
	if j.CancelRequested {
		m.transition(j, store.StatusCancelled, "用户取消")
		return
	}
	// 从没启动过：放回队列，下一轮重新分卡
	m.transition(j, store.StatusQueued, "启动途中被打断，尚未启动进程")
	log.Printf("任务 #%d %s 启动途中被打断，放回队列", j.ID, j.Name)
}

func (m *Manager) finalize(j *store.Job) {
	code, ok := m.ex.ReadExitCode(j.ID)
	if ok {
		j.ExitCode = &code
	}
	var status, reason string
	switch {
	case j.CancelRequested:
		status, reason = store.StatusCancelled, "用户取消"
	case ok && code == 0:
		status = store.StatusSucceeded
	default:
		status = store.StatusFailed
		var exitCode *int
		if ok {
			exitCode = &code
		}
		hostOOM, oomKnown := false, false
		if current, known := m.ex.OOMKills(); known && j.OOMBase >= 0 {
			hostOOM, oomKnown = current > j.OOMBase, true
		}
		failure := logs.ClassifyFailure(logs.FailureInput{
			ExitCode:     exitCode,
			Reboot:       !ok && j.BootID != "" && m.bootID != "" && j.BootID != m.bootID,
			OOMKnown:     oomKnown,
			HostOOM:      hostOOM,
			ExternalSeen: j.ExtSeen,
			LogTail:      m.logTail(j.ID),
		})
		j.FailKind, reason = failure.Kind, failure.Reason
		if offset := logs.TracebackOffset(m.ex.LogPath(j.ID)); offset >= 0 {
			j.TracebackOffset = &offset
		}
	}
	// 只按 DANLU_RUN_DIR 标记清理残留进程，不按进程组：
	// 包装脚本退出后进程组号可能被别的进程复用
	if n := m.ex.KillAll(j.ID, 0); n > 0 {
		note := fmt.Sprintf("已清理 %d 个残留进程", n)
		if j.Notes == "" {
			j.Notes = note
		} else {
			j.Notes += "；" + note
		}
	}
	if j.TrackerURL == "" {
		j.TrackerURL = logs.FindTracker(m.ex.LogPath(j.ID), 1<<20)
	}
	delete(m.cancelAt, j.ID)
	m.transition(j, status, reason)
	log.Printf("任务 #%d %s 结束：%s %s", j.ID, j.Name, j.Status, j.FailReason)
}

func (m *Manager) logTail(id int64) string {
	data, _, _, err := logs.Tail(m.ex.LogPath(id), 32<<10)
	if err != nil {
		return ""
	}
	return string(data)
}

func (m *Manager) failQueued(j *store.Job, reason string) {
	m.transition(j, store.StatusFailed, reason)
}

// schedule 严格按提交顺序：队首放不下就停，后面的任务不插队。
func (m *Manager) schedule() {
	if err := m.checkDiskGuardLocked(); err != nil {
		if m.diskError != err.Error() {
			log.Printf("磁盘保护暂停启动任务: %v", err)
		}
		m.diskError = err.Error()
		return
	}
	m.diskError = ""
	for id, j := range m.pending {
		if j.Status == store.StatusFailed && len(m.pendingEvents[id]) > 0 {
			return // 先完成失败转换及自动暂停判断，再允许后续任务启动。
		}
	}
	queue, err := m.queueStateLocked()
	if err != nil {
		log.Printf("读取队列状态失败，暂停启动任务: %v", err)
		return
	}
	if queue.Paused {
		return
	}
	queued, err := m.jobsByStatus(store.StatusQueued)
	if err != nil || len(queued) == 0 {
		return
	}
	gpus, _, gpuErr := m.gpus.Latest()
	running, err := m.jobsByStatus(store.StatusRunning)
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
		if _, unsaved := m.pending[j.ID]; unsaved {
			return // 重新排队等事件先落库，再启动下一次准备
		}
		if _, preparing := m.starting[j.ID]; preparing {
			return // 库里是排队、内存里还在准备，只会是库被别的进程改过：不能再启动一次
		}
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
	previous := *j
	port := freePort(ports)
	now := time.Now().Unix()
	j.PID, j.PIDStart, j.StartedAt, j.GPUsAssigned, j.MasterPort, j.BootID =
		0, 0, &now, gpus, port, m.bootID
	if j.OutputDir == "" && m.opt.OutputRoot != "" {
		j.OutputDir = runner.OutputPath(m.opt.OutputRoot, j.Workdir, j.ID, j.Name)
	}
	if err := m.transition(j, store.StatusRunning, ""); err != nil {
		// 占位失败时尚未执行任何准备或启动，不保留内存里的 running。
		delete(m.pending, j.ID)
		delete(m.pendingEvents, j.ID)
		*j = previous
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
	workdir := j.Workdir
	hadSnapshot := j.SnapshotOK
	var meta *snapshot.Meta
	var snapErr error
	var restoreNote string
	if j.CodeFrom == nil {
		meta, snapErr = m.snap(sctx, j)
	} else {
		source := *j.CodeFrom
		if hadSnapshot {
			source = j.ID
		}
		meta, restoreNote, snapErr = snapshot.Restore(sctx, m.opt.SnapshotDir, m.opt.RunRoot, j.Workdir, source, j.ID)
		workdir = snapshot.RunCodeDir(m.opt.RunRoot, j.ID)
	}
	cancel()
	var env []string
	var envErr error
	if j.CodeFrom != nil && snapErr != nil {
		envErr = fmt.Errorf("恢复任务 #%d 的快照失败: %w", *j.CodeFrom, snapErr)
	} else {
		env, envErr = m.ex.LoadEnv(ctx, j.InheritBashrc)
	}
	if snapErr != nil {
		j.SnapshotNote = "快照失败: " + snapErr.Error()
	} else {
		j.SnapshotOK = true
		if meta.Git != nil {
			j.GitCommit, j.GitBranch, j.GitDirty = meta.Git.Commit, meta.Git.Branch, meta.Git.Dirty
		}
		if j.CodeFrom != nil {
			j.SnapshotNote = restoreNote
		} else if meta.SkipCount > 0 {
			j.SnapshotNote = fmt.Sprintf("有 %d 个文件没有保存（超出大小限制或不是普通文件）", meta.SkipCount)
		}
	}
	recipe := &store.Recipe{Pending: true}
	recipe.Machine, recipe.MachineError, _ = m.machineRecipe(j.GPUsAssigned)
	j.Recipe = recipe
	var manifestErr error
	if ctx.Err() == nil && envErr == nil {
		manifestErr = m.writeManifest(ctx, j)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	ls, ok := m.starting[j.ID]
	if !ok {
		log.Printf("任务 #%d 的启动记录不见了，不再启动", j.ID)
		return
	}
	delete(m.starting, j.ID)
	switch {
	case ls.cancelled:
		j.CancelRequested = true
		j.Recipe = nil
		m.transition(j, store.StatusCancelled, "用户取消")
		log.Printf("任务 #%d %s 在启动前被取消", j.ID, j.Name)
		return
	case ctx.Err() != nil:
		if j.CodeFrom == nil || !hadSnapshot {
			j.SnapshotOK, j.SnapshotNote, j.GitCommit, j.GitBranch, j.GitDirty = false, "", "", "", false
		}
		m.transition(j, store.StatusQueued, "danlu 正在停止，尚未启动进程")
		log.Printf("danlu 正在停止，任务 #%d %s 放回队列", j.ID, j.Name)
		return
	case envErr != nil:
		j.FailKind = "start"
		j.Recipe = nil
		m.transition(j, store.StatusFailed, "启动失败: "+envErr.Error())
		log.Printf("任务 #%d 启动失败: %v", j.ID, envErr)
		return
	case manifestErr != nil:
		j.FailKind = "start"
		j.Recipe = nil
		m.transition(j, store.StatusFailed, "写入任务清单失败: "+manifestErr.Error())
		return
	}
	j.OOMBase = -1
	if count, known := m.ex.OOMKills(); known {
		j.OOMBase = count
	}
	if err := m.save(j); err != nil {
		log.Printf("任务 #%d 保存主机 OOM 基线失败，暂不启动: %v", j.ID, err)
		return
	}
	spec := runner.Spec{JobID: j.ID, Name: j.Name, Workdir: workdir,
		CondaEnv: j.CondaEnv, Cmd: j.Cmd, Env: j.Env, GPUs: j.GPUsAssigned, Port: j.MasterPort, OutputDir: j.OutputDir}
	pid, startTime, err := m.ex.Start(spec, env)
	if err != nil {
		j.FailKind = "start"
		j.Recipe = nil
		m.transition(j, store.StatusFailed, "启动失败: "+err.Error())
		log.Printf("任务 #%d 启动失败: %v", j.ID, err)
		return
	}
	j.PID, j.PIDStart = pid, startTime
	// 启动事件和 pid 一起保存；失败时下一轮用内存里的 pid 监控，不会再启动一次。
	m.transition(j, store.StatusRunning, "")
	m.captures.Add(1)
	go m.captureRecipe(ctx, j.ID, spec, slices.Clone(env))
	log.Printf("任务 #%d %s 已启动：GPU %v，pid %d，端口 %d", j.ID, j.Name, j.GPUsAssigned, pid, j.MasterPort)
}

func (m *Manager) captureRecipe(ctx context.Context, id int64, spec runner.Spec, env []string) {
	defer m.captures.Done()
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	packages, err := m.ex.CapturePackages(cctx, spec, env)
	var hash string
	if err == nil {
		hash, err = m.ex.StorePackages(packages)
	}
	machine, machineError := m.waitMachineRecipe(cctx, spec.GPUs)

	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.pending[id]
	if !ok {
		var getErr error
		j, getErr = m.st.GetJob(id)
		if getErr != nil {
			log.Printf("读取任务 #%d 以保存配方失败: %v", id, getErr)
			return
		}
	}
	recipe := &store.Recipe{}
	if j.Recipe != nil {
		recipe.Machine = j.Recipe.Machine
		recipe.MachineError = j.Recipe.MachineError
	}
	if machine != nil {
		recipe.Machine = machine
	}
	if machineError != "" {
		recipe.MachineError = machineError
	}
	if err != nil {
		recipe.EnvError = err.Error()
	} else {
		recipe.EnvHash = hash
	}
	j.Recipe = recipe
	if err := m.save(j); err != nil {
		log.Printf("保存任务 #%d 的环境配方失败: %v", id, err)
	}
	m.queueManifest(j)
}

func (m *Manager) machineRecipe(assigned []int) (*store.RecipeMachine, string, bool) {
	host, _ := os.Hostname()
	info := gpu.MachineInfo{Hostname: host}
	var machineErr string
	ready := true
	if source, ok := m.gpus.(interface {
		MachineLatest() (gpu.MachineInfo, time.Time, error)
	}); ok {
		var err error
		info, _, err = source.MachineLatest()
		if err != nil {
			machineErr = err.Error()
			ready = !errors.Is(err, gpu.ErrMachineInfoNotReady)
		}
		if info.Hostname == "" {
			info.Hostname = host
		}
	}
	result := &store.RecipeMachine{Hostname: info.Hostname, DriverVersion: info.DriverVersion, CUDAVersion: info.CUDAVersion}
	all, _, _ := m.gpus.Latest()
	byIndex := make(map[int]gpu.GPU, len(all))
	for _, card := range all {
		byIndex[card.Index] = card
	}
	for _, index := range assigned {
		card, ok := byIndex[index]
		if !ok {
			continue
		}
		result.GPUs = append(result.GPUs, store.RecipeGPU{Index: index, UUID: card.UUID, Name: card.Name})
	}
	return result, machineErr, ready
}

func (m *Manager) waitMachineRecipe(ctx context.Context, assigned []int) (*store.RecipeMachine, string) {
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		machine, err, ready := m.machineRecipe(assigned)
		if ready {
			return machine, err
		}
		select {
		case <-ctx.Done():
			return machine, ctx.Err().Error()
		case <-deadline.C:
			return machine, err
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// writeManifest 在启动前写清单。输出目录可能在卡住的网络存储上，最多等 ManifestTimeout，
// 超时按写入失败处理；卡住的那次写入留在后台，按目录加锁，挡不住别的任务。
func (m *Manager) writeManifest(ctx context.Context, j *store.Job) error {
	if j.OutputDir == "" {
		return nil
	}
	data, err := runner.ManifestJSON(j)
	if err != nil {
		return err
	}
	dir := j.OutputDir
	done := make(chan error, 1)
	m.manifests.Add(1)
	go func() {
		defer m.manifests.Done()
		done <- m.ex.WriteManifest(dir, data)
	}()
	timer := time.NewTimer(m.opt.ManifestTimeout)
	defer timer.Stop()
	select {
	case err := <-done:
		return err
	case <-timer.C:
		return fmt.Errorf("输出目录 %s 超过 %s 没有响应", dir, m.opt.ManifestTimeout)
	case <-ctx.Done():
		return ctx.Err()
	}
}

// waitManifests 在停止时等后台的清单写入，最多等 ManifestTimeout。清单只写文件、不碰数据库，
// 来不及写完的那份留在原样（临时文件会残留在输出目录里），不让卡住的存储拖住 danlu 退出。
func (m *Manager) waitManifests() {
	done := make(chan struct{})
	go func() { m.manifests.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(m.opt.ManifestTimeout):
		log.Printf("还有任务清单超过 %s 没写完，可能是输出目录没有响应，不再等待", m.opt.ManifestTimeout)
	}
}

func (m *Manager) queueManifest(j *store.Job, force ...bool) {
	forced := len(force) > 0 && force[0]
	if j.OutputDir == "" || (!forced && !j.Finished() && j.PID <= 0 && j.Recipe == nil) {
		return
	}
	data, err := runner.ManifestJSON(j)
	if err != nil {
		log.Printf("任务 #%d 生成任务清单失败: %v", j.ID, err)
		return
	}
	m.manifests.Add(1)
	go func(id int64, dir string, content []byte) {
		defer m.manifests.Done()
		if err := m.ex.WriteManifest(dir, content); err != nil {
			log.Printf("任务 #%d 更新任务清单失败: %v", id, err)
		}
	}(j.ID, j.OutputDir, data)
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
