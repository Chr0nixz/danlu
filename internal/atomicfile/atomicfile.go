// Package atomicfile 原子地替换小文件：先写同目录下的临时文件并落盘，再改名覆盖。
// 写到一半断电或进程被杀时，目标文件要么是旧内容，要么是新内容，不会只剩半截。
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write 把 data 写到 path，权限为 perm。
func Write(path string, data []byte, perm os.FileMode) (err error) {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	f, err := os.CreateTemp(dir, "."+base+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
		}
	}()
	if err = f.Chmod(perm); err != nil {
		return err
	}
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir 让改名本身也落盘。尽力而为：Windows 上打不开目录来 fsync，忽略即可。
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	d.Close()
}
