package execx

import (
	"context"
	"os/exec"
	"syscall"
)

// Command 和 exec.CommandContext 一样，另外加上包注释里说的两道护栏。
//
// 用 setsid 而不是只建进程组：新会话没有控制终端。在终端里手动运行 danlu serve 时，
// 一个处在后台进程组、又能打开 /dev/tty 的 bash -i 会给自己发 SIGTTIN 停住，
// 读终端的程序（比如要密码的 git）也会停住，直到超时。没有控制终端时它们直接出错返回。
// 会话首进程的 pid 同时也是进程组号，所以照样能一次杀掉整个组。
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = WaitDelay
	return cmd
}

// KillGroup 结束命令留下的整个进程组，比如 .bashrc 里用 & 起的后台进程。
// 只在 Wait 返回后调用：进程组里还有进程时，组号不会被系统分给别人。
func KillGroup(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
