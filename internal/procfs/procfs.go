// Package procfs 读取 Linux /proc 里的进程信息。
//
// danlu 用环境变量 DANLU_JOB_ID 标记任务的所有子孙进程：
// torchrun 会把每个 worker 放进新的 session，进程组追踪不到它们，
// 但环境变量会一路继承下去，读 /proc/<pid>/environ 就能认出来。
package procfs

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Stat struct {
	PID       int
	State     byte
	PPID      int
	PGID      int
	StartTime uint64 // 自系统启动以来的时钟滴答数，用来识别 pid 是否被复用
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
	// f[0]=state f[1]=ppid f[2]=pgrp ... f[19]=starttime（即 stat 的第 22 个字段）
	if len(f) < 20 || len(f[0]) == 0 {
		return st, errors.New("stat 字段不足")
	}
	st.PID = pid
	st.State = f[0][0]
	st.PPID, _ = strconv.Atoi(f[1])
	st.PGID, _ = strconv.Atoi(f[2])
	st.StartTime, err = strconv.ParseUint(f[19], 10, 64)
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
