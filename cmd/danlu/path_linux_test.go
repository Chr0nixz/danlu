package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandPathWorksInNoninteractiveBash(t *testing.T) {
	home := t.TempDir()
	exe := filepath.Join(home, "程序 '目录", "danlu")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := "case $- in *i*) ;; *) return ;; esac\n# 用户配置\n"
	rc := filepath.Join(home, ".bashrc")
	if err := os.WriteFile(rc, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ensureCommandPath(home, "/bin/bash", "", exe); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(rc)
	if err != nil || string(content) != commandPathBlock+original {
		t.Fatalf("应保留用户配置且不重复写入: %q, %v", content, err)
	}
	info, err := os.Stat(rc)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("配置文件权限不应改变: %v, %v", info, err)
	}
	cmd := exec.Command("/bin/bash", "--noprofile", "--norc", "-c", `. "$HOME/.bashrc"; command -v danlu`)
	cmd.Env = append(os.Environ(), "HOME="+home, "PATH=/usr/bin:/bin")
	out, err := cmd.CombinedOutput()
	link := filepath.Join(home, ".local", "bin", "danlu")
	if err != nil || strings.TrimSpace(string(out)) != link {
		t.Fatalf("非交互 shell 应能找到命令: %q, %v", out, err)
	}
	target, err := filepath.EvalSymlinks(link)
	if err != nil || target != exe {
		t.Fatalf("命令应指向实际程序: %q, %v", target, err)
	}
}

func TestCommandPathLoginAndZsh(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".bash_profile")
	if err := os.WriteFile(profile, []byte("# 已有登录配置"), 0o644); err != nil {
		t.Fatal(err)
	}
	zdotdir := t.TempDir()
	exe := filepath.Join(home, ".local", "bin", "danlu")
	if err := ensureCommandPath(home, "/bin/zsh", zdotdir, exe); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{profile, filepath.Join(zdotdir, ".zshenv")} {
		content, err := os.ReadFile(path)
		if err != nil || !strings.HasPrefix(string(content), commandPathBlock) {
			t.Fatalf("登录配置 %s 未加入 PATH: %q, %v", path, content, err)
		}
	}
	if _, err := os.Lstat(exe); !os.IsNotExist(err) {
		t.Fatalf("程序本来就在目标路径时不能创建自指软链接: %v", err)
	}
}

func TestCommandPathPreservesExistingCommand(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		t.Run(map[bool]string{false: "普通文件", true: "其他软链接"}[symlink], func(t *testing.T) {
			home := t.TempDir()
			bin := filepath.Join(home, ".local", "bin")
			if err := os.MkdirAll(bin, 0o755); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(bin, "danlu")
			original := filepath.Join(home, "原程序")
			if err := os.WriteFile(original, []byte("原内容"), 0o755); err != nil {
				t.Fatal(err)
			}
			var err error
			if symlink {
				err = os.Symlink(original, link)
			} else {
				err = os.WriteFile(link, []byte("原内容"), 0o755)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := ensureCommandPath(home, "/bin/bash", "", filepath.Join(home, "新程序")); err == nil {
				t.Fatal("应拒绝覆盖已有的其他命令")
			}
			content, err := os.ReadFile(link)
			if err != nil || string(content) != "原内容" {
				t.Fatalf("已有命令被改写: %q, %v", content, err)
			}
		})
	}
}
