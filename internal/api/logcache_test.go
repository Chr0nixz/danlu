package api

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// 日志没变（大小和修改时间都一样）时用缓存，变了就重读；报错行只在要的时候找
func TestLogLineCache(t *testing.T) {
	path := filepath.Join(t.TempDir(), "1.log")
	mod := time.Now().Add(-time.Hour).Truncate(time.Second)
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, mod, mod); err != nil {
			t.Fatal(err)
		}
	}
	var c logLineCache
	if last, errLine := c.lines(path, true); last != "" || errLine != "" {
		t.Fatalf("没有日志时应为空: %q %q", last, errLine)
	}
	write("step 1\nValueError: bad\nstep 9\n")
	if last, errLine := c.lines(path, false); last != "step 9" || errLine != "" {
		t.Fatalf("第一次读 = %q %q", last, errLine)
	}
	if last, errLine := c.lines(path, true); last != "step 9" || errLine != "ValueError: bad" {
		t.Fatalf("要报错行时 = %q %q", last, errLine)
	}
	// 大小和修改时间都没变：用缓存，不重读
	write("step 1\nKeyError:   bad\nstep 8\n")
	if last, errLine := c.lines(path, true); last != "step 9" || errLine != "ValueError: bad" {
		t.Fatalf("日志没变时应命中缓存: %q %q", last, errLine)
	}
	// 追加之后重读
	mod = mod.Add(time.Minute)
	write("step 1\nKeyError:   bad\nstep 8\nstep 10\n")
	if last, errLine := c.lines(path, true); last != "step 10" || errLine != "KeyError:   bad" {
		t.Fatalf("日志变了应重读: %q %q", last, errLine)
	}
}
