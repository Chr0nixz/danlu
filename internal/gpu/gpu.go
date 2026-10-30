// Package gpu 通过 nvidia-smi 采集 GPU 状态，并识别每张卡上的进程属于哪个任务。
package gpu

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"danlu/internal/procfs"
)

type Proc struct {
	PID       int    `json:"pid"`
	UsedMemMB int    `json:"used_mem_mb"`
	User      string `json:"user"`
	JobID     int64  `json:"job_id"` // 0 表示不是 danlu 任务的进程
}

type GPU struct {
	Index      int     `json:"index"`
	UUID       string  `json:"uuid"`
	Name       string  `json:"name"`
	Util       int     `json:"util"`
	MemUsedMB  int     `json:"mem_used_mb"`
	MemTotalMB int     `json:"mem_total_mb"`
	TempC      int     `json:"temp_c"`
	PowerW     float64 `json:"power_w"`
	Procs      []Proc  `json:"procs"`
}

type Source interface {
	Query(ctx context.Context) ([]GPU, error)
}

// NewSource 默认用 nvidia-smi；设置 DANLU_FAKE_GPUS=N 时用假 GPU（测试用）。
func NewSource() Source {
	if n, _ := strconv.Atoi(os.Getenv("DANLU_FAKE_GPUS")); n > 0 {
		busy := map[int]bool{}
		for _, s := range strings.Split(os.Getenv("DANLU_FAKE_BUSY"), ",") {
			if i, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
				busy[i] = true
			}
		}
		return &Fake{N: n, Busy: busy}
	}
	return SMI{}
}

// ---------- nvidia-smi ----------

type SMI struct{}

const gpuQuery = "index,uuid,name,utilization.gpu,memory.used,memory.total,temperature.gpu,power.draw"

func (SMI) Query(ctx context.Context) ([]GPU, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "nvidia-smi", "--query-gpu="+gpuQuery, "--format=csv,noheader,nounits").Output()
	if err != nil {
		return nil, fmt.Errorf("nvidia-smi 执行失败: %w", err)
	}
	gpus, err := ParseGPUs(string(out))
	if err != nil {
		return nil, err
	}
	out, err = exec.CommandContext(ctx, "nvidia-smi", "--query-compute-apps=pid,gpu_uuid,used_memory", "--format=csv,noheader,nounits").Output()
	if err != nil {
		return gpus, nil // 查不到进程不致命，只是无法识别外部占用
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
		p := Proc{PID: a.PID, UsedMemMB: a.UsedMemMB, User: procfs.Owner(a.PID)}
		if v, ok := procfs.EnvVar(a.PID, "DANLU_JOB_ID"); ok {
			p.JobID, _ = strconv.ParseInt(v, 10, 64)
		}
		gpus[i].Procs = append(gpus[i].Procs, p)
	}
	return gpus, nil
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
		gpus = append(gpus, GPU{
			Index: idx, UUID: f[1], Name: f[2],
			Util: atoi(f[3]), MemUsedMB: atoi(f[4]), MemTotalMB: atoi(f[5]), TempC: atoi(f[6]),
			PowerW: atof(f[7]), Procs: []Proc{},
		})
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
			TempC: 35 + f.util[i]/3, PowerW: 60 + float64(f.util[i])*2.5, Procs: []Proc{}}
		if f.Busy[i] {
			g.Procs = append(g.Procs, Proc{PID: 900000 + i, UsedMemMB: 2048, User: "someone"})
		}
		gpus[i] = g
	}
	return gpus, nil
}

// ---------- 监控器：定时采集并缓存最新结果 ----------

type Monitor struct {
	src Source
	mu  sync.RWMutex
	gpu []GPU
	err error
	at  time.Time
}

func NewMonitor(src Source) *Monitor { return &Monitor{src: src} }

func (m *Monitor) Run(ctx context.Context, every time.Duration) {
	m.refresh(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			m.refresh(ctx)
		}
	}
}

func (m *Monitor) refresh(ctx context.Context) {
	g, err := m.src.Query(ctx)
	m.mu.Lock()
	m.gpu, m.err, m.at = g, err, time.Now()
	m.mu.Unlock()
}

// Latest 返回最近一次采集结果的副本。
func (m *Monitor) Latest() ([]GPU, time.Time, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]GPU, len(m.gpu))
	for i, g := range m.gpu {
		g.Procs = append([]Proc(nil), g.Procs...)
		out[i] = g
	}
	return out, m.at, m.err
}
