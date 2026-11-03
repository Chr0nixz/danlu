//go:build linux

package terminal

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
)

type linuxPTY struct {
	file      *os.File
	cmd       *exec.Cmd
	waitDone  chan struct{}
	closeOnce sync.Once
	closeErr  error
}

func startPTY(opt OpenOptions) (process, error) {
	shell := "/bin/bash"
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		if _, err := os.Stat(u.HomeDir); err == nil && u.Username != "" {
			if cu, err := user.Lookup(u.Username); err == nil && cu.Uid == u.Uid && cu.Gid == u.Gid {
				if cuShell := passwdShell(cu.Username); cuShell != "" {
					shell = cuShell
				}
			}
		}
	}
	cmd := exec.Command(shell, "-l")
	cmd.Dir = opt.Workdir
	cmd.Env = shellEnvironment()
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 80, Rows: 24})
	if err != nil {
		return nil, fmt.Errorf("启动登录 shell 失败: %w", err)
	}
	p := &linuxPTY{file: f, cmd: cmd, waitDone: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		close(p.waitDone)
		_ = f.Close()
	}()
	if opt.CondaEnv != "" {
		activate := "conda activate " + shellWord(opt.CondaEnv)
		if opt.CondaBase != "" {
			activate = "source " + shellQuote(opt.CondaBase+"/etc/profile.d/conda.sh") + " && " + activate
		}
		if _, err := f.Write([]byte(activate + "\n")); err != nil {
			_ = p.Close()
			return nil, fmt.Errorf("激活 conda 环境失败: %w", err)
		}
	}
	return p, nil
}

func passwdShell(username string) string {
	b, err := os.ReadFile("/etc/passwd")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Split(line, ":")
		if len(fields) == 7 && fields[0] == username {
			return fields[6]
		}
	}
	return ""
}

func shellEnvironment() []string {
	env := os.Environ()
	clean := make([]string, 0, len(env)+1)
	for _, item := range env {
		key, _, ok := strings.Cut(item, "=")
		if !ok || key == "TERM" || strings.HasPrefix(key, "DANLU_") {
			continue
		}
		clean = append(clean, item)
	}
	return append(clean, "TERM=xterm-256color")
}

func (p *linuxPTY) Read(b []byte) (int, error)  { return p.file.Read(b) }
func (p *linuxPTY) Write(b []byte) (int, error) { return p.file.Write(b) }

func (p *linuxPTY) Resize(cols, rows int) error {
	return pty.Setsize(p.file, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
}

func (p *linuxPTY) Close() error {
	p.closeOnce.Do(func() {
		p.closeErr = p.file.Close()
		if p.cmd.Process == nil {
			return
		}
		_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGHUP)
		select {
		case <-p.waitDone:
		case <-time.After(500 * time.Millisecond):
			_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
			<-p.waitDone
		}
	})
	return p.closeErr
}
