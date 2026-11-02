// Package runner 负责把一个任务真正跑起来：生成包装脚本、注入环境变量、启动、结束进程。
// *Runner 实现了 scheduler.Executor。
package runner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"danlu/internal/procfs"
)

type Runner struct {
	RunRoot    string // ~/.danlu/run
	LogRoot    string // ~/.danlu/logs
	EnvRoot    string // ~/.danlu/envs
	CondaBase  string
	manifestMu sync.Mutex
}

type Spec struct {
	JobID     int64
	Name      string
	Workdir   string
	CondaEnv  string
	Cmd       string
	Env       map[string]string
	GPUs      []int
	Port      int
	OutputDir string
}

func (r *Runner) RunDir(id int64) string  { return filepath.Join(r.RunRoot, strconv.FormatInt(id, 10)) }
func (r *Runner) LogPath(id int64) string { return filepath.Join(r.LogRoot, fmt.Sprintf("%d.log", id)) }

// ReservedEnv 是 danlu 自己注入的变量，用户不能覆盖。
func ReservedEnv(key string) bool {
	switch key {
	case "CUDA_VISIBLE_DEVICES", "CUDA_DEVICE_ORDER", "PET_MASTER_PORT":
		return true
	}
	return strings.HasPrefix(key, "DANLU_")
}

// 用环境变量传参，脚本里不拼接任何用户输入，不用担心引号转义。
// 第一件事是写 pid 文件：danlu 在启动途中退出时，重启后靠它把进程认回来，而不是再启动一次。
const runScript = `#!/usr/bin/env bash
# 由 danlu 生成，请勿修改
echo $$ > "$DANLU_RUN_DIR/pid.tmp" && mv -f "$DANLU_RUN_DIR/pid.tmp" "$DANLU_RUN_DIR/pid"
finish() { echo "$1" > "$DANLU_RUN_DIR/exitcode.tmp" && mv -f "$DANLU_RUN_DIR/exitcode.tmp" "$DANLU_RUN_DIR/exitcode"; exit "$1"; }
cd "$DANLU_WORKDIR" || { echo "[danlu] 无法进入工作目录: $DANLU_WORKDIR"; finish 1; }
if [ -n "$DANLU_CONDA_ENV" ]; then
  if [ ! -f "$DANLU_CONDA_BASE/etc/profile.d/conda.sh" ]; then
    echo "[danlu] 找不到 conda: $DANLU_CONDA_BASE/etc/profile.d/conda.sh"; finish 127
  fi
  source "$DANLU_CONDA_BASE/etc/profile.d/conda.sh" && conda activate "$DANLU_CONDA_ENV" \
    || { echo "[danlu] conda activate 失败: $DANLU_CONDA_ENV"; finish 127; }
fi
echo "[danlu] 任务 #$DANLU_JOB_ID 开始于 $(date '+%F %T')  GPU=[$CUDA_VISIBLE_DEVICES]  端口=$PET_MASTER_PORT"
bash "$DANLU_RUN_DIR/cmd.sh"
code=$?
echo "[danlu] 任务结束于 $(date '+%F %T')，退出码 $code"
finish $code
`

func (r *Runner) prepare(s Spec) (string, error) {
	dir := r.RunDir(s.JobID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	_ = os.Remove(filepath.Join(dir, "exitcode"))
	_ = os.Remove(filepath.Join(dir, "pid"))
	if err := os.WriteFile(filepath.Join(dir, "cmd.sh"), []byte(s.Cmd+"\n"), 0o600); err != nil {
		return "", err
	}
	script := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(script, []byte(runScript), 0o700); err != nil {
		return "", err
	}
	if err := os.MkdirAll(r.LogRoot, 0o700); err != nil {
		return "", err
	}
	return script, nil
}

// BuildEnv 在 base（通常是 danlu 自己的环境）上叠加用户变量和 danlu 注入的变量。
func (r *Runner) BuildEnv(base []string, s Spec) []string {
	m := map[string]string{}
	var order []string
	set := func(k, v string) {
		if _, ok := m[k]; !ok {
			order = append(order, k)
		}
		m[k] = v
	}
	for _, kv := range base {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || ReservedEnv(k) {
			continue
		}
		set(k, v)
	}
	for k, v := range s.Env {
		if !ReservedEnv(k) {
			set(k, v)
		}
	}
	gpus := make([]string, len(s.GPUs))
	for i, g := range s.GPUs {
		gpus[i] = strconv.Itoa(g)
	}
	id := strconv.FormatInt(s.JobID, 10)
	set("CUDA_VISIBLE_DEVICES", strings.Join(gpus, ","))
	// 让 CUDA 的设备编号和 nvidia-smi 一致（CUDA 默认按算力排序，编号可能对不上）
	set("CUDA_DEVICE_ORDER", "PCI_BUS_ID")
	// torchrun 会读 PET_MASTER_PORT 作为 --master_port 的默认值，避免多个任务撞端口
	set("PET_MASTER_PORT", strconv.Itoa(s.Port))
	set("DANLU_PORT", strconv.Itoa(s.Port))
	// 输出重定向到文件时 Python 默认整块缓冲，日志会迟迟不出现
	set("PYTHONUNBUFFERED", "1")
	set("DANLU_JOB_ID", id)
	set("DANLU_JOB_NAME", s.Name)
	set("DANLU_RUN_DIR", r.RunDir(s.JobID))
	set("DANLU_WORKDIR", s.Workdir)
	set("DANLU_CONDA_BASE", r.CondaBase)
	set("DANLU_CONDA_ENV", s.CondaEnv)
	if s.OutputDir != "" {
		set("DANLU_OUTPUT_DIR", s.OutputDir)
	}
	tag := "danlu-" + id
	if old := m["WANDB_TAGS"]; old != "" {
		tag = old + "," + tag
	}
	set("WANDB_TAGS", tag)

	out := make([]string, 0, len(order))
	for _, k := range order {
		out = append(out, k+"="+m[k])
	}
	return out
}

// LoadEnv 返回任务的基础环境：默认是 danlu 自己的环境，勾选了"Bash 环境"时是加载 ~/.bashrc 之后的环境。
func (r *Runner) LoadEnv(ctx context.Context, inheritBashrc bool) ([]string, error) {
	if !inheritBashrc {
		return os.Environ(), nil
	}
	return loadBashrcEnvironment(ctx)
}

// ReadExitCode 读取包装脚本写下的退出码；进程被强杀时没有这个文件。
func (r *Runner) ReadExitCode(id int64) (int, bool) {
	return readInt(filepath.Join(r.RunDir(id), "exitcode"))
}

// Launched 返回包装脚本留下的 pid，不要求进程仍然存活。
// 文件损坏时仍然视为启动过，避免把已执行的任务放回队列再启动一次。
func (r *Runner) Launched(id int64) (int, bool) {
	path := filepath.Join(r.RunDir(id), "pid")
	if _, err := os.Stat(path); err != nil {
		return 0, false
	}
	pid, _ := readInt(path)
	return pid, true
}

// Alive 判断任务的包装脚本还在不在。没有记下启动时间时（启动后马上读 /proc 失败），
// 改为核对进程环境里的 DANLU_JOB_ID，防止 pid 被别的进程复用后认错。
func (r *Runner) Alive(id int64, pid int, start uint64) bool {
	if !procfs.Alive(pid, start) {
		return false
	}
	if start != 0 {
		return true
	}
	v, ok := procfs.EnvVar(pid, "DANLU_JOB_ID")
	return ok && v == strconv.FormatInt(id, 10)
}

// Adopt 找回 danlu 启动途中退出时留下的任务进程，返回包装脚本的 pid 和启动时间。
// 先读 pid 文件；没有的话（脚本还没来得及写），再按 DANLU_JOB_ID 找会话首进程：
// 包装脚本是用 setsid 启动的，它的 pid 就是进程组号。
func (r *Runner) Adopt(id int64) (int, uint64, bool) {
	if pid, ok := readInt(filepath.Join(r.RunDir(id), "pid")); ok && r.Alive(id, pid, 0) {
		if st, err := procfs.ReadStat(pid); err == nil {
			return pid, st.StartTime, true
		}
	}
	for _, pid := range procfs.FindByEnv("DANLU_JOB_ID", strconv.FormatInt(id, 10)) {
		if st, err := procfs.ReadStat(pid); err == nil && st.PGID == pid {
			return pid, st.StartTime, true
		}
	}
	return 0, 0, false
}

// BootID 是这次开机的标识，服务器重启后会变。读不到时返回空字符串。
func (r *Runner) BootID() string {
	path := os.Getenv("DANLU_BOOT_ID_FILE")
	if path == "" {
		path = "/proc/sys/kernel/random/boot_id"
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func readInt(path string) (int, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return v, err == nil
}
