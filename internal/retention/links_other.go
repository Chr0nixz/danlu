//go:build !unix

package retention

import "io/fs"

// 拿不到 inode 的平台不做硬链接去重；服务只跑在 Linux
type linkKey struct{}

func linkID(fs.FileInfo) (linkKey, uint64, bool) { return linkKey{}, 0, false }
