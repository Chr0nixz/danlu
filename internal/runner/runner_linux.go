package runner

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// signal 给任务的整个进程组，以及所有带本任务 DANLU_RUN_DIR 标记的子孙进程发信号，
// 返回收到信号的标记进程数量。按运行目录认进程而不是按任务编号：
// 编号在每个数据目录里都从 1 开始，同一用户下另一个 danlu 的同号任务不能被误杀。
func (r *Runner) signal(jobID int64, pgid int, sig syscall.Signal) int {
	if pgid > 0 {
		_ = syscall.Kill(-pgid, sig)
	}
	pids := procfs.FindByEnv(JobMarker, r.RunDir(jobID))
	for _, pid := range pids {
		_ = syscall.Kill(pid, sig)
	}
	return len(pids)
}

func (r *Runner) Terminate(jobID int64, pgid int) { r.signal(jobID, pgid, syscall.SIGTERM) }

// KillAll 强制结束任务的所有进程，返回清理掉的进程数。
func (r *Runner) KillAll(jobID int64, pgid int) int { return r.signal(jobID, pgid, syscall.SIGKILL) }

// ProcStats 只在后台调用，读取 /proc 期间不持有调度锁。
func (r *Runner) ProcStats(jobID int64) ProcessStats {
	var stats ProcessStats
	runDir := r.RunDir(jobID)
	for _, pid := range procfs.FindByEnv(JobMarker, runDir) {
		st, err := procfs.ReadStat(pid)
		marker, _ := procfs.EnvVar(pid, JobMarker)
		if err != nil || marker != runDir || st.State == 'Z' || st.State == 'X' {
			continue
		}
		status, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/status")
		if !procfs.Alive(pid, st.StartTime) {
			continue
		}
		stats.Count++
		if st.State == 'D' {
			stats.DState++
		}
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(status), "\n") {
			if rss, ok := strings.CutPrefix(line, "VmRSS:"); ok {
				fields := strings.Fields(rss)
				if len(fields) == 2 && fields[1] == "kB" {
					if kb, err := strconv.ParseInt(fields[0], 10, 64); err == nil && kb >= 0 {
						stats.RSSBytes += kb * 1024
						stats.RSSKnown = true
					}
				}
				break
			}
		}
	}
	return stats
}
