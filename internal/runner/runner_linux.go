package runner

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"danlu/internal/procfs"
)

// Start 在新的 session 里启动任务（不受 danlu 退出、SSH 断开影响），
// 返回 pid 和进程启动时间（用于 danlu 重启后确认是不是同一个进程）。
// env 是 LoadEnv 给出的基础环境；这里只写脚本和 fork/exec，毫秒级，调度器在锁里调用。
func (r *Runner) Start(s Spec, env []string) (int, uint64, error) {
	script, err := r.prepare(s)
	if err != nil {
		return 0, 0, err
	}
	logf, err := os.OpenFile(r.LogPath(s.JobID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, 0, err
	}
	defer logf.Close() // 子进程已经拿到自己的文件描述符
	cmd := exec.Command("/bin/bash", script)
	cmd.Dir = r.RunDir(s.JobID)
	cmd.Env = r.BuildEnv(env, s)
	cmd.Stdout = logf // 直接传文件，子进程直接写日志，不经过 danlu 中转
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, 0, err
	}
	pid := cmd.Process.Pid
	go cmd.Wait() // 回收子进程，避免留下僵尸
	st, err := procfs.ReadStat(pid)
	if err != nil {
		return pid, 0, nil
	}
	return pid, st.StartTime, nil
}

// Signal 给任务的整个进程组，以及所有带 DANLU_JOB_ID 标记的子孙进程发信号，
// 返回收到信号的标记进程数量。
func Signal(jobID int64, pgid int, sig syscall.Signal) int {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, sig)
	}
	pids := procfs.FindByEnv("DANLU_JOB_ID", strconv.FormatInt(jobID, 10))
	for _, pid := range pids {
		_ = syscall.Kill(pid, sig)
	}
	return len(pids)
}

func (r *Runner) Terminate(jobID int64, pgid int) { Signal(jobID, pgid, syscall.SIGTERM) }

// KillAll 强制结束任务的所有进程，返回清理掉的进程数。
func (r *Runner) KillAll(jobID int64, pgid int) int { return Signal(jobID, pgid, syscall.SIGKILL) }
