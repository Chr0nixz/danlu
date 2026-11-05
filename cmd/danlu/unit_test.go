package main

import (
	"strings"
	"testing"
)

// PATH 里的空格和 %、程序路径里的空格和 $ 都要原样到达 systemd
func TestRenderServiceUnitQuotes(t *testing.T) {
	got, err := renderServiceUnit("/opt/my tools/danlu$1", "/home/u/.danlu", "/usr/bin:/opt/CUDA 12/bin:/x%y")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`ExecStart="/opt/my tools/danlu$$1" serve`,
		`Environment="DANLU_HOME=/home/u/.danlu"`,
		`Environment="PATH=/usr/bin:/opt/CUDA 12/bin:/x%%y"`,
		"KillMode=process",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("unit 里缺少 %q：\n%s", want, got)
		}
	}
	if _, err := renderServiceUnit("/bin/danlu", "/home/u/.danlu", "/bin\nExecStartPre=/bin/evil"); err == nil {
		t.Fatal("带换行的 PATH 应被拒绝")
	}
}
