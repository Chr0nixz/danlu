package main

import (
	"errors"
	"fmt"
	"os"

	"danlu/internal/config"
	"danlu/internal/store"
)

// openStore 是除 serve 以外的命令打开数据库的唯一入口。库已是当前版本时直接打开；
// 还没建表的新库（第一次 init）占着 serve.lock 在这里建好；已有的旧库一律不迁移，只由服务启动时升级。
// 命令行不去猜此刻有没有服务、是哪个版本：重启的间隙、停着的时候都看不出来，
// 替旧版服务升级了库，它下次启动就会因为库比它新而起不来。
func openStore(p config.Paths) (*store.Store, error) {
	st, fresh, err := openCurrent(p)
	if !fresh {
		return st, err
	}
	unlock, err := lockHome(p.Home)
	if err != nil {
		return nil, fmt.Errorf("新建数据库要先独占数据目录：%w", err)
	}
	defer unlock() // 建库期间占着锁，正好启动的 serve 会退出等 systemd 重试，不会和这里同时迁移
	// 拿到锁再看一次：抢锁之前可能已经有服务把库建好了
	if st, fresh, err = openCurrent(p); !fresh {
		return st, err
	}
	return store.Open(p.DB)
}

// openCurrent 打开当前版本的库。fresh 表示库还没建表，可以由命令行新建；已有的旧库返回说明怎么办的错误。
func openCurrent(p config.Paths) (st *store.Store, fresh bool, err error) {
	st, err = store.OpenCurrent(p.DB)
	var old *store.OutdatedError
	if !errors.As(err, &old) {
		return st, false, err
	}
	if old.Have == 0 {
		return nil, true, nil
	}
	self, _ := os.Executable()
	// 磁盘上的服务程序已经是新版、进程还是旧的（replace --no-restart、手工换文件、replace 换完后重启失败），
	// 重启就会升级；再执行一次 replace 只会报"新程序和要替换的是同一个文件"
	return nil, false, fmt.Errorf("%w。命令行不升级已有的数据库，只在服务用新程序启动时升级："+
		"服务程序已经换成这个版本（replace 之后没重启成功，或者手工换了文件）就执行 danlu restart；"+
		"服务用的是别的 danlu，就改用那个 danlu 执行这条命令，或者先用 %s replace <服务用的 danlu 路径> 升级服务", old, self)
}
