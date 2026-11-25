//go:build linux

package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"danlu/internal/procfs"
	"danlu/internal/strict"
)

func strictRunner(t *testing.T, start func(context.Context, strict.StartRequest) (strict.StartResult, error)) *Runner {
	t.Helper()
	root := t.TempDir()
	return &Runner{RunRoot: filepath.Join(root, "run"), LogRoot: filepath.Join(root, "logs"), Exe: "/bin/true", StrictStart: start}
}

func strictSpec() Spec {
	return Spec{JobID: 7, Name: "x", Workdir: "/tmp", Cmd: "true", Strict: true}
}

// 拿不到 MainPID 时回退读 pid 文件，但文件在运行目录里、任务自己能改：只认活着且带本任务标记的进程。
// 文件里写着 1 时绝不能返回 1，否则取消时会 kill(-1) 杀掉这个用户的所有进程
func TestStartStrictVerifiesPIDFile(t *testing.T) {
	var r *Runner
	writePID := func(pid int) func(context.Context, strict.StartRequest) (strict.StartResult, error) {
		return func(_ context.Context, req strict.StartRequest) (strict.StartResult, error) {
			if err := os.WriteFile(filepath.Join(req.RunRoot, "7", "pid"), []byte(strconv.Itoa(pid)+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			return strict.StartResult{Unit: "danlu-job-test.service"}, nil
		}
	}
	r = strictRunner(t, writePID(1))
	if got, err := r.Start(strictSpec(), nil); err != nil || got.PID != 0 || got.Unit != "danlu-job-test.service" {
		t.Fatalf("pid 文件写成 1 时不能采用: %+v %v", got, err)
	}

	// 别人的活进程（没有本任务标记）也不认
	r = strictRunner(t, writePID(os.Getpid()))
	if got, err := r.Start(strictSpec(), nil); err != nil || got.PID != 0 {
		t.Fatalf("不带标记的进程不能采用: %+v %v", got, err)
	}

	// 真正的包装脚本：活着、带着标记
	r = strictRunner(t, nil)
	cmd := exec.Command("sleep", "60")
	cmd.Env = append(os.Environ(), JobMarker+"="+r.RunDir(7))
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(5 * time.Second)
	for !r.Alive(7, cmd.Process.Pid, 0) {
		if time.Now().After(deadline) {
			t.Fatal("测试进程没有带上标记")
		}
		time.Sleep(10 * time.Millisecond)
	}
	r.StrictStart = writePID(cmd.Process.Pid)
	got, err := r.Start(strictSpec(), nil)
	if err != nil || got.PID != cmd.Process.Pid || got.StartTime == 0 {
		t.Fatalf("应采用带标记的包装脚本 %d: %+v %v", cmd.Process.Pid, got, err)
	}
	if st, _ := procfs.ReadStat(got.PID); st.StartTime != got.StartTime {
		t.Fatalf("启动时间不对: %+v", got)
	}

	// 一直没有 pid 文件：等到期返回 0
	if pid := r.waitStrictPID(8, 100*time.Millisecond); pid != 0 {
		t.Fatalf("没有 pid 文件应返回 0: %d", pid)
	}
}

// 每次启动换一个环境文件名，之前留下的（包括旧版本固定叫 env 的）先删掉；启动失败时删掉这一次的
func TestStartStrictEnvFile(t *testing.T) {
	var seen string
	r := strictRunner(t, func(_ context.Context, req strict.StartRequest) (strict.StartResult, error) {
		seen = req.EnvFile
		b, err := os.ReadFile(req.EnvFile)
		if err != nil || !strings.Contains(string(b), "FOO=bar\x00") {
			t.Errorf("环境文件内容不对: %q %v", b, err)
		}
		return strict.StartResult{}, &strict.Error{Reason: strict.ReasonFailed, Detail: "失败"}
	})
	dir := r.RunDir(7)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"env", "env.0123456789abcdef"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("OLD=1\x00"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Start(strictSpec(), []string{"FOO=bar"}); err == nil {
		t.Fatal("助手失败应返回错误")
	}
	if filepath.Dir(seen) != dir || !strings.HasPrefix(filepath.Base(seen), "env.") || filepath.Base(seen) == "env.0123456789abcdef" {
		t.Fatalf("环境文件应换新名字: %s", seen)
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "env") {
			t.Fatalf("留下了环境文件 %s", e.Name())
		}
	}
}

// 取值里的 NUL 会在 NUL 分隔的环境文件里拆出一条新变量：直接拒绝，不调助手
func TestStartStrictRejectsNUL(t *testing.T) {
	called := false
	r := strictRunner(t, func(context.Context, strict.StartRequest) (strict.StartResult, error) {
		called = true
		return strict.StartResult{}, errors.New("不该调到")
	})
	s := strictSpec()
	s.Env = map[string]string{"FOO": "a\x00CUDA_VISIBLE_DEVICES=0,1,2,3"}
	if _, err := r.Start(s, nil); err == nil || !strings.Contains(err.Error(), "FOO") || called {
		t.Fatalf("应拒绝含 NUL 的取值且不调助手: %v called=%v", err, called)
	}
}

// /proc/devices 里认 nvidia-uvm：只看字符设备，名字要整段相等
func TestHasCharDevice(t *testing.T) {
	devices := "Character devices:\n  1 mem\n195 nvidia\n509 nvidia-uvm\n\nBlock devices:\n  8 nvidia-uvm\n"
	if !hasCharDevice(devices, "nvidia-uvm") || !hasCharDevice(devices, "nvidia") {
		t.Fatal("应认出已加载的字符设备")
	}
	if hasCharDevice("Character devices:\n195 nvidia\n509 nvidia-uvm-tools\n\nBlock devices:\n  8 nvidia-uvm\n", "nvidia-uvm") {
		t.Fatal("没加载 uvm 时不应误认（名字前缀相同、块设备同名都不算）")
	}
}
