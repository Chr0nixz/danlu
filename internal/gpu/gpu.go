// Package gpu 通过 nvidia-smi 采集 GPU 状态，并识别每张卡上的进程属于哪个任务。
package gpu

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"danlu/internal/execx"
	"danlu/internal/procfs"
	"danlu/internal/sample"
)

type Proc struct {
	PID       int    `json:"pid"`
	UsedMemMB int    `json:"used_mem_mb"`
	User      string `json:"user"`
	JobID     int64  `json:"job_id"` // 0 表示不是 danlu 任务的进程
	// Graphics 表示图形进程（EGL/OpenGL 渲染），--query-compute-apps 列不出来，由 nvidia-smi -q -d PIDS 补上
	Graphics bool `json:"graphics,omitempty"`
	// CUDAVisible 是任务进程启动时的 CUDA_VISIBLE_DEVICES（空串表示没设，GPU UUID 换成了卡号，见 cardsInCVD），只有本实例任务的进程才有。
	// 任务进程出现在没分给它的卡上时，拿它和分配的卡比，就知道是不是启动器或脚本改写了选卡变量
	CUDAVisible *string `json:"cuda_visible,omitempty"`
}

type GPU struct {
	Index        int     `json:"index"`
	UUID         string  `json:"uuid"`
	Name         string  `json:"name"`
	Util         int     `json:"util"`
	MemUsedMB    int     `json:"mem_used_mb"`
	MemTotalMB   int     `json:"mem_total_mb"`
	UnknownMemMB int     `json:"unknown_mem_mb,omitempty"`
	TempC        int     `json:"temp_c"`
	PowerW       float64 `json:"power_w"`
	// 以下字段是资源详情页的扩展指标，老驱动可能返回 [N/A]，此时为 0/空
	FanPercent       int     `json:"fan_percent,omitempty"`
	ClockGraphicsMHz int     `json:"clock_graphics_mhz,omitempty"`
	ClockMemoryMHz   int     `json:"clock_memory_mhz,omitempty"`
	PowerLimitW      float64 `json:"power_limit_w,omitempty"`
	PState           string  `json:"pstate,omitempty"`
	Procs            []Proc  `json:"procs"`
	BusID            string  `json:"-"` // PCI 总线号，nvidia-smi -q 的输出按它分卡
}

type Source interface {
	Query(ctx context.Context) ([]GPU, error)
}

type MachineInfo struct {
	Hostname      string `json:"hostname"`
	DriverVersion string `json:"driver_version,omitempty"`
	CUDAVersion   string `json:"cuda_version,omitempty"`
}

type MachineSource interface {
	Machine(context.Context) (MachineInfo, error)
}

// NewSource 默认用 nvidia-smi；设置 DANLU_FAKE_GPUS=N 时用假 GPU（测试用）。
// runRoot 是本实例的 <数据目录>/run，只有运行目录在它下面的进程才算本实例的任务；
// 为空时不识别任务进程（比如 doctor 只看卡在不在）。
func NewSource(runRoot string, thresholds ...int) Source {
	unknownMemoryThresholdMB := 1024
	if len(thresholds) > 0 && thresholds[0] > 0 {
		unknownMemoryThresholdMB = thresholds[0]
	}
	return NewLiveSource(runRoot, func() int { return unknownMemoryThresholdMB })
}

// NewLiveSource 和 NewSource 一样，只是未知显存阈值每次查询时取一次：面板上可以随时改。
func NewLiveSource(runRoot string, threshold func() int) Source {
	if n, _ := strconv.Atoi(os.Getenv("DANLU_FAKE_GPUS")); n > 0 {
		busy := map[int]bool{}
		for _, s := range strings.Split(os.Getenv("DANLU_FAKE_BUSY"), ",") {
			if i, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				busy[i] = true
			}
		}
		return &Fake{N: n, Busy: busy}
	}
	return &SMI{RunRoot: runRoot, Threshold: threshold, query: smi}
}

// ---------- nvidia-smi ----------

// SMI 每次查询都先带上扩展指标。扩展字段不是调度必需的，老驱动不支持时自动降级到原查询。
// 降级后要过一段时间再试：驱动不支持时不会每一轮都多跑一次 nvidia-smi，
// 临时故障恢复后功耗、风扇等指标也能自己回来。
type SMI struct {
	RunRoot                  string // 见 NewSource
	UnknownMemoryThresholdMB int
	// Threshold 不为 nil 且返回值大于 0 时代替 UnknownMemoryThresholdMB，每次查询取一次
	Threshold func() int

	nextExtrasAt atomic.Int64                                     // UnixNano；在此之前只用基本查询
	extrasLog    sync.Once                                        // 降级日志只打印一次
	query        func(context.Context, ...string) ([]byte, error) // 测试时替换
}

const gpuQuery = "index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw,pci.bus_id"

// 扩展列从第 10 列开始，ParseGPUs 把它们当作可选列。
const gpuQueryExt = gpuQuery + ",fan.speed,clocks.current.graphics,clocks.current.memory,power.limit,pstate"

const (
	// 扩展查询失败后先退回基本查询：驱动不支持时十分钟再试一次，代价可以忽略；
	// 如果是临时故障，恢复后扩展指标会自动回来，不会整个进程周期都缺失。
	extrasRetryAfterFallback = 10 * time.Minute
	// 基本查询也失败时更像临时故障，一分钟后重试。
	extrasRetryAfterFailure = time.Minute
)

// 扩展查询失败前已经花了一半超时时间，这一轮不再补查基本查询（剩下的时间不够它跑完）。
// 是变量只为单测能调短。
var extrasSlowFailure = smiTimeout / 2

// ErrProcsUnknown 表示卡的数据拿到了，但查不到卡上的进程。这时被外部进程占着的卡看起来是空的，
// 所以 Query 照常返回卡的数据（面板能显示），同时带上这个错误，调度器据此不分配 GPU。
var ErrProcsUnknown = errors.New("查不到 GPU 上的进程，没法判断哪些卡被外部占用")
var ErrMachineInfoNotReady = errors.New("机器 GPU 信息尚未采集完成")

// 每次调用单独计时：没开 persistence mode 的机器上，nvidia-smi 调一次就可能要几秒
const smiTimeout = 8 * time.Second

// 补查图形进程只是尽力而为，给得短一些：卡的查询（扩展查询和同一轮补查的基本查询共用一个 smiTimeout）、
// 进程查询和这一次加起来不超过 Monitor 的 20 秒过期时间
const pidsTimeout = 3 * time.Second

func smi(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, smiTimeout)
	defer cancel()
	return execx.Output(execx.Command(ctx, "nvidia-smi", append(args, "--format=csv,noheader,nounits")...))
}

func (s *SMI) run(ctx context.Context, args ...string) ([]byte, error) {
	if s.query != nil {
		return s.query(ctx, args...)
	}
	return smi(ctx, args...)
}

// queryGPUs 先查扩展列；失败时按情况降级，并记住多久之后再试。
// 扩展列只给资源页用，调度靠的是基本列：扩展查询无论怎么失败，都不能让之后的轮次拿不到卡的数据。
func (s *SMI) queryGPUs(ctx context.Context) ([]byte, error) {
	if time.Now().UnixNano() < s.nextExtrasAt.Load() {
		return s.run(ctx, "--query-gpu="+gpuQuery)
	}
	started := time.Now()
	out, err := s.run(ctx, "--query-gpu="+gpuQueryExt)
	if err == nil {
		return out, nil
	}
	if ctx.Err() != nil {
		return nil, err
	}
	elapsed := time.Since(started)
	if elapsed >= extrasSlowFailure {
		// 失败得慢，分不清是 nvidia-smi 整个卡住，还是只有扩展列有问题（比如老驱动加上没开
		// persistence mode，每次调用都要好几秒）。这一轮不补查，但扩展列必须按降级暂停：
		// 不暂停的话，下一轮又先等扩展查询失败，基本查询明明能用也永远拿不到数据，
		// 调度器就一直不发 GPU 任务。真是整体卡住时，之后几轮的基本查询会照常报错。
		s.fallBack(err)
		return nil, err
	}
	// 和扩展查询共用一个 smiTimeout，这一轮卡的查询总耗时不比只跑基本查询时长。
	bctx, cancel := context.WithDeadline(ctx, started.Add(smiTimeout))
	defer cancel()
	base, baseErr := s.run(bctx, "--query-gpu="+gpuQuery)
	if baseErr != nil {
		// 两次都失败更像临时故障（驱动重置、GPU 掉线），不要永久关掉扩展列。
		s.nextExtrasAt.Store(time.Now().Add(extrasRetryAfterFailure).UnixNano())
		return nil, baseErr
	}
	// 基本查询成功而扩展查询失败，通常是驱动不认识新增字段。
	s.fallBack(err)
	return base, nil
}

// fallBack 暂停扩展列，过一段时间再试，驱动升级或临时故障恢复后指标会回来。
func (s *SMI) fallBack(err error) {
	s.nextExtrasAt.Store(time.Now().Add(extrasRetryAfterFallback).UnixNano())
	s.extrasLog.Do(func() {
		log.Printf("GPU 扩展指标不可用，暂用基本查询（%s 后重试）: %v", extrasRetryAfterFallback, err)
	})
}

func (s *SMI) Query(ctx context.Context) ([]GPU, error) {
	out, err := s.queryGPUs(ctx)
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi 执行失败: %w", err)
	}
	gpus, err := ParseGPUs(string(out))
	if err != nil {
		return nil, err
	}
	out, err = s.run(ctx, "--query-compute-apps=pid,gpu_uuid,used_memory")
	if err != nil {
		return gpus, fmt.Errorf("%w（nvidia-smi: %v）", ErrProcsUnknown, err)
	}
	apps := ParseApps(string(out))
	byUUID := map[string]int{}
	for i, g := range gpus {
		byUUID[g.UUID] = i
	}
	for _, a := range apps {
		i, ok := byUUID[a.UUID]
		if !ok {
			continue
		}
		// 别的数据目录的 danlu 任务也算外部进程：编号可能和本实例的任务相同
		job, cvd := procfs.JobEnv(a.PID, s.RunRoot)
		p := Proc{PID: a.PID, UsedMemMB: a.UsedMemMB, User: procfs.Owner(a.PID), JobID: job, CUDAVisible: cvd}
		gpus[i].Procs = append(gpus[i].Procs, p)
	}
	threshold := s.unknownThreshold()
	if hasUnaccounted(gpus) {
		// 显存对不上时多查一次全部进程。查不到不算错：这部分显存照旧算未知占用
		pctx, cancel := context.WithTimeout(ctx, pidsTimeout)
		out, err := execx.Output(execx.Command(pctx, "nvidia-smi", "-q", "-d", "PIDS"))
		cancel()
		if err == nil {
			addOtherProcs(gpus, ParsePIDs(string(out)), threshold,
				func(pid int) (int64, *string) { return procfs.JobEnv(pid, s.RunRoot) }, procfs.Owner)
		}
	}
	markUnknownMemory(gpus, threshold)
	cardsInCVD(gpus)
	return gpus, nil
}

// unknownThreshold 是这一轮查询用的未知显存阈值，同一轮里前后一致。
func (s *SMI) unknownThreshold() int {
	if s.Threshold != nil {
		if n := s.Threshold(); n > 0 {
			return n
		}
	}
	if s.UnknownMemoryThresholdMB > 0 {
		return s.UnknownMemoryThresholdMB
	}
	return 1024
}

// hasUnaccounted 判断有没有卡的已用显存比列出的进程加起来多。
func hasUnaccounted(gpus []GPU) bool {
	for _, g := range gpus {
		if g.MemUsedMB > procMem(g.Procs) {
			return true
		}
	}
	return false
}

func procMem(procs []Proc) int {
	total := 0
	for _, p := range procs {
		total += p.UsedMemMB
	}
	return total
}

// cardsInCVD 把任务进程选卡变量里的 GPU UUID 换成 nvidia-smi 的卡号，其余照旧。
// 有的框架用 NVML 或 nvidia-smi 的编号查 UUID 再写进 CUDA_VISIBLE_DEVICES（NVML 不看这个变量，
// 编号是物理卡号），原样显示是一长串 UUID；换成卡号才能和分配的卡比，也看得懂。
// CUDA 接受 UUID 前缀，所以按前缀匹配；MIG 设备和对不上的项原样保留
func cardsInCVD(gpus []GPU) {
	for i := range gpus {
		for j := range gpus[i].Procs {
			p := &gpus[i].Procs[j]
			if p.CUDAVisible == nil || !strings.Contains(*p.CUDAVisible, "GPU-") {
				continue
			}
			parts := strings.Split(*p.CUDAVisible, ",")
			for k, part := range parts {
				part = strings.TrimSpace(part)
				if strings.HasPrefix(part, "GPU-") {
					for _, g := range gpus {
						if g.UUID != "" && strings.HasPrefix(g.UUID, part) {
							part = strconv.Itoa(g.Index)
							break
						}
					}
				}
				parts[k] = part
			}
			v := strings.Join(parts, ",")
			p.CUDAVisible = &v
		}
	}
}

func markUnknownMemory(gpus []GPU, thresholdMB int) {
	for i := range gpus {
		gpus[i].UnknownMemMB = 0
		unaccounted := max(0, gpus[i].MemUsedMB-procMem(gpus[i].Procs))
		if unaccounted > thresholdMB {
			gpus[i].UnknownMemMB = unaccounted
		}
	}
}

// PIDEntry 是 nvidia-smi -q -d PIDS 里的一个进程。Type 是 C、G、C+G、M+C 这类。
type PIDEntry struct {
	BusID     string
	PID       int
	Type      string
	UsedMemMB int
}

// ParsePIDs 解析 nvidia-smi -q -d PIDS。它和 --query-compute-apps 不同，图形进程也列出来；
// 卡用 PCI 总线号标识（顶格的 "GPU 00000000:18:00.0" 行）。
func ParsePIDs(out string) []PIDEntry {
	var entries []PIDEntry
	bus, cur := "", -1
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "GPU ") {
			bus, cur = strings.TrimSpace(line[len("GPU "):]), -1
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok || bus == "" {
			continue
		}
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		switch key {
		case "Process ID":
			pid, err := strconv.Atoi(val)
			if err != nil {
				cur = -1
				continue
			}
			entries = append(entries, PIDEntry{BusID: bus, PID: pid})
			cur = len(entries) - 1
		case "Type":
			if cur >= 0 {
				entries[cur].Type = val
			}
		case "Used GPU Memory":
			if cur >= 0 {
				// "1024 MiB"；N/A 之类的当作 0
				num, _, _ := strings.Cut(val, " ")
				entries[cur].UsedMemMB, _ = strconv.Atoi(num)
			}
		}
	}
	return entries
}

// normalizeBusID 统一 PCI 总线号的写法：域号有 4 位和 8 位两种，十六进制大小写也可能不同。
func normalizeBusID(id string) string {
	domain, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(id)), ":")
	if !ok {
		return ""
	}
	return strings.TrimLeft(domain, "0") + ":" + rest
}

// addOtherProcs 把 compute-apps 里没有的进程补进 gpus，主要是图形进程。
//
// 图形进程（EGL/OpenGL 离屏渲染，比如 MuJoCo、robosuite、LIBERO 的仿真画面）不在 --query-compute-apps 里，
// 显存原本只能算成未知占用。EGL 按自己的设备编号选卡，不受 CUDA_VISIBLE_DEVICES 限制，
// 渲染经常落在没分给任务的卡上，所以要认出它们属于哪个任务。
//
// 不属于本实例任务、显存又不超过阈值的进程不列出来：Xorg、桌面这类小进程每张卡上都可能有，
// 列成外部进程会让所有卡都分不出去。它们的显存照旧算进未知占用，加起来超过阈值时卡一样不分配。
func addOtherProcs(gpus []GPU, entries []PIDEntry, thresholdMB int, jobOf func(int) (int64, *string), owner func(int) string) {
	byBus := make(map[string]int, len(gpus))
	for i, g := range gpus {
		if id := normalizeBusID(g.BusID); id != "" {
			byBus[id] = i
		}
	}
	for _, e := range entries {
		i, ok := byBus[normalizeBusID(e.BusID)]
		if !ok || slices.ContainsFunc(gpus[i].Procs, func(p Proc) bool { return p.PID == e.PID }) {
			continue
		}
		job, cvd := jobOf(e.PID)
		if job == 0 && e.UsedMemMB <= thresholdMB {
			continue
		}
		gpus[i].Procs = append(gpus[i].Procs, Proc{PID: e.PID, UsedMemMB: e.UsedMemMB, User: owner(e.PID), JobID: job,
			Graphics: e.Type != "" && !strings.Contains(e.Type, "C"), CUDAVisible: cvd})
	}
}

var machineVersionRE = regexp.MustCompile(`Driver Version:\s*([^\s|]+).*CUDA Version:\s*([^\s|]+)`)

func parseMachineVersions(output string) (string, string, error) {
	match := machineVersionRE.FindStringSubmatch(output)
	if len(match) != 3 {
		return "", "", errors.New("无法从 nvidia-smi 输出读取驱动和 CUDA 版本")
	}
	return match[1], match[2], nil
}

func (*SMI) Machine(ctx context.Context) (MachineInfo, error) {
	info := MachineInfo{}
	info.Hostname, _ = os.Hostname()
	ctx, cancel := context.WithTimeout(ctx, smiTimeout)
	defer cancel()
	out, err := execx.Output(execx.Command(ctx, "nvidia-smi"))
	if err != nil {
		return info, fmt.Errorf("读取机器 GPU 版本失败: %w", err)
	}
	info.DriverVersion, info.CUDAVersion, err = parseMachineVersions(string(out))
	return info, err
}

func ParseGPUs(out string) ([]GPU, error) {
	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		f := splitCSV(line)
		if len(f) < 8 {
			return nil, fmt.Errorf("无法解析 nvidia-smi 输出: %q", line)
		}
		idx, err := strconv.Atoi(f[0])
		if err != nil {
			return nil, fmt.Errorf("无法解析 GPU 序号: %q", line)
		}
		g := GPU{
			Index: idx, UUID: f[1], Name: f[2],
			Util: atoi(f[3]), MemUsedMB: atoi(f[4]), MemTotalMB: atoi(f[5]), TempC: atoi(f[6]),
			PowerW: atof(f[7]), Procs: []Proc{},
		}
		if len(f) > 8 {
			g.BusID = f[8]
		}
		// 扩展列都是可选的；[N/A] 经 atoi/atof 后是 0，P-State 单独清空。
		if len(f) > 9 {
			g.FanPercent = atoi(f[9])
		}
		if len(f) > 10 {
			g.ClockGraphicsMHz = atoi(f[10])
		}
		if len(f) > 11 {
			g.ClockMemoryMHz = atoi(f[11])
		}
		if len(f) > 12 {
			g.PowerLimitW = atof(f[12])
		}
		if len(f) > 13 && !gpuFieldUnavailable(f[13]) {
			g.PState = f[13]
		}
		gpus = append(gpus, g)
	}
	if len(gpus) == 0 {
		return nil, errors.New("nvidia-smi 没有返回任何 GPU")
	}
	return gpus, nil
}

type App struct {
	PID       int
	UUID      string
	UsedMemMB int
}

func ParseApps(out string) []App {
	var apps []App
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		f := splitCSV(line)
		if len(f) < 3 {
			continue
		}
		pid, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		apps = append(apps, App{PID: pid, UUID: f[1], UsedMemMB: atoi(f[2])})
	}
	return apps
}

func splitCSV(line string) []string {
	f := strings.Split(line, ",")
	for i := range f {
		f[i] = strings.TrimSpace(f[i])
	}
	return f
}

// gpuFieldUnavailable 判断字符串字段是不是驱动给出的不可用占位。
func gpuFieldUnavailable(s string) bool {
	s = strings.TrimSpace(s)
	return s == "" || strings.Contains(s, "N/A") || strings.Contains(s, "Not Supported")
}

// 不支持的字段会显示为 [N/A] 或 [Not Supported]，统一当作 0
func atoi(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return int(atof(s))
	}
	return v
}

func atof(s string) float64 {
	v, _ := strconv.ParseFloat(s, 64)
	return v
}

// ---------- 假 GPU ----------

type Fake struct {
	N    int
	Busy map[int]bool // 模拟被外部进程占用的卡
	mu   sync.Mutex
	util []int
}

func (f *Fake) Machine(context.Context) (MachineInfo, error) {
	host, _ := os.Hostname()
	return MachineInfo{Hostname: host, DriverVersion: "fake", CUDAVersion: "fake"}, nil
}

func (f *Fake) Query(context.Context) ([]GPU, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.util == nil {
		f.util = make([]int, f.N)
	}
	gpus := make([]GPU, f.N)
	for i := range gpus {
		f.util[i] = max(0, min(100, f.util[i]+rand.IntN(21)-10))
		g := GPU{Index: i, UUID: fmt.Sprintf("GPU-fake-%d", i), Name: "Fake GPU 24GB",
			Util: f.util[i], MemUsedMB: 300 + f.util[i]*100, MemTotalMB: 24576,
			TempC: 35 + f.util[i]/3, PowerW: 60 + float64(f.util[i])*2.5, Procs: []Proc{},
			FanPercent: 30 + f.util[i]/4, ClockGraphicsMHz: 900 + f.util[i]*12,
			ClockMemoryMHz: 8000, PowerLimitW: 350, PState: "P0"}
		if f.Busy[i] {
			g.Procs = append(g.Procs, Proc{PID: 900000 + i, UsedMemMB: 2048, User: "someone"})
		}
		gpus[i] = g
	}
	return gpus, nil
}

// ---------- 监控器：定时采集并缓存最新结果 ----------

type Monitor struct {
	*sample.Sampler[[]GPU]
	machine *sample.Sampler[MachineInfo]
}

// NewMonitor 每 2 秒采集一次。超过 20 秒没有一次采集完成（两次 nvidia-smi 各 8 秒超时，补查图形进程 3 秒，再留点余量），
// Latest 就返回错误，调度器暂停分配 GPU。
func NewMonitor(src Source) *Monitor {
	m := &Monitor{Sampler: &sample.Sampler[[]GPU]{Name: "GPU 数据", Hint: "（nvidia-smi 可能卡住了）",
		Every: 2 * time.Second, Stale: 20 * time.Second, Query: src.Query, Clone: cloneGPUs}}
	m.machine = &sample.Sampler[MachineInfo]{Name: "机器 GPU 信息", Every: time.Hour, Stale: 2 * time.Hour,
		Query: func(ctx context.Context) (MachineInfo, error) {
			if source, ok := src.(MachineSource); ok {
				return source.Machine(ctx)
			}
			host, _ := os.Hostname()
			return MachineInfo{Hostname: host}, nil
		}}
	return m
}

func (m *Monitor) Run(ctx context.Context) {
	go m.machine.Run(ctx)
	m.Sampler.Run(ctx)
}

func (m *Monitor) MachineLatest() (MachineInfo, time.Time, error) {
	info, at, err := m.machine.Latest()
	if at.IsZero() && err == nil {
		return info, at, ErrMachineInfoNotReady
	}
	return info, at, err
}

func cloneGPUs(gpus []GPU) []GPU {
	out := make([]GPU, len(gpus))
	for i, g := range gpus {
		// 空列表也要保持非 nil：nil 切片会被 JSON 编码成 null，面板按数组读 procs 会直接报错
		g.Procs = append([]Proc{}, g.Procs...)
		out[i] = g
	}
	return out
}
