//go:build unix

package retention

import (
	"io/fs"
	"syscall"
)

// linkKey 用设备号加 inode 识别同一个文件
type linkKey struct{ dev, ino uint64 }

// linkID 返回文件的 inode 标识和链接数
func linkID(fi fs.FileInfo) (linkKey, uint64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return linkKey{}, 0, false
	}
	return linkKey{uint64(st.Dev), uint64(st.Ino)}, uint64(st.Nlink), true
}
