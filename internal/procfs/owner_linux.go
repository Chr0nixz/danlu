package procfs

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"
	"time"
)

// uid 到用户名的解析可能走 NSS（LDAP、SSSD），一次查询能卡住几秒。
// GPU 每 2 秒、资源页每 10 秒都要查一批进程的属主，所以结果缓存在进程里：
// 成功的结果一直用；失败的按 uid 记 5 分钟后重试，避免一次 LDAP 抖动把整个进程都钉在数字 uid 上。
type uidEntry struct {
	name string
	ok   bool
	at   time.Time
}

const uidFailureTTL = 5 * time.Minute

var (
	uidMu    sync.RWMutex
	uidNames = map[string]uidEntry{}
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
	return ownerOfUID(strconv.FormatUint(uint64(st.Uid), 10))
}

func ownerOfUID(uid string) string {
	uidMu.RLock()
	entry, cached := uidNames[uid]
	uidMu.RUnlock()
	if cached && (entry.ok || time.Since(entry.at) < uidFailureTTL) {
		return entry.name
	}

	name, ok := uid, false
	if u, err := user.LookupId(uid); err == nil && u.Username != "" {
		name, ok = u.Username, true
	}
	uidMu.Lock()
	uidNames[uid] = uidEntry{name: name, ok: ok, at: time.Now()}
	uidMu.Unlock()
	return name
}
