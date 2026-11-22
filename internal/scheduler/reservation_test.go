package scheduler

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/store"
)

func hours(n int64) *int64 {
	v := n * 3600
	return &v
}

func (h *harness) reserve(req ReservationReq) *store.Reservation {
	h.t.Helper()
	r, err := h.m.CreateReservation(req)
	if err != nil {
		h.t.Fatal(err)
	}
	return r
}

// reservation 读调度器内存里的预约（面板看到的也是它）；已结束的返回 nil
func (h *harness) reservation(id int64) *store.Reservation {
	for _, r := range h.m.Reservations() {
		if r.ID == id {
			return &r
		}
	}
	return nil
}

func (h *harness) expectHeld(id int64, status string, gpus ...int) {
	h.t.Helper()
	r := h.reservation(id)
	if r == nil {
		h.t.Fatalf("预约 #%d 已经结束", id)
	}
	if r.Status != status || !slices.Equal(r.GPUsHeld, gpus) {
		h.t.Fatalf("预约 #%d 是 %s %v，期望 %s %v", id, r.Status, r.GPUsHeld, status, gpus)
	}
}

func (h *harness) kindEvents(kind string) []store.Event {
	h.t.Helper()
	all, err := h.st.EventsAfter(0, 1000)
	if err != nil {
		h.t.Fatal(err)
	}
	var out []store.Event
	for _, e := range all {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func reservationStep(t *testing.T, m *Manager, now time.Time) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	gpus, _, err := m.gpus.Latest()
	m.reservationsLocked(now, gpus, err)
}

func TestReservationClaimsIdleCardsAtOnce(t *testing.T) {
	h := newHarness(t, 4)
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs = []gpu.Proc{{PID: 4242, User: "alice"}} })
	r := h.reserve(ReservationReq{GPUs: 2, Note: " 调试\n显存 "})
	if r.Note != "调试 显存" {
		t.Fatalf("用途应压成一行: %q", r.Note)
	}
	// 按张数只收真正空闲的卡，有外部进程的 GPU 0 跳过
	h.expectHeld(r.ID, store.ReservationActive, 1, 2)
	if got := h.reservation(r.ID); got.EndsAt != nil || got.ReadyAt == nil {
		t.Fatalf("不定时的预约不应有到期时间: %+v", got)
	}
	if n := len(h.kindEvents(store.EventReservationReady)); n != 1 {
		t.Fatalf("就绪事件 %d 条", n)
	}
	a, b := h.add(1), h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 3)
	h.expect(b, store.StatusQueued)
	if db, _ := h.st.GetReservation(r.ID); !slices.Equal(db.GPUsHeld, []int{1, 2}) || db.Status != store.ReservationActive {
		t.Fatalf("库里的预约: %+v", db)
	}
}

func TestReservationBeatsQueuedJobs(t *testing.T) {
	h := newHarness(t, 2)
	a := h.add(2)
	h.tick()
	h.expect(a, store.StatusRunning, 0, 1)
	b := h.add(1)
	r := h.reserve(ReservationReq{GPUs: 1, Duration: hours(1)})
	h.expectHeld(r.ID, store.ReservationWaiting)
	h.ex.finish(a, 0)
	h.tick()
	// 同一轮里空出的两张卡：预约先收 GPU 0，排队任务拿剩下的
	h.expectHeld(r.ID, store.ReservationActive, 0)
	h.expect(b, store.StatusRunning, 1)
	got := h.reservation(r.ID)
	if got.EndsAt == nil || *got.EndsAt-*got.ReadyAt != 3600 {
		t.Fatalf("计时应从卡到位时开始: %+v", got)
	}
}

func TestReservationAccumulatesCards(t *testing.T) {
	h := newHarness(t, 3)
	a, b := h.add(1), h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 0)
	h.expect(b, store.StatusRunning, 1)
	r := h.reserve(ReservationReq{GPUs: 2})
	h.expectHeld(r.ID, store.ReservationWaiting, 2)
	c := h.add(1)
	h.tick()
	h.expect(c, store.StatusQueued) // 已收的卡凑齐前也不给任务
	h.ex.finish(a, 0)
	h.tick()
	h.expectHeld(r.ID, store.ReservationActive, 0, 2)
	h.expect(c, store.StatusQueued)
	if n := len(h.kindEvents(store.EventReservationReady)); n != 1 {
		t.Fatalf("凑齐时只发一次就绪事件，实际 %d 条", n)
	}
	h.ex.finish(b, 0)
	h.tick()
	h.expect(c, store.StatusRunning, 1)
}

func TestReservationPinnedAndCountRules(t *testing.T) {
	h := newHarness(t, 3)
	h.gpus.set(func(f *fakeGPUs) {
		f.gpus[0].Procs = []gpu.Proc{{PID: 4242, User: "me"}}
		f.gpus[1].UnknownMemMB = 4096
	})
	// 指定卡号时，有外部进程的卡照样收下：多半就是用户自己的任务
	pinned := h.reserve(ReservationReq{PinnedGPUs: []int{0, 0}})
	h.expectHeld(pinned.ID, store.ReservationActive, 0)
	if got := h.reservation(pinned.ID); got.GPUsReq != 1 || !slices.Equal(got.GPUsPinned, []int{0}) {
		t.Fatalf("指定卡号应去重: %+v", got)
	}
	count := h.reserve(ReservationReq{GPUs: 2})
	h.expectHeld(count.ID, store.ReservationWaiting, 2)
	h.gpus.set(func(f *fakeGPUs) { f.gpus[1].UnknownMemMB = 0 })
	h.tick()
	h.expectHeld(count.ID, store.ReservationActive, 1, 2)
}

func TestReservationAvoidsCardsPinnedByQueue(t *testing.T) {
	h := newHarness(t, 2)
	if _, _, err := h.m.PauseQueue("manual", "", nil); err != nil {
		t.Fatal(err)
	}
	h.add(0, 0)
	r := h.reserve(ReservationReq{GPUs: 1})
	h.expectHeld(r.ID, store.ReservationActive, 1)
}

func TestReservationExpiryAndExtend(t *testing.T) {
	h := newHarness(t, 1)
	r := h.reserve(ReservationReq{GPUs: 1, Duration: hours(1)})
	h.expectHeld(r.ID, store.ReservationActive, 0)
	j := h.add(0, 0)
	h.tick()
	h.expect(j, store.StatusQueued)
	ends := time.Unix(*h.reservation(r.ID).EndsAt, 0)

	reservationStep(t, h.m, ends.Add(-11*time.Minute))
	if n := len(h.kindEvents(store.EventReservationExpiring)); n != 0 {
		t.Fatalf("还没到提醒时间: %d", n)
	}
	reservationStep(t, h.m, ends.Add(-9*time.Minute))
	reservationStep(t, h.m, ends.Add(-8*time.Minute))
	if n := len(h.kindEvents(store.EventReservationExpiring)); n != 1 {
		t.Fatalf("到期提醒应只有一条: %d", n)
	}
	// 重启后再走到这里也不会多一条
	h.m = h.manager()
	reservationStep(t, h.m, ends.Add(-7*time.Minute))
	if n := len(h.kindEvents(store.EventReservationExpiring)); n != 1 {
		t.Fatalf("重启后重复提醒: %d", n)
	}

	got, err := h.m.ExtendReservation(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *got.EndsAt != ends.Unix()+3600 || *got.Duration != 7200 {
		t.Fatalf("延长后: %+v", got)
	}
	ends = time.Unix(*got.EndsAt, 0)
	reservationStep(t, h.m, ends.Add(-9*time.Minute))
	if n := len(h.kindEvents(store.EventReservationExpiring)); n != 2 {
		t.Fatalf("延长后应再提醒一次: %d", n)
	}

	reservationStep(t, h.m, ends.Add(time.Second))
	if h.reservation(r.ID) != nil {
		t.Fatal("到期后应结束")
	}
	ended := h.kindEvents(store.EventReservationEnded)
	if len(ended) != 1 || !strings.Contains(string(ended[0].Data), "到期自动释放") {
		t.Fatalf("结束事件: %+v", ended)
	}
	if db, _ := h.st.GetReservation(r.ID); db.Status != store.ReservationEnded || db.EndedAt == nil {
		t.Fatalf("库里的预约: %+v", db)
	}
	h.tick()
	h.expect(j, store.StatusRunning, 0)
}

func TestReservationShortDurationNoWarning(t *testing.T) {
	h := newHarness(t, 1)
	ten := int64(minReservationSeconds)
	r := h.reserve(ReservationReq{GPUs: 1, Duration: &ten})
	reservationStep(t, h.m, time.Unix(*h.reservation(r.ID).EndsAt-1, 0))
	if n := len(h.kindEvents(store.EventReservationExpiring)); n != 0 {
		t.Fatalf("不超过 10 分钟的预约不提醒: %d", n)
	}
}

func TestReservationUntrustedGPUData(t *testing.T) {
	h := newHarness(t, 2)
	active := h.reserve(ReservationReq{GPUs: 1, Duration: hours(1)})
	h.gpus.set(func(f *fakeGPUs) { f.err = gpu.ErrProcsUnknown })
	waiting := h.reserve(ReservationReq{GPUs: 1})
	h.tick()
	h.expectHeld(waiting.ID, store.ReservationWaiting)
	// GPU 数据不可信时不收卡，到期照样释放
	reservationStep(t, h.m, time.Unix(*h.reservation(active.ID).EndsAt, 0))
	if h.reservation(active.ID) != nil {
		t.Fatal("GPU 数据不可信时也应按时释放")
	}
	h.gpus.set(func(f *fakeGPUs) { f.err = nil })
	h.tick()
	h.expectHeld(waiting.ID, store.ReservationActive, 0)
}

func TestReservationSaveFailureMemoryWins(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	h.tick()
	r := h.reserve(ReservationReq{GPUs: 1})
	b := h.add(1)
	h.st.mu.Lock()
	h.st.failResSave = true
	h.st.mu.Unlock()
	h.ex.finish(a, 0)
	h.tick()
	h.expectHeld(r.ID, store.ReservationActive, 0)
	h.expect(b, store.StatusQueued)
	if db, _ := h.st.GetReservation(r.ID); len(db.GPUsHeld) != 0 {
		t.Fatalf("写库失败时库里不应有已收的卡: %+v", db)
	}
	// 用户的操作要先写库：失败时什么都不改
	if _, err := h.m.ReleaseReservation(r.ID); err == nil {
		t.Fatal("写库失败时释放应报错")
	}
	h.expectHeld(r.ID, store.ReservationActive, 0)
	h.tick()
	h.expect(b, store.StatusQueued)
	h.st.mu.Lock()
	h.st.failResSave = false
	h.st.mu.Unlock()
	h.tick()
	if db, _ := h.st.GetReservation(r.ID); !slices.Equal(db.GPUsHeld, []int{0}) || db.Status != store.ReservationActive {
		t.Fatalf("重试后库里的预约: %+v", db)
	}
	if n := len(h.kindEvents(store.EventReservationReady)); n != 1 {
		t.Fatalf("重试后就绪事件 %d 条", n)
	}
}

func TestReservationSurvivesRestart(t *testing.T) {
	h := newHarness(t, 2)
	r := h.reserve(ReservationReq{GPUs: 1})
	h.m = h.manager()
	h.m.Recover()
	h.expectHeld(r.ID, store.ReservationActive, 0)
	a, b := h.add(1), h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 1)
	h.expect(b, store.StatusQueued)
}

func TestReservationReadFailureBlocksGPUJobs(t *testing.T) {
	h := newHarness(t, 1)
	h.st.mu.Lock()
	h.st.failResRead = true
	h.st.mu.Unlock()
	h.m = h.manager()
	h.m.Recover()
	g, cpu := h.add(1), h.add(0)
	h.tick()
	h.expect(g, store.StatusQueued)
	h.expect(cpu, store.StatusQueued) // 严格先来后到，排在后面的不用卡的任务也等着
	updateAlerts(t, h.m, time.Now())
	if reason := h.m.WaitReasons()[g]; reason != "等待读取 GPU 预约" {
		t.Fatalf("等待原因: %q", reason)
	}
	h.st.mu.Lock()
	h.st.failResRead = false
	h.st.mu.Unlock()
	h.tick()
	h.expect(g, store.StatusRunning, 0)
	h.tick()
	h.expect(cpu, store.StatusRunning)
}

func TestReservationReleaseAndValidation(t *testing.T) {
	h := newHarness(t, 2)
	untimed := h.reserve(ReservationReq{PinnedGPUs: []int{1}})
	if _, err := h.m.ExtendReservation(untimed.ID); !IsValidation(err) {
		t.Fatalf("不定时的预约不能延长: %v", err)
	}
	j := h.add(0, 1)
	h.tick()
	h.expect(j, store.StatusQueued)
	got, err := h.m.ReleaseReservation(untimed.ID)
	if err != nil || got.Status != store.ReservationEnded || got.EndReason != "手动释放" {
		t.Fatalf("释放: %+v %v", got, err)
	}
	if _, err := h.m.ReleaseReservation(untimed.ID); !errors.Is(err, ErrReservationEnded) {
		t.Fatalf("重复释放: %v", err)
	}
	if _, err := h.m.ReleaseReservation(999); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("不存在的预约: %v", err)
	}
	h.tick()
	h.expect(j, store.StatusRunning, 1)
	if n := len(h.kindEvents(store.EventReservationEnded)); n != 1 {
		t.Fatalf("结束事件 %d 条", n)
	}

	minute := int64(60)
	week := int64(8 * 24 * 3600)
	for _, req := range []ReservationReq{
		{GPUs: 0}, {GPUs: 3}, {PinnedGPUs: []int{5}}, {PinnedGPUs: []int{-1}},
		{GPUs: 1, Duration: &minute}, {GPUs: 1, Duration: &week},
		{GPUs: 1, Note: strings.Repeat("长", 101)},
	} {
		if _, err := h.m.CreateReservation(req); !IsValidation(err) {
			t.Fatalf("%+v 应报参数错误: %v", req, err)
		}
	}
	waiting := h.reserve(ReservationReq{GPUs: 2, Duration: hours(1)})
	if _, err := h.m.ExtendReservation(waiting.ID); !IsValidation(err) {
		t.Fatalf("还在收卡的预约不能延长: %v", err)
	}
	for i := 1; i < maxOpenReservations; i++ {
		h.reserve(ReservationReq{GPUs: 1})
	}
	if _, err := h.m.CreateReservation(ReservationReq{GPUs: 1}); !IsValidation(err) {
		t.Fatalf("预约数量上限: %v", err)
	}
}

func TestReservationWaitReasonWithoutBlockedAlert(t *testing.T) {
	h := newHarness(t, 2)
	h.reserve(ReservationReq{PinnedGPUs: []int{0}})
	// 用户自己的任务跑在预约的卡上
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs = []gpu.Proc{{PID: 4242, User: "me"}} })
	j := h.add(0, 0)
	h.tick()
	h.expect(j, store.StatusQueued)
	updateAlerts(t, h.m, time.Now())
	reason := h.m.WaitReasons()[j]
	if !strings.Contains(reason, "GPU 0 已被预约 #1 占用") || strings.Contains(reason, "外部进程") {
		t.Fatalf("等待原因: %q", reason)
	}
	if events := alertEvents(t, h.st.Store); len(events) != 0 {
		t.Fatalf("预约的卡上的进程不应触发队首阻塞告警: %+v", events)
	}
}
