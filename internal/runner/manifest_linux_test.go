package runner

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// 一个输出目录的 danlu.json 读不动（这里用 FIFO 模拟卡住的网络存储），别的目录照常写；
// 同一目录的后续写入排在它后面
func TestStuckManifestOnlyBlocksItsOwnDirectory(t *testing.T) {
	r := &Runner{}
	stuck, free := filepath.Join(t.TempDir(), "stuck"), filepath.Join(t.TempDir(), "free")
	if err := os.MkdirAll(stuck, 0o700); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(stuck, "danlu.json")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	data := []byte(`{"updated_at":1}`)
	first, second := make(chan error, 1), make(chan error, 1)
	go func() { first <- r.WriteManifest(stuck, data) }()
	time.Sleep(100 * time.Millisecond) // 让第一次写入先卡在打开 FIFO 上
	go func() { second <- r.WriteManifest(stuck, data) }()

	done := make(chan error, 1)
	go func() { done <- r.WriteManifest(free, data) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("别的输出目录被卡住的目录挡住了")
	}
	select {
	case <-first:
		t.Fatal("读 FIFO 的写入不应该已经返回")
	case <-second:
		t.Fatal("同一目录的写入应该排在卡住的那次后面")
	default:
	}

	// 打开写端再关掉，读端读到 EOF，第一次写入用普通文件替换 FIFO
	w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	for _, ch := range []chan error{first, second} {
		select {
		case err := <-ch:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("解除阻塞后写入没有完成")
		}
	}
	if fi, err := os.Lstat(fifo); err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("清单应该换成普通文件: %v %v", fi, err)
	}
	r.manifestMu.Lock()
	left := len(r.manifestLocks)
	r.manifestMu.Unlock()
	if left != 0 {
		t.Fatalf("用完的目录锁没有删掉: %d", left)
	}
}
