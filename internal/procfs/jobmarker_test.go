package procfs

import (
	"path/filepath"
	"testing"
)

func TestJobFromRunDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "home-a", "run")
	other := filepath.Join(t.TempDir(), "home-b", "run")
	cases := []struct {
		dir  string
		want int64
	}{
		{filepath.Join(root, "1"), 1},
		{filepath.Join(root, "42") + string(filepath.Separator), 42},
		{filepath.Join(other, "1"), 0}, // 别的数据目录的同号任务
		{filepath.Join(root, "1", "code"), 0},
		{filepath.Join(root, "x"), 0},
		{filepath.Join(root, "0"), 0},
		{root, 0},
		{"", 0},
	}
	for _, c := range cases {
		if got := JobFromRunDir(root, c.dir); got != c.want {
			t.Errorf("JobFromRunDir(%q) = %d，应为 %d", c.dir, got, c.want)
		}
	}
	if JobOf(1, "") != 0 {
		t.Fatal("runRoot 为空时不应识别任务")
	}
}
