package system

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseCPUStatAll(t *testing.T) {
	input := "cpu  100 20 30 400 50 6 7 8 900 100\ncpu0 1 2 3 4 5 6 7 8\ncpu1 10 20 30 40 50 60 70 80\nintr 1\n"
	aggregate, cores, err := parseCPUStatAll(input)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.total != 621 || aggregate.idle != 450 {
		t.Fatalf("总体解析错误: %+v", aggregate)
	}
	if len(cores) != 2 || cores[0].total != 36 || cores[1].total != 360 {
		t.Fatalf("每核解析错误: %+v", cores)
	}
}

func TestParseMemInfoFull(t *testing.T) {
	input := "MemTotal: 1000 kB\nMemAvailable: 400 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 200 kB\nSReclaimable: 30 kB\nShmem: 20 kB\nSwapTotal: 800 kB\nSwapFree: 300 kB\n"
	info, err := parseMemInfoFull(input)
	if err != nil {
		t.Fatal(err)
	}
	if info.TotalBytes != 1000*1024 || info.AvailableBytes != 400*1024 || info.FreeBytes != 100*1024 ||
		info.CachedBytes != 230*1024 || info.BuffersBytes != 50*1024 ||
		info.SwapTotalBytes != 800*1024 || info.SwapUsedBytes != 500*1024 {
		t.Fatalf("meminfo 解析错误: %+v", info)
	}
}

func TestParseLoadUptime(t *testing.T) {
	l1, l5, l15, err := parseLoadAvg("1.20 2.30 3.40 1/123 4567\n")
	if err != nil || l1 != 1.2 || l5 != 2.3 || l15 != 3.4 {
		t.Fatalf("loadavg: %v %v %v %v", l1, l5, l15, err)
	}
	up, err := parseUptime("1234.50 678.00\n")
	if err != nil || up != 1234.5 {
		t.Fatalf("uptime: %v %v", up, err)
	}
}

func TestParseNetDev(t *testing.T) {
	input := "Inter-|   Receive                                                |  Transmit\n" +
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n" +
		"    lo: 100 1 0 0 0 0 0 0 100 1 0 0 0 0 0 0\n" +
		"  eth0: 2000 3 4 5 0 0 0 0 3000 6 7 8 0 0 0 0\n"
	got := parseNetDev(input)
	if len(got) != 2 || got[1].Name != "eth0" || got[1].RxBytes != 2000 || got[1].TxBytes != 3000 ||
		got[1].RxErrors != 4 || got[1].TxErrors != 7 || got[1].RxDrops != 5 || got[1].TxDrops != 8 {
		t.Fatalf("net/dev 解析错误: %+v", got)
	}
}

func TestParseDiskStats(t *testing.T) {
	input := "   8       0 sda 1 0 100 0 2 0 200 0 0 500 0\n" +
		" 259       0 nvme0n1 3 0 300 0 4 0 400 0 0 700 0\n"
	got := parseDiskStats(input)
	if len(got) != 2 || got[1].Name != "nvme0n1" || got[1].ReadSectors != 300 ||
		got[1].WriteSectors != 400 || got[1].IOTicks != 700 {
		t.Fatalf("diskstats 解析错误: %+v", got)
	}
}

func TestRateAndProcessCPU(t *testing.T) {
	if got := rate(100, 300, 2); got != 100 {
		t.Fatalf("rate = %d", got)
	}
	if got := rate(300, 100, 2); got != 0 {
		t.Fatalf("计数回绕应返回 0: %d", got)
	}
	if got := rate(0, 10, 0); got != 0 {
		t.Fatalf("间隔为 0 应返回 0: %d", got)
	}
	// 新出现的网卡/设备只有累计值，第一次见到时不能当增量
	if got := counterRate(false, 0, 10_000_000_000, 2); got != 0 {
		t.Fatalf("首次见到的计数器应返回 0: %d", got)
	}
	if got := counterRate(true, 1000, 3000, 2); got != 1000 {
		t.Fatalf("counterRate = %d", got)
	}
	if got := procCPUPercent(50, 400, 4); got != 50 {
		t.Fatalf("进程 CPU = %v", got)
	}
	if got := procCPUPercent(0, 400, 4); got != 0 {
		t.Fatalf("增量为 0 应返回 0: %v", got)
	}
}

func TestParseProcDetails(t *testing.T) {
	cmd := parseCmdline([]byte("python\x00train.py\x00--lr\x001e-4\x00"))
	if cmd != "python train.py --lr 1e-4" {
		t.Fatalf("cmdline = %q", cmd)
	}
	long := strings.Repeat("a", 200)
	if got := truncateRunes(long, 10); len([]rune(got)) != 10 || !strings.HasSuffix(got, "…") {
		t.Fatalf("截断错误: %q", got)
	}
}

func TestFreqStats(t *testing.T) {
	avg, maxMHz, ok := freqStats([]int{2000000, 3000000, 0})
	if !ok || avg != 2500 || maxMHz != 3000 {
		t.Fatalf("freqStats = %v %v %v", avg, maxMHz, ok)
	}
	if _, _, ok := freqStats([]int{0, 0}); ok {
		t.Fatal("没有有效值时应返回 false")
	}
}

func TestRaplWattsAndTemp(t *testing.T) {
	if got := raplWatts(1000000, 3000000, 0, 2); got != 1 {
		t.Fatalf("raplWatts = %v", got)
	}
	if got := raplWatts(900, 100, 1000, 1); got != 200.0/1e6 {
		t.Fatalf("回绕计算 = %v", got)
	}
	if temp, ok := parseHwmonTemp("58000\n"); !ok || temp != 58 {
		t.Fatalf("温度 = %v %v", temp, ok)
	}
	if _, ok := parseHwmonTemp("0\n"); ok {
		t.Fatal("0 应视为不可用")
	}
}

func TestParseHostStatic(t *testing.T) {
	if got := parseCPUModel("processor\t: 0\nmodel name\t: AMD EPYC 7543\n"); got != "AMD EPYC 7543" {
		t.Fatalf("CPU 型号 = %q", got)
	}
	if got := parseOSRelease("NAME=\"Ubuntu\"\nPRETTY_NAME=\"Ubuntu 22.04.5 LTS\"\n"); got != "Ubuntu 22.04.5 LTS" {
		t.Fatalf("系统版本 = %q", got)
	}
}

func TestResourceDownsampleAndPeaks(t *testing.T) {
	points := make([]ResourcePoint, 400)
	for i := range points {
		v := i % 101
		points[i] = ResourcePoint{At: int64(i), CPUPercent: &v, MemoryPercent: i % 100, SwapPercent: 1,
			NetRxBps: uint64(i), NetTxBps: 2, DiskReadBps: 3, DiskWriteBps: 4}
	}
	got := downsampleResource(points, 180)
	if len(got) > 180 || len(got) == 0 || got[0].At > got[len(got)-1].At {
		t.Fatalf("降采样结果不合适: len=%d", len(got))
	}
	peaks := resourcePeaks(points)
	if peaks.CPUPercent == nil || *peaks.CPUPercent != 100 || peaks.NetRxBps != 399 || peaks.MemoryPercent != 99 {
		t.Fatalf("峰值错误: %+v", peaks)
	}
}

func TestResourceMonitorHistoryAndClone(t *testing.T) {
	src := &resourceSequence{}
	mon := NewResourceMonitor(src)
	for i := 0; i < 4; i++ {
		mon.Refresh(context.Background())
	}
	points, peaks, from, to := mon.History(ResourceHistory)
	if len(points) != 4 || peaks.CPUPercent == nil || *peaks.CPUPercent != 4 || !from.Before(to) {
		t.Fatalf("历史错误: points=%d peaks=%+v from=%v to=%v", len(points), peaks, from, to)
	}
	snap, _, err := mon.Latest()
	if err != nil {
		t.Fatal(err)
	}
	if snap.CPU.Cores == nil {
		t.Fatal("快照应包含 cores")
	}
	snap.CPU.Cores[0] = 99
	if next, _, _ := mon.Latest(); next.CPU.Cores[0] == 99 {
		t.Fatal("修改 Latest 返回值不应影响缓存")
	}
}

type resourceSequence struct{ n int }

func (s *resourceSequence) Query(context.Context) (ResourceSnapshot, error) {
	s.n++
	v := s.n
	at := time.Now().Add(-time.Duration(10-s.n) * time.Second).Unix()
	return ResourceSnapshot{
		At: at, Samples: s.n,
		CPU:       CPUInfo{Percent: &v, Cores: []int{v, -1}},
		Memory:    MemoryInfo{Percent: 10 + s.n, SwapPercent: 0},
		Network:   []NetIface{{Name: "eth0"}},
		DiskIO:    []DiskIO{{Name: "sda"}},
		Processes: []ProcessInfo{{PID: 1, Command: "x"}},
		RootDisk:  &Disk{Path: "/", Paths: []string{"/"}},
	}, nil
}

// 历史只留最近 1 小时：进入稳定状态后每 2 秒都要裁一次，裁完顺序不能乱。
func TestResourceMonitorTrimsOldPoints(t *testing.T) {
	now := time.Now()
	ats := []int64{
		now.Add(-2 * time.Hour).Unix(),
		now.Add(-90 * time.Minute).Unix(),
		now.Add(-3580 * time.Second).Unix(),
		now.Add(-30 * time.Minute).Unix(),
		now.Add(-2 * time.Second).Unix(),
	}
	src := &resourceTimeline{ats: ats}
	mon := NewResourceMonitor(src)
	for range ats {
		mon.Refresh(context.Background())
	}
	points, _, _, _ := mon.History(ResourceHistory)
	if len(points) != 3 {
		t.Fatalf("应该只剩最近 1 小时的 3 个点，实际 %d: %+v", len(points), points)
	}
	for i := 1; i < len(points); i++ {
		if points[i].At <= points[i-1].At {
			t.Fatalf("裁剪后时间顺序乱了: %+v", points)
		}
	}
	// 裁剪用的是原地搬移，后续追加不能把已经在切片里的点覆盖掉
	mon.record(ResourceSnapshot{At: now.Unix(), Memory: MemoryInfo{Percent: 7}})
	points, _, _, _ = mon.History(ResourceHistory)
	if len(points) != 4 || points[3].MemoryPercent != 7 {
		t.Fatalf("追加后的历史不对: %+v", points)
	}
}

type resourceTimeline struct {
	ats []int64
	n   int
}

func (s *resourceTimeline) Query(context.Context) (ResourceSnapshot, error) {
	at := s.ats[s.n]
	s.n++
	return ResourceSnapshot{At: at, Samples: s.n, CPU: CPUInfo{Cores: []int{}}}, nil
}

// 采集失败时缓存不能被零值覆盖：接口会带着新鲜时间戳返回上一份好快照 + error，
// 前端降级显示旧值，而不是把"内存 0%"当成活数据。
func TestResourceMonitorKeepsLastGoodOnError(t *testing.T) {
	mon := NewResourceMonitor(&resourceFailAfter{failAfter: 1})
	mon.Refresh(context.Background()) // 成功
	mon.Refresh(context.Background()) // 失败
	snap, _, err := mon.Latest()
	if err == nil {
		t.Fatal("第二次采集失败应返回错误")
	}
	if snap.Memory.Percent != 42 || snap.CPU.Percent == nil || *snap.CPU.Percent != 1 {
		t.Fatalf("应保留上一份好快照，实际: %+v", snap)
	}
}

// 自身占用进历史：注入 SelfBytes 时写点，未注入或统计不可用时为 null（不画假 0）。
func TestResourceSelfBytesHistory(t *testing.T) {
	mon := NewResourceMonitor(&resourceSequence{})
	mon.Refresh(context.Background())
	points, _, _, _ := mon.History(ResourceHistory)
	if len(points) != 1 || points[0].DataBytes != nil || points[0].DBBytes != nil {
		t.Fatalf("未注入 SelfBytes 时占用应为 null: %+v", points)
	}

	data, db := uint64(1000), uint64(200)
	mon.SelfBytes = func() (uint64, uint64, bool) { return data, db, true }
	mon.Refresh(context.Background())
	data, db = 2000, 400
	mon.Refresh(context.Background())
	points, peaks, _, _ := mon.History(ResourceHistory)
	if len(points) != 3 || points[1].DataBytes == nil || *points[1].DataBytes != 1000 ||
		points[2].DBBytes == nil || *points[2].DBBytes != 400 {
		t.Fatalf("占用没有写进历史: %+v", points)
	}
	if peaks.DataBytes == nil || *peaks.DataBytes != 2000 || peaks.DBBytes == nil || *peaks.DBBytes != 400 {
		t.Fatalf("占用峰值错误: %+v", peaks)
	}

	mon.SelfBytes = func() (uint64, uint64, bool) { return 0, 0, false }
	mon.Refresh(context.Background())
	points, _, _, _ = mon.History(ResourceHistory)
	if points[3].DataBytes != nil || points[3].DBBytes != nil {
		t.Fatalf("统计不可用时应为 null: %+v", points[3])
	}
}

// 降采样对占用取非 null 的平均；峰值取最大；全 null 的桶保持 null。
func TestResourceSelfBytesDownsample(t *testing.T) {
	points := make([]ResourcePoint, 400)
	for i := range points {
		points[i] = ResourcePoint{At: int64(i)}
		if i%2 == 0 {
			d, b := uint64(i), uint64(2*i)
			points[i].DataBytes, points[i].DBBytes = &d, &b
		}
	}
	got := downsampleResource(points, 100)
	if len(got) != 100 {
		t.Fatalf("降采样点数不对: %d", len(got))
	}
	for _, p := range got {
		if p.DataBytes == nil || p.DBBytes == nil {
			t.Fatalf("桶内有值时平均不应为 null: %+v", p)
		}
	}
	peaks := resourcePeaks(points)
	if peaks.DataBytes == nil || *peaks.DataBytes != 398 || peaks.DBBytes == nil || *peaks.DBBytes != 796 {
		t.Fatalf("占用峰值错误: %+v", peaks)
	}
	for _, p := range downsampleResource(make([]ResourcePoint, 400), 100) {
		if p.DataBytes != nil || p.DBBytes != nil {
			t.Fatalf("全 null 的桶应保持 null: %+v", p)
		}
	}
}

type resourceFailAfter struct {
	n         int
	failAfter int
}

func (s *resourceFailAfter) Query(context.Context) (ResourceSnapshot, error) {
	s.n++
	if s.n > s.failAfter {
		return ResourceSnapshot{}, errors.New("读取 CPU 数据失败: mock")
	}
	v := s.n
	return ResourceSnapshot{At: time.Now().Unix(), Samples: s.n, CPU: CPUInfo{Percent: &v, Cores: []int{v}}, Memory: MemoryInfo{Percent: 42}}, nil
}

// amd_energy 的 energy*_input 同时暴露每核（EcoreX）和每插座（EsocketX），
// 插座已含所有核，只能取插座，否则 CPU 功耗约翻倍。
func TestAmdEnergySocketOnly(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("energy1_input", "100")
	write("energy1_label", "Ecore0")
	write("energy2_input", "200")
	write("energy2_label", "Ecore1")
	write("energy3_input", "300")
	write("energy3_label", "Esocket0")
	got := amdEnergySocketInputs(dir)
	if len(got) != 1 || filepath.Base(got[0]) != "energy3_input" {
		t.Fatalf("只应取插座计数器，实际: %v", got)
	}
	// 没有 label 时宁可不出数，也不能把每核当插座
	empty := t.TempDir()
	if err := os.WriteFile(filepath.Join(empty, "energy1_input"), []byte("1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := amdEnergySocketInputs(empty); len(got) != 0 {
		t.Fatalf("无 label 应返回空: %v", got)
	}
}

// /proc 目录是字典序，截前缀会系统性漏掉高 pid；均匀抽样要覆盖整个 pid 范围。
func TestSamplePIDs(t *testing.T) {
	pids := make([]int, 10000)
	for i := range pids {
		pids[i] = 10000 - i // 逆序输入
	}
	got := samplePIDs(pids, 2048)
	if len(got) != 2048 {
		t.Fatalf("抽样数量 = %d", len(got))
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("抽样后应数值升序: %v", got[:10])
		}
	}
	if got[0] > 100 || got[len(got)-1] < 9000 {
		t.Fatalf("抽样应覆盖低/高 pid，实际首尾: %d %d", got[0], got[len(got)-1])
	}
	if small := samplePIDs([]int{3, 1, 2}, 2048); len(small) != 3 || small[0] != 1 {
		t.Fatalf("不足上限应原样排序返回: %v", small)
	}
}

func mkdirs(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(filepath.Join(root, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func diskNames(ds []diskCounter) map[string]bool {
	out := map[string]bool{}
	for _, d := range ds {
		out[d.Name] = true
	}
	return out
}

// 只统计物理整盘：LVM/md 的成员是分区时（Ubuntu 默认 LVM 装在 nvme0n1p3 上），
// 显示 dm-0 会和整盘 nvme0n1 重复相加。
func TestSelectDiskIO(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root,
		"sda/device", "sdb/device", "nvme0n1/device", "loop0",
		"md0/slaves/sda1", "md0/slaves/sdb1", "dm-0/slaves/nvme0n1p3",
	)
	all := []diskCounter{
		{Name: "sda"}, {Name: "sda1"}, {Name: "sdb"}, {Name: "sdb1"},
		{Name: "nvme0n1"}, {Name: "nvme0n1p2"}, {Name: "nvme0n1p3"},
		{Name: "md0"}, {Name: "dm-0"}, {Name: "loop0"},
	}
	got := diskNames(selectDiskIO(all, root))
	if len(got) != 3 || !got["sda"] || !got["sdb"] || !got["nvme0n1"] {
		t.Fatalf("应只有三块物理盘，不含分区、md/dm 和 loop: %v", got)
	}

	// 认不出物理盘（没有 device 链接）：退回整盘和阵列，按 slaves 去掉成员，叠起来的 dm 只留最上层
	stacked := t.TempDir()
	mkdirs(t, stacked, "sda", "sdb", "md0/slaves/sda", "md0/slaves/sdb", "dm-0/slaves/md0")
	got = diskNames(selectDiskIO([]diskCounter{{Name: "sda"}, {Name: "sdb"}, {Name: "md0"}, {Name: "dm-0"}}, stacked))
	if len(got) != 1 || !got["dm-0"] {
		t.Fatalf("没有物理盘时应只留最上层设备: %v", got)
	}

	// 没有 /sys/block：退回非噪声设备，按名字去掉分区
	got = diskNames(selectDiskIO(all, filepath.Join(root, "absent")))
	if len(got) != 5 || !got["sda"] || !got["sdb"] || !got["nvme0n1"] || !got["md0"] || !got["dm-0"] {
		t.Fatalf("fallback 应去掉分区和 loop: %v", got)
	}
}

// bridge、VLAN、tailscale 这类没有 device 的网卡算虚拟，不进总量；容器里全是虚拟时退回按名字筛。
func TestNetVirtualAndTotals(t *testing.T) {
	root := t.TempDir()
	mkdirs(t, root, "eno1/device", "ib0/device", "br0", "eno1.100", "tailscale0", "docker0")
	for name, want := range map[string]bool{"lo": true, "eno1": false, "ib0": false, "br0": true, "eno1.100": true, "tailscale0": true, "docker0": true} {
		if got := virtualNet(root, name); got != want {
			t.Errorf("virtualNet(%q) = %v, want %v", name, got, want)
		}
	}
	host := []NetIface{
		{Name: "eno1", RxBps: 100, TxBps: 10},
		{Name: "br0", Virtual: true, RxBps: 100, TxBps: 10},
		{Name: "tailscale0", Virtual: true, RxBps: 5, TxBps: 5},
		{Name: "ib0", RxBps: 1000, TxBps: 1000},
	}
	if rx, tx := netTotals(host); rx != 1100 || tx != 1010 {
		t.Fatalf("只应加物理网卡: rx=%d tx=%d", rx, tx)
	}
	container := []NetIface{
		{Name: "eth0", Virtual: true, RxBps: 50, TxBps: 5},
		{Name: "docker0", Virtual: true, RxBps: 50, TxBps: 5},
	}
	if rx, tx := netTotals(container); rx != 50 || tx != 5 {
		t.Fatalf("没有物理网卡时应去掉认得出的容器网卡: rx=%d tx=%d", rx, tx)
	}
	if rx, _ := netTotals([]NetIface{{Name: "veth1", Virtual: true, RxBps: 7}}); rx != 7 {
		t.Fatalf("都认不出时应全部相加: rx=%d", rx)
	}
}

// 多路服务器每颗 CPU 一个 hwmon，都要找出来；k10temp 优先 Tdie。
func TestDiscoverCPUTemps(t *testing.T) {
	root := t.TempDir()
	write := func(path, content string) {
		t.Helper()
		full := filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("hwmon0/name", "coretemp\n")
	write("hwmon0/temp1_label", "Core 0\n")
	write("hwmon0/temp1_input", "50000\n")
	write("hwmon0/temp2_label", "Package id 0\n")
	write("hwmon0/temp2_input", "60000\n")
	write("hwmon1/name", "coretemp\n")
	write("hwmon1/temp1_label", "Package id 1\n")
	write("hwmon1/temp1_input", "70000\n")
	write("hwmon2/name", "k10temp\n")
	write("hwmon2/temp1_label", "Tctl\n")
	write("hwmon2/temp1_input", "90000\n")
	write("hwmon2/temp2_label", "Tdie\n")
	write("hwmon2/temp2_input", "63000\n")
	write("hwmon3/name", "nvme\n")
	write("hwmon3/temp1_input", "40000\n")
	got := discoverCPUTemps(root)
	want := []string{
		filepath.Join(root, "hwmon0", "temp2_input"),
		filepath.Join(root, "hwmon1", "temp1_input"),
		filepath.Join(root, "hwmon2", "temp2_input"),
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestPowerReadError(t *testing.T) {
	if got := powerReadError(&fs.PathError{Op: "open", Path: "energy_uj", Err: fs.ErrPermission}); !strings.Contains(got, "root") {
		t.Fatalf("权限错误应写明只有 root 能读: %q", got)
	}
	if got := powerReadError(errors.New("坏了")); !strings.Contains(got, "坏了") {
		t.Fatalf("其它错误应带原文: %q", got)
	}
}

// CPU 热插拔改变核数时，按索引存的基准会错位，这一帧应全部重新采样（-1）而不是算出错值。
func TestCorePercentsHotplug(t *testing.T) {
	cur := []cpuTimes{{total: 100, idle: 50}, {total: 200, idle: 100}}
	prev := map[int]cpuTimes{0: {total: 50, idle: 25}, 1: {total: 100, idle: 50}, 2: {total: 1, idle: 1}}
	got := corePercents(cur, prev, 2)
	if got[0] != -1 || got[1] != -1 {
		t.Fatalf("核数变化时应全部 -1: %v", got)
	}
	// 核数一致时正常计算
	prev2 := map[int]cpuTimes{0: {total: 50, idle: 25}, 1: {total: 100, idle: 50}}
	got2 := corePercents(cur, prev2, 2)
	if got2[0] != 50 || got2[1] != 50 {
		t.Fatalf("正常每核占用 = %v", got2)
	}
}
