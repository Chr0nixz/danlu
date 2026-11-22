package strict

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestMonitor(t *testing.T) {
	var ok, enabled atomic.Bool
	var checks, changes atomic.Int32
	enabled.Store(true)
	m := NewMonitor(func(context.Context) Status {
		checks.Add(1)
		if ok.Load() {
			return Status{OK: true}
		}
		return Status{Reason: ReasonNoSudo}
	}, enabled.Load, func() { changes.Add(1) })
	m.Interval = 20 * time.Millisecond
	if st := m.Status(); st.OK || st.Reason != ReasonChecking {
		t.Fatalf("没查过之前不能算可用: %+v", st)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	waitFor(t, func() bool { return m.Status().Reason == ReasonNoSudo })
	ok.Store(true)
	waitFor(t, func() bool { return m.Status().OK })
	n := checks.Load()
	time.Sleep(80 * time.Millisecond)
	if checks.Load() != n {
		t.Fatal("可用以后不应再定时查（每次 sudo 都会写 auth.log）")
	}

	// 启动失败：马上标成不可用，后台随即复查
	ok.Store(false)
	m.Fail(&Error{ReasonProtocol, "助手协议 2"})
	if st := m.Status(); st.OK || st.Reason == "" {
		t.Fatalf("启动失败后应立即不可用: %+v", st)
	}
	waitFor(t, func() bool { return checks.Load() > n })
	if m.Status().Reason != ReasonNoSudo {
		t.Fatalf("复查结果应覆盖失败原因: %+v", m.Status())
	}
	if changes.Load() < 3 {
		t.Fatalf("状态变化应叫醒调度器: %d 次", changes.Load())
	}

	// 关掉开关后不再复查
	enabled.Store(false)
	m.Kick()
	time.Sleep(30 * time.Millisecond)
	n = checks.Load()
	time.Sleep(80 * time.Millisecond)
	if checks.Load() != n {
		t.Fatal("关闭后不应再复查")
	}
	if r, _ := reasonOf(errors.New("x")); r != ReasonFailed {
		t.Fatal("普通错误算 failed")
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("等待超时")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
