package runner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"danlu/internal/execx"
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
//
// 返回的 PID 为 0 表示服务已经结束、拿不到还活着的包装脚本：调度器看有没有 pid 文件决定按跑完收尾还是判启动失败。
func (r *Runner) startStrict(s Spec, env []string) (Started, error) {
	if r.StrictStart == nil || r.Exe == "" {
		return Started{}, &strict.Error{Reason: strict.ReasonFailed, Detail: "这个 danlu 没有配置严格限卡"}
	}
	if _, err := r.prepare(s); err != nil {
		return Started{}, err
	}
	if len(s.GPUs) > 0 {
		ensureUVM()
	}
	dir := r.RunDir(s.JobID)
	var buf []byte
	for _, kv := range r.BuildEnv(env, s) {
		if strings.IndexByte(kv, 0) >= 0 {
			// env 文件按 NUL 分隔，取值里的 NUL 会拆出一条新变量，盖过保留变量。Submit 已经拦过，这里兜底
			k, _, _ := strings.Cut(kv, "=")
			return Started{}, fmt.Errorf("环境变量 %s 的值含有空字符", strings.ReplaceAll(k, "\x00", ""))
		}
		buf = append(append(buf, kv...), 0)
	}
	// 每次启动换一个文件名：上一次没收到回答、晚到的旧单元读不到这一次的环境。
	// 先清掉之前留下的（包括旧版本固定叫 env 的），它们装着完整环境
	removeEnvFiles(dir)
	envFile := filepath.Join(dir, "env."+randomSuffix())
	if err := os.WriteFile(envFile, buf, 0o600); err != nil {
		return Started{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	res, err := r.StrictStart(ctx, strict.StartRequest{JobID: s.JobID, RunRoot: r.RunRoot, Exec: r.Exe,
		Log: r.LogPath(s.JobID), EnvFile: envFile, DanluPID: os.Getpid(), GPUs: s.GPUBuses})
	if err != nil {
		// 删掉环境文件：还没读到它的单元起不来。读到了的，由调度器按 pid 文件和标记认领或清掉
		_ = os.Remove(envFile)
		return Started{Unit: res.Unit}, err
	}
	started := Started{PID: res.PID, Unit: res.Unit}
	if started.PID <= 0 {
		// systemd 给不出 MainPID：任务一启动就结束了，或者查询出错。等包装脚本第一件事写的 pid 文件
		started.PID = r.waitStrictPID(s.JobID, strictPIDWait)
	}
	if st, err := procfs.ReadStat(started.PID); err == nil && started.PID > 0 {
		started.StartTime = st.StartTime
	}
	return started, nil
}

// ensureUVM 在严格单元外面先把 nvidia-uvm 加载好。放行清单在单元启动那一刻按 /proc/devices 定死，
// 而 .run 安装的驱动开机后不自动加载 uvm：第一个 CUDA 程序会经 nvidia-modprobe（setuid root）加载它、
// 建 /dev/nvidia-uvm，可那时它在严格单元里，新设备的主设备号不在放行清单上，mknod 和 open 都被拒绝，
// 重启后的第一个严格 GPU 任务就 CUDA 初始化失败。尽力而为：失败只记日志，照常启动。
func ensureUVM() {
	if b, err := os.ReadFile("/proc/devices"); err != nil || hasCharDevice(string(b), "nvidia-uvm") {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := execx.Output(execx.Command(ctx, "nvidia-modprobe", "-u", "-c=0")); err != nil {
		log.Printf("加载 nvidia-uvm 失败，严格限卡任务可能初始化不了 CUDA: %v", err)
	}
}

// hasCharDevice 判断 /proc/devices 里有没有这个名字的字符设备
func hasCharDevice(devices, name string) bool {
	for _, line := range strings.Split(devices, "\n") {
		if strings.HasPrefix(line, "Block devices") {
			return false
		}
		if f := strings.Fields(line); len(f) == 2 && f[1] == name {
			return true
		}
	}
	return false
}

// strictPIDWait 是拿不到 MainPID 时等 pid 文件的时间。job-exec 读环境、接日志后马上 exec 包装脚本，
// 包装脚本第一件事就写 pid 文件，正常是毫秒级
const strictPIDWait = 3 * time.Second

// waitStrictPID 等包装脚本写出 pid 文件，返回核对过的 pid。pid 文件在运行目录里，任务自己也能改写，
// 所以只认还活着、环境里带着本任务标记的进程；进程已经结束（或文件内容不对）时返回 0。
func (r *Runner) waitStrictPID(id int64, wait time.Duration) int {
	path := filepath.Join(r.RunDir(id), "pid")
	deadline := time.Now().Add(wait)
	for {
		if _, err := os.Stat(path); err == nil {
			break
		}
		if time.Now().After(deadline) {
			return 0
		}
		time.Sleep(50 * time.Millisecond)
	}
	pid, ok := readInt(path)
	if !ok || pid <= 1 || !r.Alive(id, pid, 0) {
		return 0
	}
	return pid
}

// removeEnvFiles 删掉运行目录里之前留下的严格任务环境文件
func removeEnvFiles(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if name := e.Name(); name == "env" || strings.HasPrefix(name, "env.") {
			_ = os.Remove(filepath.Join(dir, name))
		}
	}
}

func randomSuffix() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// signal 给任务的整个进程组，以及所有带本任务 DANLU_RUN_DIR 标记的子孙进程发信号，
// 返回收到信号的标记进程数量。按运行目录认进程而不是按任务编号：
// 编号在每个数据目录里都从 1 开始，同一用户下另一个 danlu 的同号任务不能被误杀。
func (r *Runner) signal(jobID int64, pgid int, sig syscall.Signal) int {
	// pgid 是 1 时 kill(-1) 会发给这个用户的所有进程（包括 danlu 自己），0 是自己的进程组：都不发
	if pgid > 1 {
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
