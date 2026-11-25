package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestPrependFile(t *testing.T) {
	dir := t.TempDir()
	rc := filepath.Join(dir, ".bashrc")
	if err := os.WriteFile(rc, []byte("# 用户配置\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := prependFile(rc, []byte("# 块\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(rc); string(b) != "# 块\n# 用户配置\n" {
		t.Fatalf("内容 = %q", b)
	}
	if runtime.GOOS != "windows" {
		if fi, _ := os.Stat(rc); fi.Mode().Perm() != 0o600 {
			t.Fatalf("权限应保持 600: %v", fi.Mode().Perm())
		}
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Fatalf("不应留下临时文件: %v", entries)
	}

	// 不存在时新建
	fresh := filepath.Join(dir, ".zshenv")
	if err := prependFile(fresh, []byte("# 块\n")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); string(b) != "# 块\n" {
		t.Fatalf("新建的内容 = %q", b)
	}
}

// dotfiles 管理工具把 ~/.bashrc 链到仓库里：改链接指向的文件，链接本身不能被换成普通文件
func TestPrependFileFollowsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上建符号链接要权限")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "bashrc")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("# 仓库里的配置\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rc := filepath.Join(dir, ".bashrc")
	if err := os.Symlink(real, rc); err != nil {
		t.Fatal(err)
	}
	if err := prependFile(rc, []byte("# 块\n")); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Lstat(rc); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf(".bashrc 应还是符号链接: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(real); string(b) != "# 块\n# 仓库里的配置\n" {
		t.Fatalf("应改链接指向的文件: %q", b)
	}

	// 指向不存在的文件：报错，不在链接的位置新建普通文件
	dangling := filepath.Join(dir, ".profile")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := prependFile(dangling, []byte("# 块\n")); err == nil {
		t.Fatal("悬空链接应报错")
	}
	if fi, err := os.Lstat(dangling); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("悬空链接不应被替换: %v %v", fi, err)
	}
}
