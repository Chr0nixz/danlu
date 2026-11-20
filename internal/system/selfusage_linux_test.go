//go:build linux

package system

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// 快照重跑会在 snapshots/ 里建硬链接：同一条内容只计一次字节和文件数。
func TestSelfUsageHardlinkDedup(t *testing.T) {
	home := t.TempDir()
	a := filepath.Join(home, "snapshots", "1.tar.gz")
	writeSized(t, a, 1000)
	b := filepath.Join(home, "snapshots", "2.tar.gz")
	if err := os.Link(a, b); err != nil {
		t.Fatal(err)
	}
	u, err := scanSelfUsage(context.Background(), home, filepath.Join(home, "danlu.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := selfPart(t, u, "snapshots")
	if p.Bytes != 1000 || p.Files != 1 {
		t.Fatalf("硬链接应只计一次: %+v", p)
	}
	if u.TotalBytes != 1000 {
		t.Fatalf("总量重复计了硬链接: %d", u.TotalBytes)
	}
}

// 运行目录里指回工作目录的符号链接不跟随：外部大文件不计入丹炉占用。
func TestSelfUsageSymlinkNotFollowed(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	big := filepath.Join(outside, "big.bin")
	writeSized(t, big, 10000)
	code := filepath.Join(home, "run", "1", "code")
	writeSized(t, filepath.Join(code, "real.py"), 50)
	if err := os.Symlink(big, filepath.Join(code, "link.py")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(code, "dirlink")); err != nil {
		t.Fatal(err)
	}
	u, err := scanSelfUsage(context.Background(), home, filepath.Join(home, "danlu.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := selfPart(t, u, "run")
	if p.Bytes >= 10000 {
		t.Fatalf("跟随了符号链接: %+v", p)
	}
	if p.Bytes < 50 || p.Files != 3 { // real.py + 两个链接自身
		t.Fatalf("链接自身应计大小: %+v", p)
	}
	if u.TotalBytes >= 10000 {
		t.Fatalf("外部文件计入了总量: %d", u.TotalBytes)
	}
}
