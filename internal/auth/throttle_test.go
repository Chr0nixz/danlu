package auth

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"danlu/internal/store"
)

// 全局失败没到上限时不放慢；到了以后每次校验之间隔 gap，排得太远的直接拒绝
func TestLoginThrottle(t *testing.T) {
	th := &loginThrottle{slowAfter: 3, gap: 50 * time.Millisecond, maxWait: 120 * time.Millisecond}
	th.fail()
	th.fail()
	start := time.Now()
	for i := 0; i < 5; i++ {
		if err := th.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d > 30*time.Millisecond {
		t.Fatalf("没到上限时不应放慢，用了 %s", d)
	}
	th.fail()
	// 已取消的 ctx 照样占位：第 1 次马上轮到，第 2、3 次排在 50、100 毫秒后，第 4 次超过 120 毫秒被拒
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	if err := th.wait(gone); err != nil {
		t.Fatalf("第 1 次应马上轮到: %v", err)
	}
	for i := 2; i <= 3; i++ {
		if err := th.wait(gone); !errors.Is(err, context.Canceled) {
			t.Fatalf("第 %d 次应排队，客户端走了就返回: %v", i, err)
		}
	}
	if err := th.wait(gone); !errors.Is(err, ErrLoginBusy) {
		t.Fatalf("排得太远应返回 ErrLoginBusy: %v", err)
	}

	// 真的会等
	th = &loginThrottle{slowAfter: 1, gap: 40 * time.Millisecond, maxWait: time.Second}
	th.fail()
	start = time.Now()
	for i := 0; i < 2; i++ {
		if err := th.wait(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if d := time.Since(start); d < 30*time.Millisecond {
		t.Fatalf("放慢时两次校验之间应至少隔 gap，实际 %s", d)
	}

	// 一分钟前的失败不算
	th = &loginThrottle{slowAfter: 1, gap: time.Hour, maxWait: time.Second}
	th.failures = []time.Time{time.Now().Add(-2 * time.Minute)}
	th.next = time.Now().Add(time.Hour)
	if err := th.wait(context.Background()); err != nil {
		t.Fatalf("过期的失败不应触发放慢: %v", err)
	}
}

// bcrypt 校验同时最多 bcryptSlots 个，其余排队；排队时客户端走了就不再等
func TestCheckPasswordConcurrencyLimit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := SetPassword(st, "secret-pw"); err != nil {
		t.Fatal(err)
	}
	a := New(st, Options{Username: "admin"})
	for i := 0; i < bcryptSlots; i++ {
		a.bcrypt <- struct{}{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if a.CheckPassword(ctx, "secret-pw") {
		t.Fatal("名额占满时不应校验")
	}
	if d := time.Since(start); d < 40*time.Millisecond {
		t.Fatalf("名额占满时应排队等待，实际 %s 就返回了", d)
	}
	<-a.bcrypt
	if !a.CheckPassword(context.Background(), "secret-pw") {
		t.Fatal("空出名额后应正常校验")
	}
	if len(a.bcrypt) != bcryptSlots-1 {
		t.Fatalf("校验完应归还名额，现在占用 %d 个", len(a.bcrypt))
	}
}
