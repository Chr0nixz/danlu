//go:build !linux

package system

import "errors"

func diskStats(string) (string, uint64, uint64, error) {
	return "", 0, 0, errors.New("磁盘采集只支持 Linux")
}
