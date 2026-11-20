package system

import (
	"bufio"
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type cpuTimes struct {
	total uint64
	idle  uint64
}

func parseCPUStat(input string) (cpuTimes, error) {
	aggregate, _, err := parseCPUStatAll(input)
	return aggregate, err
}

// parseCPUStatAll 解析总体和每个核的 CPU 时间。返回的 cores 按 cpu0、cpu1… 的顺序排列。
func parseCPUStatAll(input string) (cpuTimes, []cpuTimes, error) {
	var aggregate cpuTimes
	var cores []cpuTimes
	found := false
	s := bufio.NewScanner(strings.NewReader(input))
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if !strings.HasPrefix(line, "cpu") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 5 {
			continue
		}
		times, err := parseCPUTimes(f[1:])
		if err != nil {
			return cpuTimes{}, nil, fmt.Errorf("无法解析 /proc/stat %q: %w", f[0], err)
		}
		if f[0] == "cpu" {
			aggregate, found = times, true
			continue
		}
		// 只要 cpuN，忽略 guest 等其它行。
		if _, err := strconv.Atoi(strings.TrimPrefix(f[0], "cpu")); err == nil {
			cores = append(cores, times)
		}
	}
	if err := s.Err(); err != nil {
		return cpuTimes{}, nil, err
	}
	if !found {
		return cpuTimes{}, nil, errors.New("/proc/stat 中没有 CPU 数据")
	}
	return aggregate, cores, nil
}

func parseCPUTimes(fields []string) (cpuTimes, error) {
	var times cpuTimes
	for i, field := range fields {
		// guest 和 guest_nice 已经包含在 user 和 nice 中。
		if i >= 8 {
			break
		}
		v, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("无法解析 CPU 计数: %w", err)
		}
		times.total += v
		if i == 3 || i == 4 { // idle plus iowait
			times.idle += v
		}
	}
	return times, nil
}

type memInfo struct {
	TotalBytes     uint64
	AvailableBytes uint64
	FreeBytes      uint64
	CachedBytes    uint64
	BuffersBytes   uint64
	SwapTotalBytes uint64
	SwapUsedBytes  uint64
}

func parseMemInfo(input string) (totalBytes, availableBytes uint64, err error) {
	info, err := parseMemInfoFull(input)
	return info.TotalBytes, info.AvailableBytes, err
}

func parseMemInfoFull(input string) (memInfo, error) {
	values := map[string]uint64{}
	s := bufio.NewScanner(strings.NewReader(input))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 2 {
			continue
		}
		key := strings.TrimSuffix(f[0], ":")
		switch key {
		case "MemTotal", "MemAvailable", "MemFree", "Buffers", "Cached", "SReclaimable", "Shmem", "SwapTotal", "SwapFree":
		default:
			continue
		}
		v, parseErr := strconv.ParseUint(f[1], 10, 64)
		if parseErr != nil {
			return memInfo{}, fmt.Errorf("无法解析 /proc/meminfo 的 %s: %w", key, parseErr)
		}
		values[key] = v
	}
	if scanErr := s.Err(); scanErr != nil {
		return memInfo{}, scanErr
	}
	if values["MemTotal"] == 0 {
		return memInfo{}, errors.New("/proc/meminfo 中没有 MemTotal")
	}
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"] + values["Buffers"] + values["Cached"] + values["SReclaimable"]
		if available > values["Shmem"] {
			available -= values["Shmem"]
		} else {
			available = 0
		}
	}
	available = min(available, values["MemTotal"])
	swapUsed := uint64(0)
	if values["SwapTotal"] > values["SwapFree"] {
		swapUsed = values["SwapTotal"] - values["SwapFree"]
	}
	return memInfo{
		TotalBytes:     values["MemTotal"] * 1024,
		AvailableBytes: available * 1024,
		FreeBytes:      values["MemFree"] * 1024,
		// 缓存里包含可回收的 slab，页面上的“缓存”更完整；它和 available 有重叠，不用于计算 used。
		CachedBytes:    (values["Cached"] + values["SReclaimable"]) * 1024,
		BuffersBytes:   values["Buffers"] * 1024,
		SwapTotalBytes: values["SwapTotal"] * 1024,
		SwapUsedBytes:  swapUsed * 1024,
	}, nil
}

func parseLoadAvg(input string) (load1, load5, load15 float64, err error) {
	f := strings.Fields(input)
	if len(f) < 3 {
		return 0, 0, 0, errors.New("/proc/loadavg 格式错误")
	}
	values := []*float64{&load1, &load5, &load15}
	for i := range values {
		v, parseErr := strconv.ParseFloat(f[i], 64)
		if parseErr != nil {
			return 0, 0, 0, fmt.Errorf("无法解析 /proc/loadavg: %w", parseErr)
		}
		*values[i] = v
	}
	return load1, load5, load15, nil
}

func parseUptime(input string) (float64, error) {
	f := strings.Fields(input)
	if len(f) == 0 {
		return 0, errors.New("/proc/uptime 格式错误")
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0, fmt.Errorf("无法解析 /proc/uptime: %w", err)
	}
	return v, nil
}

const bytesPerSector = 512

type netCounter struct {
	Name     string
	RxBytes  uint64
	TxBytes  uint64
	RxErrors uint64
	TxErrors uint64
	RxDrops  uint64
	TxDrops  uint64
}

func parseNetDev(input string) []netCounter {
	var out []netCounter
	s := bufio.NewScanner(strings.NewReader(input))
	for s.Scan() {
		name, rest, ok := strings.Cut(s.Text(), ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if name == "" || len(f) < 16 {
			continue
		}
		rxBytes, err1 := strconv.ParseUint(f[0], 10, 64)
		rxErrors, err2 := strconv.ParseUint(f[2], 10, 64)
		rxDrops, err3 := strconv.ParseUint(f[3], 10, 64)
		txBytes, err4 := strconv.ParseUint(f[8], 10, 64)
		txErrors, err5 := strconv.ParseUint(f[10], 10, 64)
		txDrops, err6 := strconv.ParseUint(f[11], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || err6 != nil {
			continue
		}
		out = append(out, netCounter{Name: name, RxBytes: rxBytes, TxBytes: txBytes,
			RxErrors: rxErrors, TxErrors: txErrors, RxDrops: rxDrops, TxDrops: txDrops})
	}
	return out
}

type diskCounter struct {
	Name         string
	ReadSectors  uint64
	WriteSectors uint64
	IOTicks      uint64
}

func parseDiskStats(input string) []diskCounter {
	var out []diskCounter
	s := bufio.NewScanner(strings.NewReader(input))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 14 {
			continue
		}
		name := f[2]
		read, err1 := strconv.ParseUint(f[5], 10, 64)
		write, err2 := strconv.ParseUint(f[9], 10, 64)
		ticks, err3 := strconv.ParseUint(f[12], 10, 64)
		if err1 != nil || err2 != nil || err3 != nil {
			continue
		}
		out = append(out, diskCounter{Name: name, ReadSectors: read, WriteSectors: write, IOTicks: ticks})
	}
	return out
}

// rate 把累计计数换算成每秒速率。计数器变小说明设备重启或计数器回绕，当作 0。
func rate(prev, cur uint64, seconds float64) uint64 {
	if seconds <= 0 || cur < prev {
		return 0
	}
	return uint64(math.Round(float64(cur-prev) / seconds))
}

// counterRate 在“这一轮才第一次见到这个名字”时返回 0。
// 新出现的网卡（docker0、veth、热插拔）和新设备只有开机以来的累计值；
// 没有基准却当成一次增量，会画出每秒几十 MB 的假尖峰，还会污染历史峰值。
func counterRate(seen bool, prev, cur uint64, seconds float64) uint64 {
	if !seen {
		return 0
	}
	return rate(prev, cur, seconds)
}

// samplePIDs 把进程列表数值排序后均匀抽样到 max 个。/proc 的目录条目是字典序
// （"1","10","100"…），直接截前缀会系统性漏掉高 pid（往往是新起的训练进程）。
func samplePIDs(pids []int, max int) []int {
	sort.Ints(pids)
	if max <= 0 || len(pids) <= max {
		return pids
	}
	out := make([]int, 0, max)
	for i := 0; i < max; i++ {
		out = append(out, pids[i*len(pids)/max])
	}
	return out
}

// procCPUPercent 把一个进程的 jiffies 增量换算成单核百分比；多线程进程可以超过 100。
func procCPUPercent(delta, total uint64, cores int) float64 {
	if delta == 0 || total == 0 || cores <= 0 {
		return 0
	}
	v := float64(delta) / (float64(total) / float64(cores)) * 100
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return 0
	}
	return math.Round(v*10) / 10
}

func parseCmdline(input []byte) string {
	parts := strings.Split(string(input), "\x00")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part != "" {
			out = append(out, part)
		}
	}
	return truncateRunes(strings.Join(out, " "), 160)
}

func truncateRunes(s string, limit int) string {
	r := []rune(s)
	if limit <= 0 || len(r) <= limit {
		return s
	}
	if limit == 1 {
		return "…"
	}
	return string(r[:limit-1]) + "…"
}

func freqStats(values []int) (avg, maxMHz float64, ok bool) {
	if len(values) == 0 {
		return 0, 0, false
	}
	total := 0
	count := 0
	maxValue := 0
	for _, v := range values {
		if v <= 0 {
			continue
		}
		total += v
		count++
		if v > maxValue {
			maxValue = v
		}
	}
	if count == 0 {
		return 0, 0, false
	}
	// sysfs 的 scaling_cur_freq 单位是 kHz。
	return float64(total) / 1000 / float64(count), float64(maxValue) / 1000, true
}

// raplWatts 用两次能量计数（微焦）计算平均功率；max 是计数器回绕上限，0 表示未知。
func raplWatts(prev, cur, max uint64, seconds float64) float64 {
	if seconds <= 0 {
		return 0
	}
	if cur < prev {
		if max > 0 && max >= prev {
			return float64(max-prev+cur) / 1e6 / seconds
		}
		return 0
	}
	return float64(cur-prev) / 1e6 / seconds
}

func parseHwmonTemp(input string) (float64, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(input), 64)
	if err != nil || v <= 0 {
		return 0, false
	}
	return v / 1000, true
}

func parseCPUModel(input string) string {
	s := bufio.NewScanner(strings.NewReader(input))
	for s.Scan() {
		key, value, ok := strings.Cut(s.Text(), ":")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "model name" || key == "Hardware" || key == "Processor" {
			if v := strings.TrimSpace(value); v != "" {
				return v
			}
		}
	}
	return ""
}

func parseOSRelease(input string) string {
	s := bufio.NewScanner(strings.NewReader(input))
	for s.Scan() {
		key, value, ok := strings.Cut(s.Text(), "=")
		if !ok || key != "PRETTY_NAME" {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return ""
}
