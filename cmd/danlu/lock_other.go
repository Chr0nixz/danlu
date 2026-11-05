//go:build !linux

package main

// 服务只在 Linux 上运行，其他平台不加锁，只保留编译和单元测试能力。
func lockHome(string) (func(), error) { return func() {}, nil }
