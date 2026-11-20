package system

import (
	"context"
	"sync"
	"time"

	"danlu/internal/sample"
)

const (
	// ResourceInterval 是资源详情页的主机采样间隔。
	ResourceInterval = 2 * time.Second
	// 历史只保存最近 1 小时，服务重启后清零。
	ResourceHistory = time.Hour
	// 接口返回的历史最多降采样到这么多点。
	ResourceHistoryMaxPoints = 180
)

type HostInfo struct {
	Hostname       string `json:"hostname"`
	OS             string `json:"os,omitempty"`
	Kernel         string `json:"kernel,omitempty"`
	CPUModel       string `json:"cpu_model,omitempty"`
	CPUCores       int    `json:"cpu_cores"`
	MemoryTotal    uint64 `json:"memory_total_bytes"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	UptimeSeconds  int64  `json:"uptime_seconds"`
	BootTime       int64  `json:"boot_time,omitempty"`
}

type CPUInfo struct {
	Percent    *int     `json:"percent"`
	Cores      []int    `json:"cores"` // -1 表示还没有第二次采样
	Load1      float64  `json:"load_1"`
	Load5      float64  `json:"load_5"`
	Load15     float64  `json:"load_15"`
	FreqMHz    *float64 `json:"freq_mhz"`
	FreqMaxMHz *float64 `json:"freq_max_mhz"`
	TempC      *float64 `json:"temp_c"`
	PowerW     *float64 `json:"power_w"`
	// PowerError 说明 CPU 功耗为什么读不到（多半是内核只允许 root 读能量计数器）；
	// 只是还在等第二次读数时为空。
	PowerError string `json:"power_error,omitempty"`
}

type MemoryInfo struct {
	TotalBytes     uint64 `json:"total_bytes"`
	UsedBytes      uint64 `json:"used_bytes"`
	AvailableBytes uint64 `json:"available_bytes"`
	FreeBytes      uint64 `json:"free_bytes"`
	CachedBytes    uint64 `json:"cached_bytes"`
	BuffersBytes   uint64 `json:"buffers_bytes"`
	Percent        int    `json:"percent"`
	SwapTotalBytes uint64 `json:"swap_total_bytes"`
	SwapUsedBytes  uint64 `json:"swap_used_bytes"`
	SwapPercent    int    `json:"swap_percent"`
}

type NetIface struct {
	Name      string `json:"name"`
	Virtual   bool   `json:"virtual"`
	SpeedMbps int    `json:"speed_mbps,omitempty"`
	RxBps     uint64 `json:"rx_bytes_per_sec"`
	TxBps     uint64 `json:"tx_bytes_per_sec"`
	RxTotal   uint64 `json:"rx_total_bytes"`
	TxTotal   uint64 `json:"tx_total_bytes"`
	RxErrors  uint64 `json:"rx_errors"`
	TxErrors  uint64 `json:"tx_errors"`
	RxDrops   uint64 `json:"rx_drops"`
	TxDrops   uint64 `json:"tx_drops"`
}

type DiskIO struct {
	Name       string `json:"name"`
	ReadBps    uint64 `json:"read_bytes_per_sec"`
	WriteBps   uint64 `json:"write_bytes_per_sec"`
	ReadTotal  uint64 `json:"read_total_bytes"`
	WriteTotal uint64 `json:"write_total_bytes"`
	Busy       int    `json:"busy_percent"`
}

type ProcessInfo struct {
	PID        int     `json:"pid"`
	User       string  `json:"user"`
	Command    string  `json:"command"`
	CPUPercent float64 `json:"cpu_percent"`
	RSSBytes   uint64  `json:"rss_bytes"`
	JobID      int64   `json:"job_id"`
	JobName    string  `json:"job_name,omitempty"`
}

// GPUStats 是主机采集从 GPU 监控器拿到的少量汇总数据。
type GPUStats struct {
	PowerW     float64
	PowerKnown bool
	MaxTempC   int
	TempKnown  bool
}

type ResourceSnapshot struct {
	At             int64 `json:"-"` // 采样成功的时间；接口按它给出 updated_at，0 表示还没有成功过
	Samples        int   `json:"samples"`
	ProcessSamples int   `json:"process_samples"`

	Host      HostInfo      `json:"host"`
	CPU       CPUInfo       `json:"cpu"`
	Memory    MemoryInfo    `json:"memory"`
	Network   []NetIface    `json:"network"`
	DiskIO    []DiskIO      `json:"disk_io"`
	Processes []ProcessInfo `json:"processes"`

	GPUPowerW   *float64 `json:"gpu_power_w"`
	GPUTempMaxC *int     `json:"gpu_temp_max_c"`
	PowerW      *float64 `json:"power_w"`
	EnergyWh    *float64 `json:"energy_wh"`

	NetRxBps     uint64 `json:"net_rx_bps"`
	NetTxBps     uint64 `json:"net_tx_bps"`
	DiskReadBps  uint64 `json:"disk_read_bps"`
	DiskWriteBps uint64 `json:"disk_write_bps"`

	ProcessesAt int64 `json:"processes_at"`
	RootDisk    *Disk `json:"root_disk,omitempty"`
	RootDiskAt  int64 `json:"root_disk_at,omitempty"`

	NetworkError   string `json:"network_error,omitempty"`
	DiskIOError    string `json:"disk_io_error,omitempty"`
	ProcessesError string `json:"processes_error,omitempty"`
}

type ResourcePoint struct {
	At            int64    `json:"at"`
	CPUPercent    *int     `json:"cpu_percent"`
	MemoryPercent int      `json:"memory_percent"`
	SwapPercent   int      `json:"swap_percent"`
	NetRxBps      uint64   `json:"net_rx_bps"`
	NetTxBps      uint64   `json:"net_tx_bps"`
	DiskReadBps   uint64   `json:"disk_read_bps"`
	DiskWriteBps  uint64   `json:"disk_write_bps"`
	CPUPowerW     *float64 `json:"cpu_power_w"`
	GPUPowerW     *float64 `json:"gpu_power_w"`
	// 丹炉自身占用（数据目录总量、数据库逻辑大小）；自身统计还没成功或已过期时为 null，不画假 0。
	DataBytes *uint64 `json:"data_bytes"`
	DBBytes   *uint64 `json:"db_bytes"`
}

type ResourcePeaks struct {
	CPUPercent    *int     `json:"cpu_percent"`
	MemoryPercent int      `json:"memory_percent"`
	SwapPercent   int      `json:"swap_percent"`
	NetRxBps      uint64   `json:"net_rx_bps"`
	NetTxBps      uint64   `json:"net_tx_bps"`
	DiskReadBps   uint64   `json:"disk_read_bps"`
	DiskWriteBps  uint64   `json:"disk_write_bps"`
	CPUPowerW     *float64 `json:"cpu_power_w"`
	GPUPowerW     *float64 `json:"gpu_power_w"`
	DataBytes     *uint64  `json:"data_bytes"`
	DBBytes       *uint64  `json:"db_bytes"`
}

type ResourceSource interface {
	Query(context.Context) (ResourceSnapshot, error)
}

// ResourceMonitor 保存最新主机快照和最近 1 小时的压缩历史。
type ResourceMonitor struct {
	*sample.Sampler[ResourceSnapshot]

	// SelfBytes 读自身占用监控的缓存值（数据目录总量、数据库逻辑大小），
	// 在 Run 启动前设置，只由采集 goroutine 调用。未设置时历史里没有占用曲线。
	SelfBytes func() (data, db uint64, ok bool)

	mu     sync.RWMutex
	points []ResourcePoint
}

func NewResourceMonitor(src ResourceSource) *ResourceMonitor {
	m := &ResourceMonitor{}
	// lastGood 只在采集 goroutine 里读写（Sampler.Run 同步调用 Query），无需加锁。
	var lastGood ResourceSnapshot
	m.Sampler = &sample.Sampler[ResourceSnapshot]{
		Name:  "服务器资源数据",
		Hint:  "（/proc 读取可能卡住了）",
		Every: ResourceInterval,
		Stale: 20 * time.Second,
		Query: func(ctx context.Context) (ResourceSnapshot, error) {
			snap, err := src.Query(ctx)
			if err == nil {
				m.record(snap)
				lastGood = snap
				return snap, nil
			}
			// 采集失败时返回上一份好快照而不是零值：Sampler 会缓存返回值，零值会带着
			// 新鲜时间戳进到接口，前端把"内存 0%"当成活数据显示。首次就失败时 lastGood
			// 是零值，此时本来也没有数据，前端走"等待第一次采样"。
			return lastGood, err
		},
		Clone: cloneResourceSnapshot,
	}
	return m
}

func (m *ResourceMonitor) record(snap ResourceSnapshot) {
	if snap.At <= 0 {
		return
	}
	p := ResourcePoint{
		At: snap.At, CPUPercent: cloneInt(snap.CPU.Percent),
		MemoryPercent: snap.Memory.Percent, SwapPercent: snap.Memory.SwapPercent,
		NetRxBps: snap.NetRxBps, NetTxBps: snap.NetTxBps,
		DiskReadBps: snap.DiskReadBps, DiskWriteBps: snap.DiskWriteBps,
		CPUPowerW: cloneFloat(snap.CPU.PowerW), GPUPowerW: cloneFloat(snap.GPUPowerW),
	}
	if m.SelfBytes != nil {
		if data, db, ok := m.SelfBytes(); ok {
			p.DataBytes, p.DBBytes = cloneUint64(data), cloneUint64(db)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if n := len(m.points); n > 0 && m.points[n-1].At == p.At {
		m.points[n-1] = p
	} else {
		m.points = append(m.points, p)
	}
	cutoff := p.At - int64(ResourceHistory.Seconds())
	first := 0
	for first < len(m.points) && m.points[first].At < cutoff {
		first++
	}
	if first > 0 {
		// 原地搬移，避免进入稳定状态后每 2 秒重新分配整段历史。
		m.points = append(m.points[:0], m.points[first:]...)
	}
}

// History 返回指定范围的历史、按原始点计算的峰值，以及请求的时间窗口。
func (m *ResourceMonitor) History(rng time.Duration) ([]ResourcePoint, ResourcePeaks, time.Time, time.Time) {
	now := time.Now()
	from, to := now.Add(-rng), now
	m.mu.RLock()
	all := append([]ResourcePoint(nil), m.points...)
	m.mu.RUnlock()
	filtered := make([]ResourcePoint, 0, len(all))
	fromUnix := from.Unix()
	for _, p := range all {
		if p.At >= fromUnix && p.At <= to.Unix() {
			filtered = append(filtered, p)
		}
	}
	peaks := resourcePeaks(filtered)
	return downsampleResource(filtered, ResourceHistoryMaxPoints), peaks, from, to
}

func resourcePeaks(points []ResourcePoint) ResourcePeaks {
	var peaks ResourcePeaks
	for _, p := range points {
		if p.CPUPercent != nil && (peaks.CPUPercent == nil || *p.CPUPercent > *peaks.CPUPercent) {
			peaks.CPUPercent = cloneInt(p.CPUPercent)
		}
		peaks.MemoryPercent = max(peaks.MemoryPercent, p.MemoryPercent)
		peaks.SwapPercent = max(peaks.SwapPercent, p.SwapPercent)
		peaks.NetRxBps = max(peaks.NetRxBps, p.NetRxBps)
		peaks.NetTxBps = max(peaks.NetTxBps, p.NetTxBps)
		peaks.DiskReadBps = max(peaks.DiskReadBps, p.DiskReadBps)
		peaks.DiskWriteBps = max(peaks.DiskWriteBps, p.DiskWriteBps)
		if p.CPUPowerW != nil && (peaks.CPUPowerW == nil || *p.CPUPowerW > *peaks.CPUPowerW) {
			peaks.CPUPowerW = cloneFloat(p.CPUPowerW)
		}
		if p.GPUPowerW != nil && (peaks.GPUPowerW == nil || *p.GPUPowerW > *peaks.GPUPowerW) {
			peaks.GPUPowerW = cloneFloat(p.GPUPowerW)
		}
		if p.DataBytes != nil && (peaks.DataBytes == nil || *p.DataBytes > *peaks.DataBytes) {
			peaks.DataBytes = cloneUint64(*p.DataBytes)
		}
		if p.DBBytes != nil && (peaks.DBBytes == nil || *p.DBBytes > *peaks.DBBytes) {
			peaks.DBBytes = cloneUint64(*p.DBBytes)
		}
	}
	return peaks
}

// downsampleResource 按顺序分桶取平均，最多保留 max 个点；时间戳用桶内平均，图表仍能反映真实间隔。
func downsampleResource(points []ResourcePoint, maxPoints int) []ResourcePoint {
	if maxPoints <= 0 || len(points) <= maxPoints {
		return points
	}
	out := make([]ResourcePoint, 0, maxPoints)
	for bucket := 0; bucket < maxPoints; bucket++ {
		start := bucket * len(points) / maxPoints
		end := (bucket + 1) * len(points) / maxPoints
		if start == end {
			continue
		}
		part := points[start:end]
		p := ResourcePoint{
			At:            averageInt64(part, func(x ResourcePoint) int64 { return x.At }),
			MemoryPercent: int(averageInt64(part, func(x ResourcePoint) int64 { return int64(x.MemoryPercent) })),
			SwapPercent:   int(averageInt64(part, func(x ResourcePoint) int64 { return int64(x.SwapPercent) })),
			NetRxBps:      averageUint64(part, func(x ResourcePoint) uint64 { return x.NetRxBps }),
			NetTxBps:      averageUint64(part, func(x ResourcePoint) uint64 { return x.NetTxBps }),
			DiskReadBps:   averageUint64(part, func(x ResourcePoint) uint64 { return x.DiskReadBps }),
			DiskWriteBps:  averageUint64(part, func(x ResourcePoint) uint64 { return x.DiskWriteBps }),
		}
		if values := averageIntPointers(part, func(x ResourcePoint) *int { return x.CPUPercent }); values != nil {
			p.CPUPercent = values
		}
		if values := averageFloatPointers(part, func(x ResourcePoint) *float64 { return x.CPUPowerW }); values != nil {
			p.CPUPowerW = values
		}
		if values := averageFloatPointers(part, func(x ResourcePoint) *float64 { return x.GPUPowerW }); values != nil {
			p.GPUPowerW = values
		}
		if values := averageUint64Pointers(part, func(x ResourcePoint) *uint64 { return x.DataBytes }); values != nil {
			p.DataBytes = values
		}
		if values := averageUint64Pointers(part, func(x ResourcePoint) *uint64 { return x.DBBytes }); values != nil {
			p.DBBytes = values
		}
		out = append(out, p)
	}
	return out
}

func averageInt64(points []ResourcePoint, pick func(ResourcePoint) int64) int64 {
	if len(points) == 0 {
		return 0
	}
	var total int64
	for _, p := range points {
		total += pick(p)
	}
	return total / int64(len(points))
}

func averageUint64(points []ResourcePoint, pick func(ResourcePoint) uint64) uint64 {
	if len(points) == 0 {
		return 0
	}
	var total uint64
	for _, p := range points {
		total += pick(p)
	}
	return total / uint64(len(points))
}

func averageIntPointers(points []ResourcePoint, pick func(ResourcePoint) *int) *int {
	var total, n int
	for _, p := range points {
		if v := pick(p); v != nil {
			total += *v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	v := int(mathRound(float64(total) / float64(n)))
	return &v
}

func averageFloatPointers(points []ResourcePoint, pick func(ResourcePoint) *float64) *float64 {
	var total float64
	var n int
	for _, p := range points {
		if v := pick(p); v != nil {
			total += *v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	v := total / float64(n)
	return &v
}

func averageUint64Pointers(points []ResourcePoint, pick func(ResourcePoint) *uint64) *uint64 {
	var total uint64
	var n int
	for _, p := range points {
		if v := pick(p); v != nil {
			total += *v
			n++
		}
	}
	if n == 0 {
		return nil
	}
	v := total / uint64(n)
	return &v
}

func mathRound(v float64) int64 {
	if v < 0 {
		return 0
	}
	return int64(v + 0.5)
}

func cloneResourceSnapshot(in ResourceSnapshot) ResourceSnapshot {
	out := in
	out.CPU.Cores = append([]int(nil), in.CPU.Cores...)
	out.CPU.Percent = cloneInt(in.CPU.Percent)
	out.CPU.FreqMHz = cloneFloat(in.CPU.FreqMHz)
	out.CPU.FreqMaxMHz = cloneFloat(in.CPU.FreqMaxMHz)
	out.CPU.TempC = cloneFloat(in.CPU.TempC)
	out.CPU.PowerW = cloneFloat(in.CPU.PowerW)
	out.Network = append([]NetIface(nil), in.Network...)
	out.DiskIO = append([]DiskIO(nil), in.DiskIO...)
	out.Processes = append([]ProcessInfo(nil), in.Processes...)
	out.GPUPowerW = cloneFloat(in.GPUPowerW)
	out.GPUTempMaxC = cloneInt(in.GPUTempMaxC)
	out.PowerW = cloneFloat(in.PowerW)
	out.EnergyWh = cloneFloat(in.EnergyWh)
	if in.RootDisk != nil {
		root := *in.RootDisk
		root.Paths = append([]string(nil), in.RootDisk.Paths...)
		out.RootDisk = &root
	}
	return out
}

func cloneInt(v *int) *int {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

func cloneFloat(v *float64) *float64 {
	if v == nil {
		return nil
	}
	n := *v
	return &n
}

func cloneUint64(v uint64) *uint64 {
	return &v
}
