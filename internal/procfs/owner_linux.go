package procfs

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"syscall"
)

// Owner 返回进程所属的用户名，查不到时返回 uid。
func Owner(pid int) string {
	fi, err := os.Stat(fmt.Sprintf("/proc/%d", pid))
	if err != nil {
		return ""
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return ""
	}
	uid := strconv.FormatUint(uint64(st.Uid), 10)
	if u, err := user.LookupId(uid); err == nil {
		return u.Username
	}
	return uid
}
