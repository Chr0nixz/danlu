// Package runner 负责把一个任务真正跑起来：生成包装脚本、注入环境变量、启动、结束进程。
package runner

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Runner struct {
	RunRoot   string // ~/.danlu/run
	LogRoot   string // ~/.danlu/logs
	CondaBase string
}

type Spec struct {
	JobID         int64
	Name          string
	Workdir       string
	CondaEnv      string
	Cmd           string
	Env           map[string]string
	InheritBashrc bool
	GPUs          []int
	Port          int
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
const runScript = `#!/usr/bin/env bash
# 由 danlu 生成，请勿修改
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

// ReadExitCode 读取包装脚本写下的退出码；进程被强杀时没有这个文件。
func (r *Runner) ReadExitCode(id int64) (int, bool) {
	b, err := os.ReadFile(filepath.Join(r.RunDir(id), "exitcode"))
	if err != nil {
		return 0, false
	}
	v, err := strconv.Atoi(strings.TrimSpace(string(b)))
	return v, err == nil
}
