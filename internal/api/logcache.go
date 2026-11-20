package api

import (
	"os"
	"sync"
	"time"

	"danlu/internal/logs"
)

// logLineCache 缓存任务列表里每个任务日志的最后一行和报错行，按文件大小和修改时间判断是否过期。
// 列表每 3 秒拉一次，最多上百个任务；结束的任务日志不再变，命中缓存时只多一次 stat，
// 不用每次再读 8KB 的尾部、失败任务再读 32KB。运行中的任务日志在变，照旧每次重读。
type logLineCache struct {
	mu sync.Mutex
	m  map[string]logLineEntry
}

type logLineEntry struct {
	size    int64
	mod     time.Time
	last    string
	errLine string
	hasErr  bool // 已经找过报错行（只有失败的任务要找）
}

// logLineCacheMax 是缓存的日志个数上限，超过时随便丢掉一半。
const logLineCacheMax = 1024

// lines 返回日志的最后一行；wantErr 时再返回最后一条报错行。日志不存在时都是空串。
func (c *logLineCache) lines(path string, wantErr bool) (last, errLine string) {
	fi, err := os.Stat(path)
	if err != nil {
		return "", ""
	}
	c.mu.Lock()
	e, ok := c.m[path]
	c.mu.Unlock()
	fresh := ok && e.size == fi.Size() && e.mod.Equal(fi.ModTime())
	if !fresh {
		e = logLineEntry{size: fi.Size(), mod: fi.ModTime(), last: logs.LastLine(path)}
	}
	if wantErr && !e.hasErr {
		e.errLine, e.hasErr = logs.ErrorLine(path), true
	}
	if !fresh || wantErr {
		c.mu.Lock()
		if c.m == nil {
			c.m = map[string]logLineEntry{}
		}
		if _, exists := c.m[path]; !exists && len(c.m) >= logLineCacheMax {
			for k := range c.m {
				delete(c.m, k)
				if len(c.m) <= logLineCacheMax/2 {
					break
				}
			}
		}
		c.m[path] = e
		c.mu.Unlock()
	}
	if !wantErr {
		return e.last, ""
	}
	return e.last, e.errLine
}
