package main

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"danlu/internal/atomicfile"
)

// prependFile 把 block 加到 path 开头，原子替换：先写同目录的临时文件再改名，磁盘或配额满时原文件不受影响
// （截断重写的话截断成功、写入失败，用户的 .bashrc 就空了）。文件不存在时新建。
//
// 原文件的权限照旧。path 是符号链接（stow、chezmoi 这类 dotfiles 管理工具）时改它指向的文件，
// 不能把链接换成普通文件。
func prependFile(path string, block []byte) error {
	target := path
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		if target, err = filepath.EvalSymlinks(path); err != nil {
			return fmt.Errorf("%s 是符号链接，但找不到它指向的文件: %w", path, err)
		}
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	perm := fs.FileMode(0o644)
	content, err := os.ReadFile(target)
	switch {
	case err == nil:
		fi, err := os.Stat(target)
		if err != nil {
			return err
		}
		perm = fi.Mode().Perm()
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	data := make([]byte, 0, len(block)+len(content))
	return atomicfile.Write(target, append(append(data, block...), content...), perm)
}
