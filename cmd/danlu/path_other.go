//go:build !linux

package main

// 服务器只在 Linux 上安装，其他平台保留编译和单元测试能力。
func setupCommandPath(exe string) {}
