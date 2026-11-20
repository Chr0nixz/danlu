package scheduler

import (
	"context"
	"slices"
	"sync"
	"testing"
	"time"

	"danlu/internal/snapshot"
	"danlu/internal/store"
)

// liveSource 模拟面板上的设置：测试里随时改，调度器下一轮读到
type liveSource struct {
	mu sync.Mutex
	v  LiveOptions
}

func (l *liveSource) get() LiveOptions {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.v
}

func (l *liveSource) set(f func(*LiveOptions)) {
	l.mu.Lock()
	f(&l.v)
	l.mu.Unlock()
}

func attachLive(h *harness, v LiveOptions) *liveSource {
	l := &liveSource{v: v}
	h.m.live = l.get
	return l
}

func baseLive() LiveOptions {
	return LiveOptions{KillGrace: time.Hour, Snapshot: snapshot.Options{MaxFile: 1 << 20, MaxTotal: 50 << 20}}
}

// 取消途中在面板上缩短宽限期，下一轮就按新值强杀
func TestLiveKillGrace(t *testing.T) {
	h := newHarness(t, 1)
	live := attachLive(h, baseLive())
	a := h.add(1)
	h.tick()
	if _, err := h.m.Cancel(a); err != nil {
		t.Fatal(err)
	}
	h.tick()
	if len(h.ex.kills) != 0 {
		t.Fatal("宽限期内不应该强杀")
	}
	live.set(func(l *LiveOptions) { l.KillGrace = time.Millisecond })
	time.Sleep(5 * time.Millisecond)
	h.tick()
	if !slices.Equal(h.ex.kills, []int64{a}) {
		t.Fatalf("改短宽限期后应该强杀: %v", h.ex.kills)
	}
}

// 面板上关掉快速失败暂停，之后的快速失败不再暂停队列
func TestLiveFailFastToggle(t *testing.T) {
	h := newHarness(t, 1)
	v := baseLive()
	v.FailFast = 2 * time.Minute
	live := attachLive(h, v)
	live.set(func(l *LiveOptions) { l.FailFast = 0 })
	a, next := h.add(1), h.add(1)
	h.tick()
	h.ex.finish(a, 1)
	h.tick()
	h.tick()
	h.expect(a, store.StatusFailed)
	h.expect(next, store.StatusRunning)
	if state, err := h.m.QueueState(); err != nil || state.Paused {
		t.Fatalf("关闭后不应暂停: %+v %v", state, err)
	}
}

// 调高暂停线立即暂停；调到 0（关闭）自动恢复
func TestLiveDiskMin(t *testing.T) {
	h := newHarness(t, 1)
	disk := attachDisk(h)
	v := baseLive()
	v.DiskMinBytes = 2 << 30
	live := attachLive(h, v)
	live.set(func(l *LiveOptions) { l.DiskMinBytes = 10 << 30 })
	next := h.add(1)
	h.tick()
	h.expect(next, store.StatusQueued)
	if state, err := h.m.QueueState(); err != nil || !state.Paused || state.Kind != "disk" {
		t.Fatalf("调高暂停线后应暂停: %+v %v", state, err)
	}
	if disk.available != 8<<30 {
		t.Fatal("磁盘余量不应变化")
	}
	live.set(func(l *LiveOptions) { l.DiskMinBytes = 0 })
	h.tick()
	h.tick()
	h.expect(next, store.StatusRunning)
}

// 快照上限在占位时读出，传给做快照的那一步
func TestLiveSnapshotLimits(t *testing.T) {
	h := newHarness(t, 1)
	v := baseLive()
	v.Snapshot = snapshot.Options{MaxFile: 3 << 20, MaxTotal: 7 << 20}
	attachLive(h, v)
	got := make(chan snapshot.Options, 1)
	h.m.snap = func(_ context.Context, _ *store.Job, limits snapshot.Options) (*snapshot.Meta, error) {
		got <- limits
		return &snapshot.Meta{}, nil
	}
	h.add(1)
	h.tick()
	select {
	case limits := <-got:
		if limits.MaxFile != 3<<20 || limits.MaxTotal != 7<<20 {
			t.Fatalf("快照上限没传到: %+v", limits)
		}
	case <-time.After(time.Second):
		t.Fatal("没有做快照")
	}
}

// 关闭日志静默提醒时，下限取到不了的值，不会标记静默
func TestSilenceFloorWhenDisabled(t *testing.T) {
	h := newHarness(t, 1)
	h.m.opt.SilenceAlert = 0
	if h.m.silenceFloor() < 100*365*24*time.Hour {
		t.Fatalf("关闭时下限应足够大: %s", h.m.silenceFloor())
	}
	var s runStat
	start := time.Now().Add(-time.Hour)
	started := start.Unix()
	s.observeLog(logSample{at: time.Now(), modified: start}, &started, time.Now(), h.m.silenceFloor())
	if !s.silentSince.IsZero() {
		t.Fatal("关闭时不应标记静默")
	}
	h.m.opt.SilenceAlert = time.Minute
	s.observeLog(logSample{at: time.Now(), modified: start}, &started, time.Now(), h.m.silenceFloor())
	if s.silentSince.IsZero() {
		t.Fatal("打开后一小时没输出应标记静默")
	}
}
