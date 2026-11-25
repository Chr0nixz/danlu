package main

import (
	"os"
	"strings"
	"testing"

	"danlu/internal/config"
)

// 服务占着数据目录时：当前版本的库照常打开；还没建表的库不由命令行建，免得和服务同时迁移
func TestOpenStoreWhileServeHoldsLock(t *testing.T) {
	p := config.PathsFor(t.TempDir())
	st, err := openStore(p)
	if err != nil {
		t.Fatal(err)
	}
	st.Close()
	free, err := lockHome(p.Home)
	if err != nil {
		t.Fatalf("建完库应释放数据目录的锁: %v", err)
	}
	free()

	unlock, err := lockHome(p.Home) // 假装 serve 在运行
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	st, err = openStore(p)
	if err != nil {
		t.Fatalf("服务在运行时，当前版本的库应能打开: %v", err)
	}
	st.Close()

	for _, f := range []string{p.DB, p.DB + "-wal", p.DB + "-shm"} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	if _, err := openStore(p); err == nil || !strings.Contains(err.Error(), "新建数据库要先独占数据目录") {
		t.Fatalf("服务占着数据目录时不应建库: %v", err)
	}
	if v := dbVersion(t, p.DB); v != 0 {
		t.Fatalf("没拿到锁却建了表: user_version=%d", v)
	}
}
