//go:build linux

package system

import (
	"io/fs"
	"syscall"
)

// hardlinkKey 用设备号+inode 识别硬链接，整次统计里同一条只计一次（du 的口径）。
type hardlinkKey struct{ dev, ino uint64 }

func hardlinkID(fi fs.FileInfo) (hardlinkKey, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || st.Nlink <= 1 {
		return hardlinkKey{}, false
	}
	return hardlinkKey{dev: uint64(st.Dev), ino: st.Ino}, true
}
