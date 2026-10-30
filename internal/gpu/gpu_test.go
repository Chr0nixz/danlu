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

func TestFakeBusy(t *testing.T) {
	f := &Fake{N: 4, Busy: map[int]bool{2: true}}
	gpus, _ := f.Query(context.Background())
	if len(gpus) != 4 || len(gpus[2].Procs) != 1 || len(gpus[0].Procs) != 0 {
		t.Fatalf("假 GPU 不符合预期: %+v", gpus)
	}
}
