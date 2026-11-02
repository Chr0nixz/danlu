//go:build !linux

package retention

import (
	"errors"
	"os"
	"sync"
)

// 非 Linux 只供单测；实际服务和清理命令在 Linux 上使用跨进程文件锁。
var testLock sync.Mutex

func lock(*os.Root) (func(), error) {
	if !testLock.TryLock() {
		return nil, errors.New("另一个清理正在运行")
	}
	return testLock.Unlock, nil
}
