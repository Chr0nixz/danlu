package runner

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"danlu/internal/procfs"
	"danlu/internal/strict"
)

// Start 在新的 session 里启动任务（不受 danlu 退出、SSH 断开影响），
// 返回 pid 和进程启动时间（用于 danlu 重启后确认是不是同一个进程）。
// env 是 LoadEnv 给出的基础环境。普通任务只写脚本和 fork/exec，毫秒级，调度器在锁里调用；
// 严格任务要经 sudo 调助手，调度器在锁外调用（见 startStrict）。
func (r *Runner) Start(s Spec, env []string) (Started, error) {
	if s.Strict {
		return r.startStrict(s, env)
	}
	script, err := r.prepare(s)
	if err != nil {
		return Started{}, err
	}
	logf, err := os.OpenFile(r.LogPath(s.JobID), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return Started{}, err
	}
	defer logf.Close() // 子进程已经拿到自己的文件描述符
	cmd := exec.Command("/bin/bash", script)
	cmd.Dir = r.RunDir(s.JobID)
	cmd.Env = r.BuildEnv(env, s)
	cmd.Stdout = logf // 直接传文件，子进程直接写日志，不经过 danlu 中转
	cmd.Stderr = logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return Started{}, err
	}
	pid := cmd.Process.Pid
	go cmd.Wait() // 回收子进程，避免留下僵尸
	st, err := procfs.ReadStat(pid)
	if err != nil {
		return Started{PID: pid}, nil
	}
	return Started{PID: pid, StartTime: st.StartTime}, nil
}

// startStrict 请 root 助手把任务作为临时 systemd 服务启动，只放行分到的卡。
// 完整环境写进任务目录的 env 文件（NUL 分隔，0600），由服务里的 danlu job-exec 读取后删掉：
// 不经 systemd 的环境变量传递，任何取值都不用转义。失败时原样返回 *strict.Error，调度器据此放回队列。
func (r *Runner) startStrict(s Spec, env []string) (Started, error) {
	if r.StrictStart == nil || r.Exe == "" {
		return Started{}, &strict.Error{Reason: strict.ReasonFailed, Detail: "这个 danlu 没有配置严格限卡"}
	}
	if _, err := r.prepare(s); err != nil {
		return Started{}, err
	}
	var buf []byte
	for _, kv := range r.BuildEnv(env, s) {
		buf = append(append(buf, kv...), 0)
	}
	envFile := filepath.Join(r.RunDir(s.JobID), "env")
	if err := os.WriteFile(envFile, buf, 0o600); err != nil {
		return Started{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res, err := r.StrictStart(ctx, strict.StartRequest{JobID: s.JobID, RunRoot: r.RunRoot, Exec: r.Exe,
		Log: r.LogPath(s.JobID), EnvFile: envFile, DanluPID: os.Getpid(), GPUs: s.GPUBuses})
	if err != nil {
		_ = os.Remove(envFile)
		return Started{}, err
	}
	pid := res.PID
	if pid <= 0 {
		// 任务一启动就结束时 systemd 给不出 MainPID，包装脚本第一件事写的 pid 文件还在
		pid, _ = readInt(filepath.Join(r.RunDir(s.JobID), "pid"))
	}
	started := Started{PID: pid, Unit: res.Unit}
	if st, err := procfs.ReadStat(pid); err == nil && pid > 0 {
		started.StartTime = st.StartTime
	}
	return started, nil
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
