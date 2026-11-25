package localsock

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestListenPeerUID(t *testing.T) {
	path := Path(t.TempDir())
	ln, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	fi, err := os.Stat(path)
	if err != nil || fi.Mode()&os.ModeSocket == 0 || fi.Mode().Perm() != 0o600 {
		t.Fatalf("socket 文件 %v 权限 %v", err, fi.Mode())
	}
	got := make(chan int, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			got <- -2
			return
		}
		defer c.Close()
		uid, ok := PeerUID(c)
		if !ok {
			uid = -1
		}
		got <- uid
	}()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if uid := <-got; uid != os.Geteuid() {
		t.Fatalf("对端 uid = %d，应为 %d", uid, os.Geteuid())
	}
	// TCP 连接没有对端 uid
	tl, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()
	go func() { c, _ := net.Dial("tcp", tl.Addr().String()); _ = c }()
	tc, err := tl.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer tc.Close()
	if _, ok := PeerUID(tc); ok {
		t.Fatal("TCP 连接不应取到 uid")
	}
}

// 上次没清理的 socket 文件会被替换；同名的普通文件不删；路径太长直接报错
func TestListenStaleAndRefusals(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	old, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	old.(*net.UnixListener).SetUnlinkOnClose(false)
	old.Close() // 模拟进程被杀：文件留下，没人监听
	ln, err := Listen(path)
	if err != nil {
		t.Fatalf("残留的 socket 文件应被替换: %v", err)
	}
	ln.Close()

	file := filepath.Join(dir, "plain")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Listen(file); err == nil {
		t.Fatal("同名的普通文件不应被删掉")
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatalf("普通文件不见了: %v", err)
	}
	long := filepath.Join(dir, strings.Repeat("d", MaxPath), Name)
	if _, err := Listen(long); err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("路径太长应报错: %v", err)
	}
}
