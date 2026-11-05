//go:build linux

package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"danlu/internal/procfs"
)

// 同一用户下两个数据目录各有一个 #1：一边收尾清理、取消或认领，都不能碰到另一边的进程。
func TestSameJobIDInTwoDataDirs(t *testing.T) {
	a := &Runner{RunRoot: filepath.Join(t.TempDir(), "a", "run")}
	b := &Runner{RunRoot: filepath.Join(t.TempDir(), "b", "run")}
	start := func(r *Runner) *exec.Cmd {
		cmd := exec.Command("sleep", "60")
		cmd.Env = append(os.Environ(), "DANLU_JOB_ID=1", JobMarker+"="+r.RunDir(1))
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	pa, pb := start(a), start(b)
	// 等两个进程的 environ 可读（exec 完成）
	deadline := time.Now().Add(5 * time.Second)
	for procfs.JobOf(pa.Process.Pid, a.RunRoot) != 1 || procfs.JobOf(pb.Process.Pid, b.RunRoot) != 1 {
		if time.Now().After(deadline) {
			t.Fatal("测试进程没有带上标记")
		}
		time.Sleep(10 * time.Millisecond)
	}

	if procfs.JobOf(pb.Process.Pid, a.RunRoot) != 0 {
		t.Fatal("B 的进程不应归到 A 的任务")
	}
	if pid, _, ok := a.Adopt(1); !ok || pid != pa.Process.Pid {
		t.Fatalf("A 应认领自己的进程 %d，实际 %d %v", pa.Process.Pid, pid, ok)
	}
	if !a.Alive(1, pa.Process.Pid, 0) || a.Alive(1, pb.Process.Pid, 0) {
		t.Fatal("没有启动时间时，Alive 应只认本数据目录的进程")
	}

	if n := a.KillAll(1, 0); n != 1 {
		t.Fatalf("A 应只清理 1 个进程，实际 %d", n)
	}
	_ = pa.Wait()
	if !procfs.Alive(pb.Process.Pid, 0) {
		t.Fatal("A 收尾清理杀掉了 B 的同号任务")
	}
}
