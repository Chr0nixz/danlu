package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"danlu/internal/execx"
)

// 从服务的主进程定位程序，不依赖 PATH、当前目录或 DANLU_HOME。
func installedExecutable() (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := execx.Command(ctx, "systemctl", "--user", "show", "danlu", "--property=MainPID", "--value")
	out, err := execx.Output(cmd)
	if err != nil {
		return "", fmt.Errorf("读取 danlu 用户服务失败: %w；可用 ./danlu.new replace <原程序路径> 手动指定", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil || pid <= 0 {
		return "", fmt.Errorf("danlu 用户服务没有运行；请先启动服务，或用 ./danlu.new replace <原程序路径> 手动指定")
	}
	exe, err := os.Readlink(filepath.Join("/proc", strconv.Itoa(pid), "exe"))
	if err != nil {
		return "", fmt.Errorf("读取 danlu 进程 %d 的程序路径失败: %w", pid, err)
	}
	if !filepath.IsAbs(exe) || strings.HasSuffix(exe, " (deleted)") {
		return "", fmt.Errorf("运行中的 danlu 程序已被移走，无法自动定位；请用 ./danlu.new replace <原程序路径> 手动指定")
	}
	return exe, nil
}
