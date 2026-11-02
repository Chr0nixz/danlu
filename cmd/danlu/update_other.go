//go:build !linux

package main

import "errors"

func installedExecutable() (string, error) {
	return "", errors.New("update 只能在运行 danlu 服务的 Linux 服务器上执行")
}
