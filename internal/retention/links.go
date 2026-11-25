package retention

import "io/fs"

// links 处理硬链接的计量。快照重跑在 snapshots/ 里给源快照建硬链接（同一份内容挂在两个任务名下），
// 逐个目录项累加会把它算两遍：总量偏大，max_total_gb 会提前清理；而删掉一个还有别的链接的文件
// 腾不出空间，也不能从余量里扣。
//
//   - total：统计总量时同一个 inode 只计一次（和资源页、du 的口径一致）；
//   - freed：算清掉一个任务能腾出多少时，只有删的是最后一个链接才计。预览不真删，
//     所以自己记着每个 inode 还剩几个链接：一个任务算进清理清单（commit）才扣，没算进去（reset）就作废。
//
// 拿不到 inode 的平台（Windows 开发机）按文件大小计，和以前一样。
type links struct {
	counted map[linkKey]bool
	left    map[linkKey]uint64
	pending map[linkKey]uint64
}

func newLinks() *links {
	return &links{counted: map[linkKey]bool{}, left: map[linkKey]uint64{}, pending: map[linkKey]uint64{}}
}

func (l *links) total(fi fs.FileInfo) int64 {
	k, n, ok := linkID(fi)
	if !ok || n <= 1 {
		return fi.Size()
	}
	if l.counted[k] {
		return 0
	}
	l.counted[k] = true
	return fi.Size()
}

func (l *links) freed(fi fs.FileInfo) int64 {
	k, n, ok := linkID(fi)
	if !ok || n <= 1 {
		return fi.Size()
	}
	left, seen := l.left[k]
	if !seen {
		// 第一次见到时的链接数是准的：之前的清理删链接前都经过这里，已经记在 left 上
		left = n
		l.left[k] = n
	}
	left -= min(l.pending[k], left)
	l.pending[k]++
	if left <= 1 {
		return fi.Size() // 这是最后一个链接
	}
	return 0
}

// commit 把当前任务要删的链接记下来，reset 作废。每个任务算大小之前 reset，算进清理清单时 commit
func (l *links) commit() {
	for k, n := range l.pending {
		l.left[k] -= min(n, l.left[k])
	}
	clear(l.pending)
}

func (l *links) reset() { clear(l.pending) }
