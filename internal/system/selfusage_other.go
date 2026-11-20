//go:build !linux

package system

import "io/fs"

// 非 Linux 开发机拿不到 inode，不做硬链接去重；服务只跑在 Linux，正确性由 linux 测试覆盖。
type hardlinkKey struct{}

func hardlinkID(fs.FileInfo) (hardlinkKey, bool) { return hardlinkKey{}, false }
