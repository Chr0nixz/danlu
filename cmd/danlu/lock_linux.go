package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// lockHome 让一个数据目录同时只有一个 serve：第二个实例在碰数据库和任务之前就退出。
// 锁随进程退出自动释放（kill -9 也一样）；Go 打开的文件带 O_CLOEXEC，训练进程不会继承它。
func lockHome(home string) (func(), error) {
	path := filepath.Join(home, "serve.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		holder, _ := os.ReadFile(path)
		f.Close()
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, fmt.Errorf("锁定数据目录失败: %w", err)
		}
		who := "另一个 danlu"
		if pid := strings.TrimSpace(string(holder)); pid != "" {
			who += "（pid " + pid + "）"
		}
		return nil, fmt.Errorf("%s正在使用数据目录 %s；服务已在运行时用 danlu restart 重启", who, home)
	}
	// pid 只用于报错时说明是谁占着，锁本身靠 flock
	if err := f.Truncate(0); err == nil {
		_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	}
	return func() { f.Close() }, nil
}
