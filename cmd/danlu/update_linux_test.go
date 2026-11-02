package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fakeSystemctl(t *testing.T, output string, exitCode int) {
	t.Helper()
	bin := t.TempDir()
	script := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' '%s'\nexit %d\n", output, exitCode)
	if err := os.WriteFile(filepath.Join(bin, "systemctl"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func TestInstalledExecutableIgnoresPathAndDataDir(t *testing.T) {
	fakeSystemctl(t, strconv.Itoa(os.Getpid()), 0)
	t.Setenv("DANLU_HOME", t.TempDir())
	want, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	got, err := installedExecutable()
	if err != nil || got != want {
		t.Fatalf("应按运行中进程定位，不能依赖 PATH 或数据目录: %q, %v", got, err)
	}
}

func TestInstalledExecutableFailure(t *testing.T) {
	for _, tc := range []struct {
		name, output, message string
		exitCode              int
	}{
		{"服务未启动", "0", "没有运行", 0},
		{"响应为空", "", "没有运行", 0},
		{"响应不是进程号", "invalid", "没有运行", 0},
		{"进程已经退出", "2147483647", "程序路径失败", 0},
		{"systemctl 失败", "", "读取 danlu 用户服务失败", 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeSystemctl(t, tc.output, tc.exitCode)
			got, err := installedExecutable()
			if got != "" || err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("应清楚报告定位失败: %q, %v", got, err)
			}
		})
	}
}
