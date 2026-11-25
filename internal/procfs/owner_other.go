//go:build !linux

package procfs

func Owner(pid int) string { return "" }

// OwnerUID 在没有 /proc 的平台上一律当成进程不在
func OwnerUID(pid int) (uid int, exists bool) { return -1, false }
