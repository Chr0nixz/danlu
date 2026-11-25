package localsock

import (
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"
)

// Listen 在 path 上监听，权限 0600。调用方必须已经拿到 serve.lock：
// 先删掉上次没清理的 socket 文件，有锁在，删掉的不可能是另一个实例正在用的。
// 新建的数据目录是 0700（已有的目录不会被改权限）；即使 chmod 之前的空隙里有别的用户连进来，
// 也会被 api.LocalHandler 按对端 uid 挡掉，权限只是第一道门。
func Listen(path string) (net.Listener, error) {
	if len(path) > MaxPath {
		return nil, fmt.Errorf("socket 路径 %d 字节，超过 unix socket 的上限 %d 字节：%s", len(path), MaxPath, path)
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&os.ModeSocket == 0 {
			return nil, fmt.Errorf("%s 已存在且不是 socket，不删除", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	return ln, nil
}

// PeerUID 用 SO_PEERCRED 取 unix socket 对端进程的 uid。
func PeerUID(c net.Conn) (int, bool) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, false
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, false
	}
	var cred *syscall.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	}); err != nil || credErr != nil || cred == nil {
		return 0, false
	}
	return int(cred.Uid), true
}
