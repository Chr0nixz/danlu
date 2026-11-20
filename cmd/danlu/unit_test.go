package main

import (
	"strings"
	"testing"

	"danlu/internal/gpu"
)

// 资源页只汇总读得到功耗/温度的卡：老驱动上功率为 0 的卡不该把合计拉成 0，也不该被当成 0 W。
func TestResourceGPUStats(t *testing.T) {
	stats := resourceGPUStats([]gpu.GPU{
		{PowerW: 120, TempC: 55},
		{PowerW: 0, TempC: 0},
		{PowerW: 80.5, TempC: 71},
	})
	if !stats.PowerKnown || stats.PowerW != 200.5 {
		t.Fatalf("功耗合计错误: %+v", stats)
	}
	if !stats.TempKnown || stats.MaxTempC != 71 {
		t.Fatalf("最高温度错误: %+v", stats)
	}
	if empty := resourceGPUStats(nil); empty.PowerKnown || empty.TempKnown {
		t.Fatalf("没有卡时不应报告已知: %+v", empty)
	}
}

// PATH 里的空格和 %、程序路径里的空格和 $ 都要原样到达 systemd
func TestRenderServiceUnitQuotes(t *testing.T) {
	got, err := renderServiceUnit("/opt/my tools/danlu$1", "/home/u/.danlu", "/usr/bin:/opt/CUDA 12/bin:/x%y")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`ExecStart="/opt/my tools/danlu$1" serve`,
		`Environment="DANLU_HOME=/home/u/.danlu"`,
		`Environment="PATH=/usr/bin:/opt/CUDA 12/bin:/x%%y"`,
		"KillMode=process",
		"OOMPolicy=continue",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit 里缺少 %q：\n%s", want, got)
		}
	}
	if _, err := renderServiceUnit("/bin/danlu", "/home/u/.danlu", "/bin\nExecStartPre=/bin/evil"); err == nil {
		t.Fatal("带换行的 PATH 应被拒绝")
	}
}
