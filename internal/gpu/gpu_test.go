package gpu

import (
	"context"
	"testing"
)

func TestParseGPUs(t *testing.T) {
	out := `0, GPU-aaa, NVIDIA A100-SXM4-80GB, 35, 1234, 81920, 45, 120.50
1, GPU-bbb, NVIDIA A100-SXM4-80GB, 0, 4, 81920, 30, [N/A]
`
	gpus, err := ParseGPUs(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(gpus) != 2 || gpus[0].Util != 35 || gpus[0].MemTotalMB != 81920 || gpus[0].PowerW != 120.5 ||
		gpus[1].UUID != "GPU-bbb" || gpus[1].PowerW != 0 {
		t.Fatalf("解析错误: %+v", gpus)
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
	gpus := []GPU{{UUID: "GPU-a", MemUsedMB: 3000}, {UUID: "GPU-b", MemUsedMB: 1500}}
	apps := []App{{UUID: "GPU-a", UsedMemMB: 1900}, {UUID: "GPU-b", UsedMemMB: 1400}}
	markUnknownMemory(gpus, apps, 1024)
	if gpus[0].UnknownMemMB != 1100 || gpus[1].UnknownMemMB != 0 {
		t.Fatalf("未知显存计算错误: %+v", gpus)
	}
	markUnknownMemory(gpus, apps, 1100)
	if gpus[0].UnknownMemMB != 0 {
		t.Fatalf("等于阈值时不应标记未知占用: %+v", gpus[0])
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
