//go:build linux

package runner

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestLoadBashrcEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BASHRC_REMOVE_TEST", "remove-me")
	contents := "echo startup noise\nexport BASHRC_TEST_VALUE=loaded\nLOCAL_ONLY=hidden\nunset BASHRC_REMOVE_TEST\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := loadBashrcEnvironment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if values["BASHRC_TEST_VALUE"] != "loaded" {
		t.Fatalf("没有读取到 .bashrc 导出的变量: %q", values["BASHRC_TEST_VALUE"])
	}
	if _, ok := values["LOCAL_ONLY"]; ok {
		t.Fatal("未导出的 shell 变量不应进入任务环境")
	}
	if _, ok := values["BASHRC_REMOVE_TEST"]; ok {
		t.Fatal(".bashrc 中 unset 的变量不应保留在任务环境")
	}
}

// .bashrc 里起了后台进程：照样读到环境，不等后台进程退出，读完以后把它结束掉
func TestLoadBashrcEnvironmentBackgroundJob(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	pidFile := filepath.Join(home, "bg.pid")
	contents := "sleep 30 & echo $! > " + pidFile + "\nexport BASHRC_TEST_VALUE=loaded\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	begin := time.Now()
	env, err := loadBashrcEnvironment(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Since(begin); d > 5*time.Second {
		t.Fatalf("加载用了 %s，不应该等后台进程退出", d)
	}
	found := false
	for _, kv := range env {
		found = found || kv == "BASHRC_TEST_VALUE=loaded"
	}
	if !found {
		t.Fatal("没有读取到 .bashrc 导出的变量")
	}
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; syscall.Kill(pid, 0) == nil; i++ {
		if i == 50 {
			t.Fatalf(".bashrc 起的后台进程 %d 还活着", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
