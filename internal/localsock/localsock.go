// Package localsock 是本机命令行入口（FEATURES 5.1）用的 unix socket：
// serve 在数据目录里监听 danlu.sock，danlu submit 等命令经它调用同一套 /api。
// 监听和取对端 uid 只在 Linux 上实现（*_linux.go），其他平台返回 ErrUnsupported。
package localsock

import (
	"errors"
	"path/filepath"
)

// Name 是 socket 在数据目录里的文件名。
const Name = "danlu.sock"

// MaxPath 是 sockaddr_un.sun_path 能放下的最长路径（108 字节含结尾的 NUL）。
const MaxPath = 107

// ErrUnsupported：当前平台没有命令行入口（只在 Linux 上提供）。
var ErrUnsupported = errors.New("命令行入口只在 Linux 上提供")

// Path 返回数据目录里的 socket 路径。
func Path(home string) string { return filepath.Join(home, Name) }
