// Package procfs 读取 Linux /proc 里的进程信息。
//
// danlu 用环境变量 JobMarker 标记任务的所有子孙进程：
// torchrun 会把每个 worker 放进新的 session，进程组追踪不到它们，
// 但环境变量会一路继承下去，读 /proc/<pid>/environ 就能认出来。
package procfs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// JobMarker 的值是任务的运行目录 <数据目录>/run/<id>，每个数据目录都不一样。
// 不用 DANLU_JOB_ID：编号在每个数据目录里都从 1 开始，同一用户下两个 danlu 的同号任务会撞上。
const JobMarker = "DANLU_RUN_DIR"

// JobOf 从进程的 JobMarker 取出任务编号；进程不属于 runRoot 下的任务时返回 0。
func JobOf(pid int, runRoot string) int64 {
	job, _ := JobEnv(pid, runRoot)
	return job
}

// JobEnv 读一次 environ，同时取出任务编号和进程启动时的 CUDA_VISIBLE_DEVICES。
// 只有属于 runRoot 下任务的进程才返回选卡变量；没设这个变量时是空串（CUDA 能看到全部卡）。
// 读的是 exec 时的环境：程序运行中改 os.environ 看不到。
func JobEnv(pid int, runRoot string) (int64, *string) {
	if runRoot == "" {
		return 0, nil
	}
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return 0, nil
	}
	return jobFromEnviron(b, runRoot)
}

// Ownership 是 GPU 上一个进程的归属。
type Ownership struct {
	Job  int64   // 本实例任务的编号；0 表示不是（外部进程，或者认不出）
	CVD  *string // 任务进程启动时的 CUDA_VISIBLE_DEVICES，只有本实例任务的进程才有
	Gone bool    // 进程已经不在了：nvidia-smi 列出来时还在，读 /proc 时已经退出
	// Unknown 表示认不出：和 danlu 同一个用户的进程，environ 读到空（正在退出，或者是僵尸）、读不到或读超时。
	// 多半是刚结束的任务还没退干净的进程，不能当成外部进程告警，但卡照样算占用
	Unknown bool
}

// JobOwnership 判断 GPU 上的进程属于本实例的哪个任务。read 读 /proc/<pid>/environ（见 EnvReader）。
func JobOwnership(pid int, runRoot string, read func(int) ([]byte, error)) Ownership {
	b, err := read(pid)
	uid, exists := -1, true
	if err != nil || len(b) == 0 {
		uid, exists = OwnerUID(pid)
	}
	return classify(b, err, runRoot, uid, exists, os.Geteuid())
}

// classify 是 JobOwnership 的判断部分：environ 读到了就按 JobMarker 认；读不到或读到空时，
// 进程不在了就丢掉，别的用户的进程算外部（读不到它的 environ 是正常的，danlu 的任务都以 danlu 的用户运行），
// 同一用户的算认不出。
func classify(environ []byte, readErr error, runRoot string, uid int, exists bool, self int) Ownership {
	if readErr == nil && len(environ) > 0 {
		if runRoot == "" {
			return Ownership{}
		}
		job, cvd := jobFromEnviron(environ, runRoot)
		return Ownership{Job: job, CVD: cvd}
	}
	switch {
	case !exists:
		return Ownership{Gone: true}
	case uid >= 0 && uid != self:
		return Ownership{}
	default:
		return Ownership{Unknown: true}
	}
}

// ErrEnvTimeout 表示读进程环境超时（或者上一次对同一个进程的读取还卡着）。
var ErrEnvTimeout = errors.New("读进程环境超时")

// EnvReader 读 /proc/<pid>/environ，每次最多等 Timeout（为 0 时 500 毫秒）。
// 读 environ 要拿目标进程的内存锁：那个进程卡在失联网络存储的缺页里时，读的人也跟着卡住，而且杀不掉。
// 只靠同步读的话，一个这样的进程（任何用户的）就让 GPU 采样过期、所有 GPU 任务停止调度。
// 超时后那次读取放着不管（占一个系统线程），同一个进程的那次没回来之前不再发起新的，直接按超时算，
// 线程不会越积越多。只给一个调用方（GPU 采样）用：同一个进程的并发读取会被当成超时。零值可用。
type EnvReader struct {
	Timeout time.Duration

	mu       sync.Mutex
	inflight map[int]bool
}

func (r *EnvReader) Read(pid int) ([]byte, error) {
	r.mu.Lock()
	if r.inflight[pid] {
		r.mu.Unlock()
		return nil, ErrEnvTimeout
	}
	if r.inflight == nil {
		r.inflight = map[int]bool{}
	}
	r.inflight[pid] = true
	r.mu.Unlock()
	type result struct {
		b   []byte
		err error
	}
	done := make(chan result, 1)
	go func() {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
		r.mu.Lock()
		delete(r.inflight, pid)
		r.mu.Unlock()
		done <- result{b, err}
	}()
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case res := <-done:
		return res.b, res.err
	case <-timer.C:
		return nil, ErrEnvTimeout
	}
}

// jobFromEnviron 从 environ 取出本实例任务的编号和选卡变量，不是本实例的任务时返回 0
func jobFromEnviron(b []byte, runRoot string) (int64, *string) {
	v, ok := LookupEnv(b, JobMarker)
	if !ok {
		return 0, nil
	}
	job := JobFromRunDir(runRoot, v)
	if job == 0 {
		return 0, nil
	}
	cvd, _ := LookupEnv(b, "CUDA_VISIBLE_DEVICES")
	return job, &cvd
}

// JobFromRunDir 解析 <runRoot>/<id>，不是这个形式时返回 0。
func JobFromRunDir(runRoot, dir string) int64 {
	if filepath.Dir(filepath.Clean(dir)) != filepath.Clean(runRoot) {
		return 0
	}
	id, err := strconv.ParseInt(filepath.Base(dir), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

type Stat struct {
	PID       int
	Comm      string
	State     byte
	PPID      int
	PGID      int
	UTime     uint64 // 用户态时钟滴答数
	STime     uint64 // 内核态时钟滴答数
	StartTime uint64 // 自系统启动以来的时钟滴答数，用来识别 pid 是否被复用
	RSSPages  uint64 // 常驻内存页数（stat 第 24 个字段），比再读 statm 少一次系统调用
}

// ParseStat 解析 /proc/<pid>/stat。进程名 comm 可能含空格和括号，所以从最后一个 ')' 往后切。
func ParseStat(s string) (Stat, error) {
	var st Stat
	open := strings.IndexByte(s, '(')
	close := strings.LastIndexByte(s, ')')
	if open < 0 || close < open {
		return st, errors.New("stat 格式错误")
	}
	pid, err := strconv.Atoi(strings.TrimSpace(s[:open]))
	if err != nil {
		return st, err
	}
	f := strings.Fields(s[close+1:])
	// f[0]=state f[1]=ppid f[2]=pgrp ... f[11]=utime f[12]=stime ... f[19]=starttime（即 stat 的第 22 个字段）
	if len(f) < 20 || len(f[0]) == 0 {
		return st, errors.New("stat 字段不足")
	}
	st.PID = pid
	st.Comm = strings.TrimSpace(s[open+1 : close])
	st.State = f[0][0]
	st.PPID, _ = strconv.Atoi(f[1])
	st.PGID, _ = strconv.Atoi(f[2])
	st.UTime, _ = strconv.ParseUint(f[11], 10, 64)
	st.STime, _ = strconv.ParseUint(f[12], 10, 64)
	st.StartTime, err = strconv.ParseUint(f[19], 10, 64)
	if len(f) > 21 {
		st.RSSPages, _ = strconv.ParseUint(f[21], 10, 64)
	}
	return st, err
}

func ReadStat(pid int) (Stat, error) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return Stat{}, err
	}
	return ParseStat(string(b))
}

// Alive 判断进程是否还在运行：存在、不是僵尸，且启动时间和记录的一致。
func Alive(pid int, start uint64) bool {
	if pid <= 0 {
		return false
	}
	st, err := ReadStat(pid)
	if err != nil || st.State == 'Z' || st.State == 'X' {
		return false
	}
	return start == 0 || st.StartTime == start
}

// EnvVar 读取进程启动时的某个环境变量。没有权限读（其他用户的进程）时返回 false。
func EnvVar(pid int, key string) (string, bool) {
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", pid))
	if err != nil {
		return "", false
	}
	return LookupEnv(b, key)
}

func LookupEnv(environ []byte, key string) (string, bool) {
	prefix := []byte(key + "=")
	for _, kv := range bytes.Split(environ, []byte{0}) {
		if bytes.HasPrefix(kv, prefix) {
			return string(kv[len(prefix):]), true
		}
	}
	return "", false
}

func PIDs() []int {
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var out []int
	for _, e := range ents {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			out = append(out, pid)
		}
	}
	return out
}

// FindByEnv 找出环境变量 key=value 的所有存活进程（不含当前进程）。
func FindByEnv(key, value string) []int {
	self := os.Getpid()
	var out []int
	for _, pid := range PIDs() {
		if pid == self {
			continue
		}
		if v, ok := EnvVar(pid, key); ok && v == value {
			if st, err := ReadStat(pid); err == nil && st.State != 'Z' {
				out = append(out, pid)
			}
		}
	}
	return out
}
