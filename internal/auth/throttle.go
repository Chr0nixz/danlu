package auth

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrLoginBusy 表示全局登录失败太多，已经在放慢，排队也要等太久。
var ErrLoginBusy = errors.New("登录尝试过多，请稍后再试")

// 按 IP 计数挡不住换着 IP 撞密码。所有 IP 加起来最近一分钟失败超过 globalSlowAfter 次时，
// 每次密码校验之间至少隔 slowGap，全局每分钟最多猜几十次；排在 maxSlowWait 之后的直接拒绝，
// 不让请求一直挂着。正常使用时远到不了这个量，不受影响。
const (
	globalSlowAfter = 30
	slowGap         = time.Second
	maxSlowWait     = 15 * time.Second
	// bcryptSlots 是同时进行的 bcrypt 校验个数上限：cost 12 每次要几百毫秒 CPU，
	// 并发的登录请求不能把 CPU 占满，挤到正在跑的训练和调度。
	bcryptSlots = 2
)

type loginThrottle struct {
	slowAfter int
	gap       time.Duration
	maxWait   time.Duration

	mu       sync.Mutex
	failures []time.Time // 最近一分钟内的失败时刻，最多留 slowAfter 个
	next     time.Time   // 放慢时，下一次允许校验密码的时刻
}

func newLoginThrottle() *loginThrottle {
	return &loginThrottle{slowAfter: globalSlowAfter, gap: slowGap, maxWait: maxSlowWait}
}

func (t *loginThrottle) trim(now time.Time) {
	i := 0
	for i < len(t.failures) && now.Sub(t.failures[i]) >= time.Minute {
		i++
	}
	t.failures = t.failures[i:]
}

// fail 记一次失败（任何 IP）。
func (t *loginThrottle) fail() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	t.trim(now)
	t.failures = append(t.failures, now)
	if len(t.failures) > t.slowAfter {
		t.failures = t.failures[len(t.failures)-t.slowAfter:]
	}
}

// wait 在全局失败过多时排到下一个空档再返回；要等超过 maxWait 时返回 ErrLoginBusy，ctx 结束时返回它的错误。
func (t *loginThrottle) wait(ctx context.Context) error {
	t.mu.Lock()
	now := time.Now()
	t.trim(now)
	if len(t.failures) < t.slowAfter {
		t.mu.Unlock()
		return nil
	}
	at := t.next
	if at.Before(now) {
		at = now
	}
	if at.Sub(now) > t.maxWait {
		t.mu.Unlock()
		return ErrLoginBusy
	}
	t.next = at.Add(t.gap)
	t.mu.Unlock()
	d := time.Until(at)
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
