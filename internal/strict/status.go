package strict

import (
	"context"
	"errors"
	"sync"
	"time"
)

// 助手不可用的原因代码。推送和面板按代码出文案，不转发原始错误。
const (
	ReasonChecking     = "checking"      // 还没查过
	ReasonNotInstalled = "not_installed" // 没有 HelperPath
	ReasonNoSudo       = "no_sudo"       // sudo -n 被拒绝（没有 sudoers 规则、要密码）
	ReasonProtocol     = "protocol"      // 助手和 danlu 的协议版本不一致
	ReasonFailed       = "failed"        // 其他错误，详情在 Detail
	ReasonUnsupported  = "unsupported"   // 不是 Linux
)

// Status 是助手的可用状态。Detail 是原始错误，只给设置页和日志看。
type Status struct {
	OK            bool   `json:"ok"`
	Reason        string `json:"reason,omitempty"`
	Detail        string `json:"detail,omitempty"`
	HelperVersion string `json:"helper_version,omitempty"`
	CheckedAt     int64  `json:"checked_at,omitempty"`
}

// Error 是调用助手失败，Reason 同 Status.Reason。
type Error struct{ Reason, Detail string }

func (e *Error) Error() string { return "严格限卡不可用（" + e.Reason + "）：" + e.Detail }

func reasonOf(err error) (string, string) {
	var e *Error
	if errors.As(err, &e) {
		return e.Reason, e.Detail
	}
	return ReasonFailed, err.Error()
}

// Monitor 缓存助手的可用状态，调度器每轮读它，不在锁里执行 sudo。
// 每次 sudo 都会在 auth.log 留一行，所以不定时轮询：开启时启动后查一次、打开开关时查一次、
// 启动失败后马上复查；不可用期间每 Interval 复查，恢复后通过 onChange 叫醒调度器。
type Monitor struct {
	Interval time.Duration
	check    func(context.Context) Status
	enabled  func() bool
	onChange func()
	wake     chan struct{}

	mu sync.Mutex
	st Status
}

func NewMonitor(check func(context.Context) Status, enabled func() bool, onChange func()) *Monitor {
	return &Monitor{Interval: 2 * time.Minute, check: check, enabled: enabled, onChange: onChange,
		wake: make(chan struct{}, 1), st: Status{Reason: ReasonChecking}}
}

func (m *Monitor) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

func (m *Monitor) set(st Status) {
	m.mu.Lock()
	changed := m.st.OK != st.OK || m.st.Reason != st.Reason
	m.st = st
	m.mu.Unlock()
	if changed && m.onChange != nil {
		m.onChange()
	}
}

// Recheck 同步查一次，最多 20 秒。
func (m *Monitor) Recheck(ctx context.Context) Status {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	st := m.check(ctx)
	m.set(st)
	return st
}

// Fail 记下一次启动失败：先标成不可用（调度器随即停止启动任务），再叫后台马上复查。
func (m *Monitor) Fail(err error) {
	reason, detail := reasonOf(err)
	m.set(Status{Reason: reason, Detail: detail, CheckedAt: time.Now().Unix()})
	m.Kick()
}

// Kick 叫后台马上看一眼（比如刚打开开关）。
func (m *Monitor) Kick() {
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// Run 是后台循环，ctx 结束时返回。
func (m *Monitor) Run(ctx context.Context) {
	for {
		if m.enabled() && !m.Status().OK {
			m.Recheck(ctx)
		}
		var retry <-chan time.Time
		if m.enabled() && !m.Status().OK {
			retry = time.After(m.Interval)
		}
		select {
		case <-ctx.Done():
			return
		case <-m.wake:
		case <-retry:
		}
	}
}
