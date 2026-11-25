package gpu

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"danlu/internal/procfs"
)

func TestParseGPUExtras(t *testing.T) {
	out := `0, GPU-aaa, NVIDIA A100-SXM4-80GB, 35, 1234, 81920, 45, 120.50, 00000000:18:00.0, 55, 1410, 1215, 300.00, P0
1, GPU-bbb, NVIDIA A100-SXM4-80GB, 0, 4, 81920, 30, 50.00, 00000000:2A:00.0, [N/A], [N/A], [N/A], [N/A], [Not Supported]
`
	gpus, err := ParseGPUs(out)
	if err != nil {
		t.Fatal(err)
	}
	if gpus[0].FanPercent != 55 || gpus[0].ClockGraphicsMHz != 1410 || gpus[0].ClockMemoryMHz != 1215 ||
		gpus[0].PowerLimitW != 300 || gpus[0].PState != "P0" {
		t.Fatalf("扩展字段解析错误: %+v", gpus[0])
	}
	if gpus[1].FanPercent != 0 || gpus[1].ClockGraphicsMHz != 0 || gpus[1].ClockMemoryMHz != 0 ||
		gpus[1].PowerLimitW != 0 || gpus[1].PState != "" {
		t.Fatalf("[N/A] 应该当作不可用: %+v", gpus[1])
	}
}

func TestSMIExtrasFallback(t *testing.T) {
	base := "0, GPU-aaa, A100, 35, 0, 81920, 45, 120.50, 00000000:18:00.0\n"
	extCalls, baseCalls := 0, 0
	s := &SMI{query: func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case strings.Contains(args[0], "fan.speed"):
			extCalls++
			return nil, errors.New("unsupported option")
		case strings.HasPrefix(args[0], "--query-gpu="):
			baseCalls++
			return []byte(base), nil
		case strings.HasPrefix(args[0], "--query-compute-apps="):
			return []byte(""), nil
		default:
			return nil, errors.New("unexpected nvidia-smi args: " + strings.Join(args, " "))
		}
	}}
	gpus, err := s.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 1 || s.nextExtrasAt.Load() == 0 || extCalls != 1 || baseCalls != 1 {
		t.Fatalf("首次失败应降级并重试一次: gpus=%d next=%d ext=%d base=%d", len(gpus), s.nextExtrasAt.Load(), extCalls, baseCalls)
	}
	if _, err := s.Query(context.Background()); err != nil {
		t.Fatal(err)
	}
	if extCalls != 1 || baseCalls != 2 {
		t.Fatalf("冷却期内不应再查扩展列: ext=%d base=%d", extCalls, baseCalls)
	}
}

func TestSMIExtrasBaseFailureRetriesLater(t *testing.T) {
	extCalls, baseCalls := 0, 0
	fail := true
	s := &SMI{query: func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case strings.Contains(args[0], "fan.speed"):
			extCalls++
			if fail {
				return nil, errors.New("gpu reset")
			}
			return []byte("0, GPU-aaa, A100, 35, 0, 81920, 45, 120.50, 00000000:18:00.0, 55, 1410, 1215, 300.00, P0\n"), nil
		case strings.HasPrefix(args[0], "--query-gpu="):
			baseCalls++
			if fail {
				return nil, errors.New("gpu reset")
			}
			return []byte("0, GPU-aaa, A100, 35, 0, 81920, 45, 120.50, 00000000:18:00.0\n"), nil
		case strings.HasPrefix(args[0], "--query-compute-apps="):
			return []byte(""), nil
		default:
			return nil, errors.New("unexpected nvidia-smi args: " + strings.Join(args, " "))
		}
	}}
	if _, err := s.Query(context.Background()); err == nil {
		t.Fatal("两次查询都失败时应返回错误")
	}
	if s.nextExtrasAt.Load() == 0 {
		t.Fatal("临时故障后应设置重试时间")
	}
	if extCalls != 1 || baseCalls != 1 {
		t.Fatalf("首次应各调一次: ext=%d base=%d", extCalls, baseCalls)
	}

	// 模拟冷却结束、故障恢复：扩展列应重新启用，而不是永久降级。
	fail = false
	s.nextExtrasAt.Store(0)
	gpus, err := s.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 1 || gpus[0].FanPercent != 55 || extCalls != 2 || baseCalls != 1 {
		t.Fatalf("恢复后应重新使用扩展列: gpus=%+v ext=%d base=%d", gpus, extCalls, baseCalls)
	}
}

func TestSMIExtrasContextCanceledDoesNotFallback(t *testing.T) {
	baseCalls := 0
	s := &SMI{query: func(ctx context.Context, args ...string) ([]byte, error) {
		switch {
		case strings.Contains(args[0], "fan.speed"):
			return nil, ctx.Err()
		default:
			baseCalls++
			return []byte(""), nil
		}
	}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Query(ctx); err == nil {
		t.Fatal("取消的上下文应该返回错误")
	}
	if baseCalls != 0 || s.nextExtrasAt.Load() != 0 {
		t.Fatalf("取消时不应补查或设置冷却: base=%d next=%d", baseCalls, s.nextExtrasAt.Load())
	}
}

// 老驱动加上没开 persistence mode 时，扩展查询每次都要好几秒才报错。这一轮可以不补查，
// 但之后必须只用基本查询，否则每一轮都卡在扩展查询上，调度器永远拿不到卡的数据。
func TestSMIExtrasSlowFailurePausesExtras(t *testing.T) {
	old := extrasSlowFailure
	extrasSlowFailure = 20 * time.Millisecond
	defer func() { extrasSlowFailure = old }()

	extCalls, baseCalls := 0, 0
	s := &SMI{query: func(_ context.Context, args ...string) ([]byte, error) {
		switch {
		case strings.Contains(args[0], "fan.speed"):
			extCalls++
			time.Sleep(40 * time.Millisecond)
			return nil, errors.New("slow unsupported field")
		case strings.HasPrefix(args[0], "--query-gpu="):
			baseCalls++
			return []byte("0, GPU-aaa, A100, 35, 0, 81920, 45, 120.50, 00000000:18:00.0\n"), nil
		case strings.HasPrefix(args[0], "--query-compute-apps="):
			return []byte(""), nil
		default:
			return nil, errors.New("unexpected nvidia-smi args: " + strings.Join(args, " "))
		}
	}}
	if _, err := s.Query(context.Background()); err == nil {
		t.Fatal("扩展查询慢失败的这一轮应返回错误")
	}
	if baseCalls != 0 {
		t.Fatalf("慢失败的这一轮不应补查基本查询: base=%d", baseCalls)
	}
	if next := time.Unix(0, s.nextExtrasAt.Load()); time.Until(next) < extrasRetryAfterFallback-time.Minute {
		t.Fatalf("慢失败后应按降级暂停扩展列，下次重试在 %s", time.Until(next))
	}
	for range 3 {
		gpus, err := s.Query(context.Background())
		if err != nil {
			t.Fatalf("暂停扩展列后基本查询应能拿到数据: %v", err)
		}
		if len(gpus) != 1 {
			t.Fatalf("gpus=%+v", gpus)
		}
	}
	if extCalls != 1 || baseCalls != 3 {
		t.Fatalf("冷却期内只应跑基本查询: ext=%d base=%d", extCalls, baseCalls)
	}
}

// 扩展查询很快失败时同一轮补查基本查询，两者共用一个 smiTimeout，整轮不超出 Monitor 的过期时间。
func TestSMIExtrasFallbackSharesTimeout(t *testing.T) {
	const extSpent = 20 * time.Millisecond
	var remaining time.Duration
	var hasDeadline bool
	s := &SMI{query: func(ctx context.Context, args ...string) ([]byte, error) {
		switch {
		case strings.Contains(args[0], "fan.speed"):
			time.Sleep(extSpent)
			return nil, errors.New("unsupported option")
		case strings.HasPrefix(args[0], "--query-gpu="):
			var deadline time.Time
			deadline, hasDeadline = ctx.Deadline()
			remaining = time.Until(deadline)
			return []byte("0, GPU-aaa, A100, 35, 0, 81920, 45, 120.50, 00000000:18:00.0\n"), nil
		case strings.HasPrefix(args[0], "--query-compute-apps="):
			return []byte(""), nil
		default:
			return nil, errors.New("unexpected nvidia-smi args: " + strings.Join(args, " "))
		}
	}}
	if _, err := s.Query(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !hasDeadline {
		t.Fatal("补查的基本查询应带期限")
	}
	// 扩展查询已经用掉 extSpent，补查剩下的时间不能再是完整的 smiTimeout
	if remaining > smiTimeout-extSpent {
		t.Fatalf("补查还剩 %s，应扣掉扩展查询用掉的 %s", remaining, extSpent)
	}
}

func TestParseGPUs(t *testing.T) {
	out := `0, GPU-aaa, NVIDIA A100-SXM4-80GB, 35, 1234, 81920, 45, 120.50, 00000000:18:00.0
1, GPU-bbb, NVIDIA A100-SXM4-80GB, 0, 4, 81920, 30, [N/A], 00000000:2A:00.0
`
	gpus, err := ParseGPUs(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 2 || gpus[0].Util != 35 || gpus[0].MemTotalMB != 81920 || gpus[0].PowerW != 120.5 ||
		gpus[1].UUID != "GPU-bbb" || gpus[1].PowerW != 0 || gpus[1].BusID != "00000000:2A:00.0" {
		t.Fatalf("解析错误: %+v", gpus)
	}
	// 没有总线号这一列（旧格式）也能解析
	if gpus, err := ParseGPUs("0, GPU-aaa, A100, 35, 1234, 81920, 45, 120.50\n"); err != nil || gpus[0].BusID != "" {
		t.Fatalf("旧格式: %+v %v", gpus, err)
	}
	if _, err := ParseGPUs(""); err == nil {
		t.Fatal("空输出应报错")
	}
}

func TestParseApps(t *testing.T) {
	apps := ParseApps("12345, GPU-aaa, 1200\nbad line\n")
	if len(apps) != 1 || apps[0].PID != 12345 || apps[0].UsedMemMB != 1200 {
		t.Fatalf("解析错误: %+v", apps)
	}
}

func TestMarkUnknownMemory(t *testing.T) {
	gpus := []GPU{
		{UUID: "GPU-a", MemUsedMB: 3000, Procs: []Proc{{PID: 1, UsedMemMB: 1900}}},
		{UUID: "GPU-b", MemUsedMB: 1500, Procs: []Proc{{PID: 2, UsedMemMB: 1000}, {PID: 3, UsedMemMB: 400}}},
	}
	markUnknownMemory(gpus, 1024)
	if gpus[0].UnknownMemMB != 1100 || gpus[1].UnknownMemMB != 0 {
		t.Fatalf("未知显存计算错误: %+v", gpus)
	}
	markUnknownMemory(gpus, 1100)
	if gpus[0].UnknownMemMB != 0 {
		t.Fatalf("等于阈值时不应标记未知占用: %+v", gpus[0])
	}
}

// 按 535 驱动的格式写的样例：有 GI/CI 两行，没有进程的卡写 None，显存读不到时写 N/A
const pidsSample = `
==============NVSMI LOG==============

Timestamp                                 : Sat Nov 15 10:00:00 2026
Driver Version                            : 535.183.01
CUDA Version                              : 12.2

Attached GPUs                             : 3
GPU 00000000:18:00.0
    Processes
        GPU instance ID                   : N/A
        Compute instance ID               : N/A
        Process ID                        : 2364
            Type                          : G
            Name                          : /usr/lib/xorg/Xorg
            Used GPU Memory               : 4 MiB
        GPU instance ID                   : N/A
        Compute instance ID               : N/A
        Process ID                        : 5001
            Type                          : C+G
            Name                          : python
            Used GPU Memory               : 10240 MiB

GPU 00000000:2A:00.0
    Processes                             : None

GPU 00000000:3D:00.0
    Processes
        Process ID                        : 5002
            Type                          : G
            Name                          : python
            Used GPU Memory               : 11264 MiB
        Process ID                        : 5003
            Type                          : G
            Name                          : python
            Used GPU Memory               : N/A
`

func TestParsePIDs(t *testing.T) {
	got := ParsePIDs(strings.ReplaceAll(pidsSample, "\n", "\r\n"))
	want := []PIDEntry{
		{BusID: "00000000:18:00.0", PID: 2364, Type: "G", UsedMemMB: 4},
		{BusID: "00000000:18:00.0", PID: 5001, Type: "C+G", UsedMemMB: 10240},
		{BusID: "00000000:3D:00.0", PID: 5002, Type: "G", UsedMemMB: 11264},
		{BusID: "00000000:3D:00.0", PID: 5003, Type: "G", UsedMemMB: 0},
	}
	if !slices.Equal(got, want) {
		t.Fatalf("ParsePIDs =\n%+v\n想要\n%+v", got, want)
	}
}

func TestNormalizeBusID(t *testing.T) {
	if a, b := normalizeBusID("00000000:3D:00.0"), normalizeBusID("0000:3d:00.0"); a != b || a == "" {
		t.Fatalf("同一张卡的两种写法应该一致: %q %q", a, b)
	}
	if normalizeBusID("") != "" {
		t.Fatal("空总线号应返回空")
	}
}

func TestAddOtherProcs(t *testing.T) {
	gpus := []GPU{
		{Index: 0, BusID: "00000000:18:00.0", MemUsedMB: 10250, Procs: []Proc{{PID: 5001, UsedMemMB: 10240, JobID: 7}}},
		{Index: 1, BusID: "00000000:2A:00.0"},
		{Index: 2, BusID: "0000:3d:00.0", MemUsedMB: 30000},
	}
	jobs := map[int]int64{5001: 7, 5002: 7}
	addOtherProcs(gpus, ParsePIDs(pidsSample), 1024,
		func(pid int) procfs.Ownership { return procfs.Ownership{Job: jobs[pid]} }, func(int) string { return "alice" })
	if len(gpus[0].Procs) != 1 {
		t.Fatalf("Xorg 这类小的外部图形进程不应列出，compute-apps 里已有的进程不应重复: %+v", gpus[0].Procs)
	}
	want := []Proc{{PID: 5002, UsedMemMB: 11264, User: "alice", JobID: 7, Graphics: true}}
	if !slices.Equal(gpus[2].Procs, want) {
		t.Fatalf("任务的渲染进程应归到任务名下: %+v", gpus[2].Procs)
	}
	markUnknownMemory(gpus, 1024)
	if gpus[2].UnknownMemMB != 30000-11264 {
		t.Fatalf("认出来的图形进程不再算未知占用: %+v", gpus[2])
	}

	// 不属于任务但显存超过阈值的图形进程列成外部进程，和以前算未知占用一样让卡不可分配
	gpus = []GPU{{Index: 2, BusID: "00000000:3D:00.0", MemUsedMB: 11264}}
	addOtherProcs(gpus, ParsePIDs(pidsSample), 1024, func(int) procfs.Ownership { return procfs.Ownership{} }, func(int) string { return "bob" })
	if len(gpus[0].Procs) != 1 || gpus[0].Procs[0].PID != 5002 || gpus[0].Procs[0].JobID != 0 || !gpus[0].Procs[0].Graphics {
		t.Fatalf("大的外部图形进程应列出: %+v", gpus[0].Procs)
	}
}

// 列出来以后已经退出的进程丢掉；认不出归属（正在退出的任务进程）的照样列出、带上标记
func TestAddOtherProcsGoneAndUnknown(t *testing.T) {
	gpus := []GPU{{Index: 2, BusID: "00000000:3D:00.0", MemUsedMB: 11264}}
	addOtherProcs(gpus, ParsePIDs(pidsSample), 1024, func(int) procfs.Ownership { return procfs.Ownership{Gone: true} }, func(int) string { return "bob" })
	if len(gpus[0].Procs) != 0 {
		t.Fatalf("已经退出的进程不应列出: %+v", gpus[0].Procs)
	}
	addOtherProcs(gpus, ParsePIDs(pidsSample), 1024, func(int) procfs.Ownership { return procfs.Ownership{Unknown: true} }, func(int) string { return "bob" })
	if len(gpus[0].Procs) != 1 || !gpus[0].Procs[0].OwnerUnknown || gpus[0].Procs[0].JobID != 0 {
		t.Fatalf("认不出归属的大进程应列出并带标记: %+v", gpus[0].Procs)
	}
}

func TestHasUnaccounted(t *testing.T) {
	if hasUnaccounted([]GPU{{MemUsedMB: 100, Procs: []Proc{{UsedMemMB: 100}}}, {MemUsedMB: 0}}) {
		t.Fatal("显存全部对得上时不用补查")
	}
	if !hasUnaccounted([]GPU{{MemUsedMB: 100, Procs: []Proc{{UsedMemMB: 60}}}}) {
		t.Fatal("显存对不上时应补查")
	}
}

func TestParseMachineVersions(t *testing.T) {
	output := "| NVIDIA-SMI 560.94 Driver Version: 560.94 CUDA Version: 12.6 |"
	driver, cuda, err := parseMachineVersions(output)
	if err != nil || driver != "560.94" || cuda != "12.6" {
		t.Fatalf("parseMachineVersions = %q %q %v", driver, cuda, err)
	}
	if _, _, err := parseMachineVersions("no version info"); err == nil {
		t.Fatal("缺少版本信息时应该返回错误")
	}
}

func TestFakeBusy(t *testing.T) {
	f := &Fake{N: 4, Busy: map[int]bool{2: true}}
	gpus, _ := f.Query(context.Background())
	if len(gpus) != 4 || len(gpus[2].Procs) != 1 || len(gpus[0].Procs) != 0 {
		t.Fatalf("假 GPU 不符合预期: %+v", gpus)
	}
	if gpus[0].FanPercent == 0 || gpus[0].PowerLimitW == 0 || gpus[0].PState != "P0" {
		t.Fatalf("假 GPU 应带扩展指标: %+v", gpus[0])
	}
}

func TestCloneGPUsProcsNotNull(t *testing.T) {
	// 空的 procs 克隆后必须还是空数组而不是 nil：nil 会被 JSON 编码成 null，
	// 总览页在"卡分给了任务但还没有进程"时按数组读 procs，遇到 null 直接崩
	gpus := []GPU{{Index: 0, Procs: []Proc{}}, {Index: 1, Procs: []Proc{{PID: 42}}}}
	out := cloneGPUs(gpus)
	if out[0].Procs == nil {
		t.Fatal("空的 procs 克隆后不应变成 nil")
	}
	b, err := json.Marshal(out[0])
	if err != nil || !strings.Contains(string(b), `"procs":[]`) {
		t.Fatalf("procs 应编码成空数组: %s %v", b, err)
	}
	out[1].Procs[0].PID = 43
	if gpus[1].Procs[0].PID != 42 {
		t.Fatal("克隆应深拷贝进程列表，改副本不能影响原件")
	}
}

// 未知显存阈值可以在面板上随时改：每次查询取一次，取不到时退回配置值，再退回 1 GiB
func TestUnknownThresholdLive(t *testing.T) {
	n := 0
	s := &SMI{UnknownMemoryThresholdMB: 512, Threshold: func() int { return n }}
	if got := s.unknownThreshold(); got != 512 {
		t.Fatalf("Threshold 返回 0 时应用配置值，实际 %d", got)
	}
	n = 2048
	if got := s.unknownThreshold(); got != 2048 {
		t.Fatalf("应取最新的阈值，实际 %d", got)
	}
	if got := (&SMI{}).unknownThreshold(); got != 1024 {
		t.Fatalf("都没有时应是 1024，实际 %d", got)
	}
}

func TestCardsInCVD(t *testing.T) {
	str := func(s string) *string { return &s }
	gpus := []GPU{
		{Index: 0, UUID: "GPU-534f69f2-f0f9-50ca-4197-62cb1b49694d"},
		{Index: 1, UUID: "GPU-fcba6bec-a222-167e-d5c2-9ea64cbf5282", Procs: []Proc{
			{PID: 1, JobID: 82, CUDAVisible: str("GPU-534f69f2-f0f9-50ca-4197-62cb1b49694d,GPU-fcba6bec")},
			{PID: 2, JobID: 82, CUDAVisible: str("2,3")},
			{PID: 3, JobID: 82, CUDAVisible: str("GPU-ffffffff,MIG-abc")},
			{PID: 4},
		}},
	}
	cardsInCVD(gpus)
	got := []string{}
	for _, p := range gpus[1].Procs {
		if p.CUDAVisible == nil {
			got = append(got, "<nil>")
		} else {
			got = append(got, *p.CUDAVisible)
		}
	}
	want := []string{"0,1", "2,3", "GPU-ffffffff,MIG-abc", "<nil>"}
	if !slices.Equal(got, want) {
		t.Fatalf("UUID 应按前缀换成卡号，对不上的原样保留: %q", got)
	}
}
