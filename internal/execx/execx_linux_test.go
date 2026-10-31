package execx

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

// 后台子进程占着输出管道时，命令一退出就返回输出，并且结束这个子进程
func TestOutputWithBackgroundChild(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "bg.pid")
	begin := time.Now()
	out, err := Output(Command(context.Background(), "/bin/sh", "-c", "sleep 30 & echo $! > "+pidFile+"; echo hi"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "hi" {
		t.Fatalf("输出 %q", out)
	}
	if d := time.Since(begin); d > WaitDelay+time.Second {
		t.Fatalf("等了 %s，应该在 WaitDelay 之后就返回", d)
	}
	waitGone(t, pidFile)
}

// 超时时连子进程一起杀掉
func TestTimeoutKillsGroup(t *testing.T) {
	pidFile := filepath.Join(t.TempDir(), "bg.pid")
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	begin := time.Now()
	_, err := Output(Command(ctx, "/bin/sh", "-c", "sleep 30 & echo $! > "+pidFile+"; sleep 30"))
	if err == nil {
		t.Fatal("超时应该返回错误")
	}
	if d := time.Since(begin); d > WaitDelay+time.Second {
		t.Fatalf("等了 %s", d)
	}
	waitGone(t, pidFile)
}

func waitGone(t *testing.T, pidFile string) {
	t.Helper()
	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	for i := 0; i < 50; i++ {
		if syscall.Kill(pid, 0) != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("后台进程 %d 还活着", pid)
}
