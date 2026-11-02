package system

import (
	"os"
	"strconv"
	"syscall"
)

func diskStats(path string) (string, uint64, uint64, error) {
	path, err := existingParent(path)
	if err != nil {
		return "", 0, 0, err
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", 0, 0, err
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(path, &fs); err != nil {
		return "", 0, 0, err
	}
	return strconv.FormatUint(uint64(fi.Sys().(*syscall.Stat_t).Dev), 10), fs.Blocks * uint64(fs.Bsize), fs.Bavail * uint64(fs.Bsize), nil
}
