//go:build !linux

package procfs

func Owner(pid int) string { return "" }
