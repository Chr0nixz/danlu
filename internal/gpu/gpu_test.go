package gpu

import (
	"context"
	"slices"
	"strings"
	"testing"
)

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
		func(pid int) int64 { return jobs[pid] }, func(int) string { return "alice" })
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
	addOtherProcs(gpus, ParsePIDs(pidsSample), 1024, func(int) int64 { return 0 }, func(int) string { return "bob" })
	if len(gpus[0].Procs) != 1 || gpus[0].Procs[0].PID != 5002 || gpus[0].Procs[0].JobID != 0 || !gpus[0].Procs[0].Graphics {
		t.Fatalf("大的外部图形进程应列出: %+v", gpus[0].Procs)
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
}
