//go:build linux

package system

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"danlu/internal/procfs"
)

const processInterval = 10 * time.Second

// 每 10 秒读一遍 /proc/<pid>/stat，几千个进程也只要几十毫秒。上限只是保险：
// 超过时均匀抽样，没抽中的进程这一轮不进榜、下一轮也算不出 CPU。
const maxScannedProcesses = 8192
const maxTopProcesses = 16

type procKey struct {
	pid   int
	start uint64
}

type procEntry struct {
	pid   int
	start uint64
	comm  string
	cpu   float64
	rss   uint64
}

// ProcResourceSource 从 /proc 和 /sys 读取资源详情。Query 只由 sample.Sampler 的一个 goroutine 调用。
type ProcResourceSource struct {
	runRoot   string
	warnBytes uint64
	gpuStats  func() GPUStats
	rootStat  diskStatFunc

	prevCPU   cpuTimes
	prevCores map[int]cpuTimes
	prevAt    time.Time

	prevNet    map[string]netCounter
	prevNetAt  time.Time
	prevDisk   map[string]diskCounter
	prevDiskAt time.Time

	staticLoaded bool
	staticHost   HostInfo

	// 链路速率几乎不变，缓存一分钟，省掉每 2 秒每个网卡一次 sysfs 读取
	speedsAt map[string]time.Time
	speeds   map[string]int

	freqPaths   []string
	freqScanned bool

	tempPaths   []string
	tempScanned bool

	power powerMeter

	processes      []ProcessInfo
	processesAt    int64
	processSamples int
	processesError string
	lastProcAt     time.Time // 上次成功扫描的时间，也是进程 CPU 的基准
	nextProcAt     time.Time // 下次允许扫描；失败后仍要等满 10 秒
	lastProcCPU    uint64
	prevProc       map[procKey]uint64

	samples     int
	lastPowerAt time.Time
	energyWh    float64
	energyKnown bool
}

func NewResourceSource(runRoot string, warnBytes uint64, gpuStats func() GPUStats) ResourceSource {
	return &ProcResourceSource{
		runRoot:   runRoot,
		warnBytes: warnBytes,
		gpuStats:  gpuStats,
		rootStat:  boundedStat(diskStats, statTimeout),
		prevCores: map[int]cpuTimes{},
		prevNet:   map[string]netCounter{},
		prevDisk:  map[string]diskCounter{},
		prevProc:  map[procKey]uint64{},
	}
}

func (s *ProcResourceSource) Query(ctx context.Context) (ResourceSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return ResourceSnapshot{}, err
	}
	now := time.Now()
	elapsed := 0.0
	if !s.prevAt.IsZero() {
		elapsed = now.Sub(s.prevAt).Seconds()
	}

	cpuRaw, err := os.ReadFile("/proc/stat")
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("读取 CPU 数据失败: %w", err)
	}
	aggregate, cores, err := parseCPUStatAll(string(cpuRaw))
	if err != nil {
		return ResourceSnapshot{}, err
	}
	memRaw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("读取内存数据失败: %w", err)
	}
	mem, err := parseMemInfoFull(string(memRaw))
	if err != nil {
		return ResourceSnapshot{}, err
	}

	s.ensureStatic()
	load1, load5, load15 := 0.0, 0.0, 0.0
	if raw, readErr := os.ReadFile("/proc/loadavg"); readErr == nil {
		load1, load5, load15, _ = parseLoadAvg(string(raw))
	}
	uptime := 0.0
	if raw, readErr := os.ReadFile("/proc/uptime"); readErr == nil {
		uptime, _ = parseUptime(string(raw))
	}

	host := s.staticHost
	host.CPUCores = len(cores)
	if host.CPUCores == 0 {
		host.CPUCores = runtime.NumCPU()
	}
	host.MemoryTotal = mem.TotalBytes
	host.SwapTotalBytes = mem.SwapTotalBytes
	host.UptimeSeconds = int64(uptime)
	if uptime > 0 {
		host.BootTime = now.Unix() - int64(uptime)
	}

	cpu := CPUInfo{
		Percent: cpuPercent(s.prevCPU, aggregate, elapsed),
		Cores:   corePercents(cores, s.prevCores, elapsed),
		Load1:   load1, Load5: load5, Load15: load15,
	}
	cpu.FreqMHz, cpu.FreqMaxMHz = s.cpuFreq()
	cpu.TempC = s.cpuTemp()
	cpu.PowerW, cpu.PowerError = s.power.sample(now)

	used := uint64(0)
	if mem.TotalBytes > mem.AvailableBytes {
		used = mem.TotalBytes - mem.AvailableBytes
	}
	memory := MemoryInfo{
		TotalBytes: mem.TotalBytes, UsedBytes: used, AvailableBytes: mem.AvailableBytes,
		FreeBytes: mem.FreeBytes, CachedBytes: mem.CachedBytes, BuffersBytes: mem.BuffersBytes,
		Percent:        percent(used, mem.TotalBytes),
		SwapTotalBytes: mem.SwapTotalBytes, SwapUsedBytes: mem.SwapUsedBytes,
		SwapPercent: percent(mem.SwapUsedBytes, mem.SwapTotalBytes),
	}

	netElapsed, diskElapsed := 0.0, 0.0
	if !s.prevNetAt.IsZero() {
		netElapsed = now.Sub(s.prevNetAt).Seconds()
	}
	if !s.prevDiskAt.IsZero() {
		diskElapsed = now.Sub(s.prevDiskAt).Seconds()
	}
	network, netErr, netRx, netTx := s.readNetwork(now, netElapsed)
	diskIO, diskErr, diskRead, diskWrite := s.readDiskIO(now, diskElapsed)

	root := &Disk{Path: "/", Paths: []string{"/"}}
	if id, total, available, statErr := s.rootStat("/"); statErr != nil {
		root.Error = statErr.Error()
	} else {
		root.ID = id
		root.TotalBytes = total
		root.AvailableBytes = available
		root.Warn = s.warnBytes > 0 && available < s.warnBytes
	}

	if s.nextProcAt.IsZero() || !now.Before(s.nextProcAt) {
		if err := s.refreshProcesses(ctx, now, aggregate.total, len(cores)); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return ResourceSnapshot{}, err
			}
			s.processesError = err.Error()
			// 进程扫描出错也等下一个 10 秒周期再试，避免每 2 秒扫一次 /proc。
			// 不要动 lastProcAt/lastProcCPU：下次成功时仍按两次成功扫描的间隔算 CPU，
			// 否则进程的 jiffies 增量会除以一个更短的窗口，占用率被放大。
			s.nextProcAt = now.Add(processInterval)
		} else {
			s.processesError = ""
			s.nextProcAt = now.Add(processInterval)
		}
	}

	gpuPower, gpuTemp := s.gpuPower()
	powerW := knownPower(cpu.PowerW, gpuPower)
	energy := s.integrateEnergy(now, powerW)

	s.samples++
	s.prevCPU = aggregate
	coresMap := make(map[int]cpuTimes, len(cores))
	for i, value := range cores {
		coresMap[i] = value
	}
	s.prevCores = coresMap
	s.prevAt = now

	snap := ResourceSnapshot{
		At: now.Unix(), Samples: s.samples, ProcessSamples: s.processSamples,
		Host: host, CPU: cpu, Memory: memory,
		Network: network, DiskIO: diskIO, Processes: append([]ProcessInfo(nil), s.processes...),
		GPUPowerW: gpuPower, GPUTempMaxC: gpuTemp, PowerW: powerW, EnergyWh: energy,
		NetRxBps: netRx, NetTxBps: netTx, DiskReadBps: diskRead, DiskWriteBps: diskWrite,
		ProcessesAt: s.processesAt, RootDisk: root, RootDiskAt: now.Unix(),
		NetworkError: netErr, DiskIOError: diskErr, ProcessesError: s.processesError,
	}
	return snap, nil
}

func (s *ProcResourceSource) ensureStatic() {
	if s.staticLoaded {
		return
	}
	s.staticLoaded = true
	host, _ := os.Hostname()
	s.staticHost = HostInfo{Hostname: host}
	if raw, err := os.ReadFile("/etc/os-release"); err == nil {
		s.staticHost.OS = parseOSRelease(string(raw))
	}
	if raw, err := os.ReadFile("/proc/sys/kernel/osrelease"); err == nil {
		s.staticHost.Kernel = strings.TrimSpace(string(raw))
	}
	if raw, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		s.staticHost.CPUModel = parseCPUModel(string(raw))
	}
}

func (s *ProcResourceSource) gpuPower() (*float64, *int) {
	if s.gpuStats == nil {
		return nil, nil
	}
	stats := s.gpuStats()
	var power *float64
	if stats.PowerKnown {
		v := stats.PowerW
		power = &v
	}
	var temp *int
	if stats.TempKnown {
		v := stats.MaxTempC
		temp = &v
	}
	return power, temp
}

func (s *ProcResourceSource) integrateEnergy(now time.Time, power *float64) *float64 {
	if power != nil && !s.lastPowerAt.IsZero() {
		if dt := now.Sub(s.lastPowerAt); dt > 0 && dt <= 3*ResourceInterval {
			s.energyWh += *power * dt.Hours()
			s.energyKnown = true
		}
	}
	s.lastPowerAt = now
	var out *float64
	if s.energyKnown {
		v := s.energyWh
		out = &v
	}
	return out
}

func (s *ProcResourceSource) readNetwork(now time.Time, elapsed float64) ([]NetIface, string, uint64, uint64) {
	raw, err := os.ReadFile("/proc/net/dev")
	if err != nil {
		return nil, "读取网络数据失败: " + err.Error(), 0, 0
	}
	counters := parseNetDev(string(raw))
	entries := make([]NetIface, 0, len(counters))
	next := make(map[string]netCounter, len(counters))
	for _, c := range counters {
		if c.Name == "lo" {
			continue
		}
		prev, seen := s.prevNet[c.Name]
		entries = append(entries, NetIface{
			Name: c.Name, Virtual: virtualNet("/sys/class/net", c.Name), SpeedMbps: s.netSpeed(c.Name, now),
			RxBps: counterRate(seen, prev.RxBytes, c.RxBytes, elapsed), TxBps: counterRate(seen, prev.TxBytes, c.TxBytes, elapsed),
			RxTotal: c.RxBytes, TxTotal: c.TxBytes,
			RxErrors: c.RxErrors, TxErrors: c.TxErrors, RxDrops: c.RxDrops, TxDrops: c.TxDrops,
		})
		next[c.Name] = c
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].RxTotal+entries[i].TxTotal > entries[j].RxTotal+entries[j].TxTotal
	})
	rx, tx := netTotals(entries)
	s.prevNet, s.prevNetAt = next, now
	return entries, "", rx, tx
}

func (s *ProcResourceSource) readDiskIO(now time.Time, elapsed float64) ([]DiskIO, string, uint64, uint64) {
	raw, err := os.ReadFile("/proc/diskstats")
	if err != nil {
		return nil, "读取磁盘 I/O 失败: " + err.Error(), 0, 0
	}
	all := parseDiskStats(string(raw))
	counters := selectDiskIO(all, "/sys/block")
	entries := make([]DiskIO, 0, len(counters))
	next := make(map[string]diskCounter, len(counters))
	for _, c := range counters {
		prev, seen := s.prevDisk[c.Name]
		busy := 0
		if seen && elapsed > 0 && c.IOTicks >= prev.IOTicks {
			// IOTicks 是设备有请求在途的毫秒数（diskstats 第 13 列），和 iostat 的 %util 同口径；
			// 两次读数和墙钟有少量错位，钳到 0–100%。
			busy = int(math.Round(float64(c.IOTicks-prev.IOTicks) / (elapsed * 1000) * 100))
			busy = max(0, min(100, busy))
		}
		entries = append(entries, DiskIO{
			Name:      c.Name,
			ReadBps:   counterRate(seen, prev.ReadSectors, c.ReadSectors, elapsed) * bytesPerSector,
			WriteBps:  counterRate(seen, prev.WriteSectors, c.WriteSectors, elapsed) * bytesPerSector,
			ReadTotal: c.ReadSectors * bytesPerSector, WriteTotal: c.WriteSectors * bytesPerSector,
			Busy: busy,
		})
		next[c.Name] = c
	}
	// 按开机以来的累计读写排，常用的盘在前；按当前速率排的话，列表每次轮询都会跳行。
	sort.SliceStable(entries, func(i, j int) bool {
		a, b := entries[i].ReadTotal+entries[i].WriteTotal, entries[j].ReadTotal+entries[j].WriteTotal
		if a == b {
			return entries[i].Name < entries[j].Name
		}
		return a > b
	})
	var readBps, writeBps uint64
	for _, entry := range entries {
		readBps += entry.ReadBps
		writeBps += entry.WriteBps
	}
	s.prevDisk, s.prevDiskAt = next, now
	return entries, "", readBps, writeBps
}

func (s *ProcResourceSource) cpuFreq() (*float64, *float64) {
	// 空结果时每次重扫：cpufreq 驱动可能在服务启动后才加载，扫一次就定死会永远显示 —。
	if !s.freqScanned || len(s.freqPaths) == 0 {
		s.freqScanned = true
		s.freqPaths, _ = filepath.Glob("/sys/devices/system/cpu/cpu[0-9]*/cpufreq/scaling_cur_freq")
	}
	if len(s.freqPaths) == 0 {
		return nil, nil
	}
	values := make([]int, 0, len(s.freqPaths))
	for _, path := range s.freqPaths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
		if err == nil && v > 0 {
			values = append(values, v)
		}
	}
	avg, maxMHz, ok := freqStats(values)
	if !ok {
		return nil, nil
	}
	return &avg, &maxMHz
}

// cpuTemp 返回各颗 CPU 封装温度里最高的一个：多路服务器只看第一颗会漏掉更热的那颗。
func (s *ProcResourceSource) cpuTemp() *float64 {
	if !s.tempScanned {
		s.tempScanned = true
		s.tempPaths = discoverCPUTemps("/sys/class/hwmon")
	}
	best, found := 0.0, false
	for _, path := range s.tempPaths {
		raw, err := os.ReadFile(path)
		if err != nil {
			// 驱动重载后 hwmon 编号可能变了，下一轮重新找
			s.tempScanned = false
			continue
		}
		if v, ok := parseHwmonTemp(string(raw)); ok && (!found || v > best) {
			best, found = v, true
		}
	}
	if !found {
		return nil
	}
	return &best
}

func (s *ProcResourceSource) refreshProcesses(ctx context.Context, now time.Time, totalCPU uint64, cores int) error {
	pids := procfs.PIDs()
	if len(pids) == 0 {
		return errors.New("读取进程列表失败")
	}
	pids = samplePIDs(pids, maxScannedProcesses)
	var totalDelta uint64
	if !s.lastProcAt.IsZero() && totalCPU > s.lastProcCPU {
		totalDelta = totalCPU - s.lastProcCPU
	}
	entries := make([]procEntry, 0, len(pids))
	ticks := make(map[procKey]uint64, len(pids))
	pageSize := uint64(os.Getpagesize())
	for i, pid := range pids {
		if i%128 == 0 {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		st, err := procfs.ReadStat(pid)
		if err != nil || st.State == 'Z' || st.State == 'X' {
			continue
		}
		// stat 里已经带了 RSS（页数），不必再读 /proc/<pid>/statm。
		rss := st.RSSPages * pageSize
		key := procKey{pid: pid, start: st.StartTime}
		totalTicks := st.UTime + st.STime
		cpu := 0.0
		if totalDelta > 0 {
			if prev, ok := s.prevProc[key]; ok && totalTicks >= prev {
				cpu = procCPUPercent(totalTicks-prev, totalDelta, cores)
			}
		}
		entries = append(entries, procEntry{pid: pid, start: st.StartTime, comm: st.Comm, cpu: cpu, rss: rss})
		ticks[key] = totalTicks
	}

	byCPU := append([]procEntry(nil), entries...)
	sort.Slice(byCPU, func(i, j int) bool {
		if byCPU[i].cpu == byCPU[j].cpu {
			return byCPU[i].rss > byCPU[j].rss
		}
		return byCPU[i].cpu > byCPU[j].cpu
	})
	byMem := append([]procEntry(nil), entries...)
	sort.Slice(byMem, func(i, j int) bool { return byMem[i].rss > byMem[j].rss })

	selected := make(map[procKey]procEntry, maxTopProcesses)
	for i := 0; i < len(byCPU) && i < maxTopProcesses/2; i++ {
		selected[procKey{pid: byCPU[i].pid, start: byCPU[i].start}] = byCPU[i]
	}
	for i := 0; i < len(byMem) && i < maxTopProcesses/2; i++ {
		selected[procKey{pid: byMem[i].pid, start: byMem[i].start}] = byMem[i]
	}
	out := make([]ProcessInfo, 0, len(selected))
	for _, entry := range selected {
		command := entry.comm
		if raw, err := os.ReadFile("/proc/" + strconv.Itoa(entry.pid) + "/cmdline"); err == nil {
			if cmd := parseCmdline(raw); cmd != "" {
				command = cmd
			}
		}
		out = append(out, ProcessInfo{
			PID: entry.pid, User: procfs.Owner(entry.pid), Command: command,
			CPUPercent: entry.cpu, RSSBytes: entry.rss, JobID: procfs.JobOf(entry.pid, s.runRoot),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CPUPercent == out[j].CPUPercent {
			return out[i].RSSBytes > out[j].RSSBytes
		}
		return out[i].CPUPercent > out[j].CPUPercent
	})
	s.processes = out
	s.processesAt = now.Unix()
	s.processSamples++
	s.lastProcAt = now
	s.lastProcCPU = totalCPU
	s.prevProc = ticks
	return nil
}

func cpuPercent(prev, cur cpuTimes, elapsed float64) *int {
	if elapsed <= 0 || cur.total <= prev.total || cur.idle < prev.idle {
		return nil
	}
	total := cur.total - prev.total
	idle := cur.idle - prev.idle
	if total == 0 {
		return nil
	}
	v := int(math.Round(float64(total-idle) / float64(total) * 100))
	v = max(0, min(100, v))
	return &v
}

func percent(used, total uint64) int {
	if total == 0 {
		return 0
	}
	return max(0, min(100, int(math.Round(float64(used)/float64(total)*100))))
}

const netSpeedTTL = time.Minute

func (s *ProcResourceSource) netSpeed(name string, now time.Time) int {
	if at, ok := s.speedsAt[name]; ok && now.Sub(at) < netSpeedTTL {
		return s.speeds[name]
	}
	v := readNetSpeed(name)
	if s.speeds == nil {
		s.speedsAt = map[string]time.Time{}
		s.speeds = map[string]int{}
	}
	s.speedsAt[name] = now
	s.speeds[name] = v
	return v
}

func readNetSpeed(name string) int {
	raw, err := os.ReadFile("/sys/class/net/" + name + "/speed")
	if err != nil {
		return 0
	}
	// 无线和部分虚拟网卡读出来是 -1 或很大的值，只接受合理的正数。
	v, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil || v <= 0 || v > 1_000_000 {
		return 0
	}
	return v
}

type powerCounter struct {
	path    string
	prev    uint64
	max     uint64
	prevAt  time.Time
	hasPrev bool
}

type powerMeter struct {
	scanned  bool
	counters []powerCounter
}

// sample 返回 CPU 封装功率。读不到时第二个返回值写明原因；只是还在等第二次读数时为空。
func (p *powerMeter) sample(now time.Time) (*float64, string) {
	if !p.scanned {
		p.scanned = true
		p.discover()
	}
	if len(p.counters) == 0 {
		return nil, "这台机器没有 RAPL 或 amd_energy 能量计数器"
	}
	total := 0.0
	known := false
	var readErr error
	for i := range p.counters {
		c := &p.counters[i]
		raw, err := os.ReadFile(c.path)
		if err != nil {
			c.hasPrev = false
			readErr = err
			continue
		}
		cur, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil {
			c.hasPrev = false
			readErr = err
			continue
		}
		if c.hasPrev && !c.prevAt.IsZero() {
			if seconds := now.Sub(c.prevAt).Seconds(); seconds > 0 {
				total += raplWatts(c.prev, cur, c.max, seconds)
				known = true
			}
		}
		c.prev, c.prevAt, c.hasPrev = cur, now, true
	}
	if known {
		return &total, ""
	}
	if readErr != nil {
		return nil, powerReadError(readErr)
	}
	return nil, ""
}

func (p *powerMeter) discover() {
	if dirs, _ := filepath.Glob("/sys/class/powercap/intel-rapl:*"); len(dirs) > 0 {
		for _, dir := range dirs {
			if strings.Count(filepath.Base(dir), ":") != 1 {
				continue // 跳过 core/uncore 等子域
			}
			name, _ := os.ReadFile(filepath.Join(dir, "name"))
			if !strings.HasPrefix(strings.TrimSpace(string(name)), "package-") {
				continue
			}
			max := uint64(0)
			if raw, err := os.ReadFile(filepath.Join(dir, "max_energy_range_uj")); err == nil {
				max, _ = strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64)
			}
			p.counters = append(p.counters, powerCounter{path: filepath.Join(dir, "energy_uj"), max: max})
		}
	}
	if len(p.counters) > 0 {
		return
	}
	dirs, _ := filepath.Glob("/sys/class/hwmon/hwmon*")
	for _, dir := range dirs {
		name, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil || strings.TrimSpace(string(name)) != "amd_energy" {
			continue
		}
		for _, input := range amdEnergySocketInputs(dir) {
			p.counters = append(p.counters, powerCounter{path: input})
		}
	}
}

func knownPower(values ...*float64) *float64 {
	total := 0.0
	known := false
	for _, v := range values {
		if v != nil {
			total += *v
			known = true
		}
	}
	if !known {
		return nil
	}
	return &total
}
