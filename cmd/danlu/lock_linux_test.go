package main

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestLockHomeExcludesSecondServe(t *testing.T) {
	home := t.TempDir()
	unlock, err := lockHome(home)
	if err != nil {
		t.Fatal(err)
	}
	if another, err := lockHome(home); err == nil {
		another()
		t.Fatal("同一个数据目录取得了两把锁")
	} else if !strings.Contains(err.Error(), "pid "+strconv.Itoa(os.Getpid())) {
		t.Fatalf("报错应该说明占用者的 pid: %v", err)
	}
	unlock()
	unlock, err = lockHome(home)
	if err != nil {
		t.Fatalf("释放后应该能重新加锁: %v", err)
	}
	unlock()
}
