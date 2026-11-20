//go:build linux

package system

import (
	"context"
	"testing"
	"time"
)

func TestProcResourceSourceQuery(t *testing.T) {
	src := NewResourceSource("", 0, func() GPUStats {
		return GPUStats{PowerW: 120, PowerKnown: true, MaxTempC: 60, TempKnown: true}
	})
	first, err := src.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Samples != 1 || first.Host.CPUCores == 0 || first.Memory.TotalBytes == 0 ||
		len(first.CPU.Cores) != first.Host.CPUCores || first.RootDisk == nil {
		t.Fatalf("第一次采样不完整: %+v", first)
	}
	if first.GPUPowerW == nil || *first.GPUPowerW != 120 || first.GPUTempMaxC == nil || *first.GPUTempMaxC != 60 {
		t.Fatalf("GPU 汇总错误: %+v", first)
	}
	// 让 /proc/stat 产生一点差值，第二次采样应该能算出 CPU 百分比。
	time.Sleep(20 * time.Millisecond)
	second, err := src.Query(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if second.Samples != 2 || len(second.CPU.Cores) != second.Host.CPUCores || second.ProcessSamples != 1 {
		t.Fatalf("第二次采样错误: samples=%d process_samples=%d cores=%d/%d", second.Samples, second.ProcessSamples, len(second.CPU.Cores), second.Host.CPUCores)
	}
	if second.CPU.Percent != nil && (*second.CPU.Percent < 0 || *second.CPU.Percent > 100) {
		t.Fatalf("CPU 百分比越界: %v", *second.CPU.Percent)
	}
	if second.PowerW == nil || *second.PowerW < 120 {
		t.Fatalf("合计功耗应包含 GPU: %+v", second.PowerW)
	}
	if second.EnergyWh == nil || *second.EnergyWh < 0 {
		t.Fatalf("第二次采样应开始累计电量: %+v", second.EnergyWh)
	}
}
