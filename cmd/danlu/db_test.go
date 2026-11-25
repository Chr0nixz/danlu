package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danlu/internal/config"
	"danlu/internal/store"
	"danlu/internal/tlsutil"
)

// setVersion 直接改库的 user_version，模拟旧版本的库（只测要不要迁移，不真的跑迁移）
func setVersion(path string, v int) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer db.Close()
	_, err = db.Exec(fmt.Sprintf("PRAGMA user_version = %d", v))
	return err
}

func dbVersion(t *testing.T, path string) int {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	if err := db.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// 命令行只建新库，已有的旧库一律不升级：有没有服务在跑都一样，重启的间隙也看不出服务是哪个版本
func TestOpenStoreRefusesOldDatabase(t *testing.T) {
	p := config.PathsFor(t.TempDir())
	st, err := openStore(p)
	if err != nil {
		t.Fatalf("还没有库时应建库: %v", err)
	}
	st.Close()
	want := dbVersion(t, p.DB)
	if want == 0 {
		t.Fatal("新库应已迁移到当前版本")
	}

	if err := setVersion(p.DB, want-1); err != nil {
		t.Fatal(err)
	}
	_, err = openStore(p)
	var old *store.OutdatedError
	if !errors.As(err, &old) || !strings.Contains(err.Error(), "danlu restart") {
		t.Fatalf("已有的旧库应拒绝，并提示重启服务: %v", err)
	}
	if v := dbVersion(t, p.DB); v != want-1 {
		t.Fatalf("命令行不能升级已有的库: user_version=%d", v)
	}
	if backups, _ := filepath.Glob(filepath.Join(p.Home, "backup", "*.db")); len(backups) != 0 {
		t.Fatalf("不迁移也就不备份: %v", backups)
	}

	// 服务启动时升级完以后照常打开
	if err := setVersion(p.DB, want); err != nil {
		t.Fatal(err)
	}
	st, err = openStore(p)
	if err != nil {
		t.Fatalf("库是当前版本时应能打开: %v", err)
	}
	st.Close()
}

// init --force 遇到打不开的库时什么都不改：CA 换掉了，正在运行的服务会加载新证书，所有设备失去信任
func TestInitForceLeavesCAWhenDatabaseOld(t *testing.T) {
	home := t.TempDir()
	t.Setenv("DANLU_HOME", home)
	args := []string{"--yes", "--no-qr", "--public-ip", "127.0.0.1", "--password", "danlu-test-123"}
	if err := cmdInit(args); err != nil {
		t.Fatalf("第一次 init: %v", err)
	}
	p := config.PathsFor(home)
	ca := tlsutil.FilesIn(p.TLS).CACert
	before, err := os.ReadFile(ca)
	if err != nil {
		t.Fatal(err)
	}
	conf, err := os.ReadFile(p.Config)
	if err != nil {
		t.Fatal(err)
	}
	if err := setVersion(p.DB, dbVersion(t, p.DB)-1); err != nil {
		t.Fatal(err)
	}

	err = cmdInit(append([]string{"--force"}, args...))
	var old *store.OutdatedError
	if !errors.As(err, &old) {
		t.Fatalf("库是旧版本时 init --force 应失败: %v", err)
	}
	if after, _ := os.ReadFile(ca); !bytes.Equal(before, after) {
		t.Fatal("init --force 失败了却换掉了 CA")
	}
	if after, _ := os.ReadFile(p.Config); !bytes.Equal(conf, after) {
		t.Fatal("init --force 失败了却改了配置文件")
	}
}
