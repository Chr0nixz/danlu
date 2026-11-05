//go:build linux

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 让 systemd 自己解析生成的 unit：程序路径带空格、$、% 时，verify 要能找到这个可执行文件。
// verify 会检查 ExecStart 的程序是否存在且可执行，所以引号或转义写错时这里会失败。
func TestRenderServiceUnitParsedBySystemd(t *testing.T) {
	analyze, err := exec.LookPath("systemd-analyze")
	if err != nil {
		t.Skip("没有 systemd-analyze")
	}
	dir := filepath.Join(t.TempDir(), "my tools 100%")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(dir, "danlu$1")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	content, err := renderServiceUnit(exe, "/home/u/.danlu", "/usr/bin:/opt/CUDA 12/bin:/x%y")
	if err != nil {
		t.Fatal(err)
	}
	unit := filepath.Join(t.TempDir(), "danlu-render-test.service")
	if err := os.WriteFile(unit, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(analyze, "verify", unit).CombinedOutput()
	// [Install] 里的 default.target 等在测试环境可能缺，只关心 ExecStart 的解析结果
	if strings.Contains(string(out), "is not executable") || strings.Contains(string(out), "Failed to parse") {
		t.Fatalf("systemd 解析 unit 出错: %v\n%s\n%s", err, out, content)
	}
}
