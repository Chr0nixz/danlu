package store

import (
	"errors"
	"slices"
	"testing"
)

func TestReservationRoundTrip(t *testing.T) {
	s := openTest(t)
	d := int64(3600)
	r := &Reservation{Note: "调试", GPUsReq: 2, GPUsPinned: []int{}, Duration: &d, Status: ReservationWaiting, CreatedAt: 100}
	if err := s.InsertReservation(r); err != nil || r.ID == 0 {
		t.Fatalf("插入: id=%d err=%v", r.ID, err)
	}
	pinned := &Reservation{GPUsReq: 1, GPUsPinned: []int{3}, Status: ReservationWaiting, CreatedAt: 101}
	if err := s.InsertReservation(pinned); err != nil {
		t.Fatal(err)
	}

	got, err := s.GetReservation(r.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Note != "调试" || got.GPUsReq != 2 || got.Duration == nil || *got.Duration != 3600 || got.ReadyAt != nil ||
		got.EndsAt != nil || len(got.GPUsHeld) != 0 || got.Status != ReservationWaiting {
		t.Fatalf("读回: %+v", got)
	}
	if p, _ := s.GetReservation(pinned.ID); p.Duration != nil || !slices.Equal(p.GPUsPinned, []int{3}) || p.Want() != 1 {
		t.Fatalf("指定卡号的预约: %+v", p)
	}

	ready, ends := int64(200), int64(3800)
	r.GPUsHeld, r.Status, r.ReadyAt, r.EndsAt = []int{0, 2}, ReservationActive, &ready, &ends
	e := &Event{Kind: EventReservationReady, Key: "reservation:1", DedupeKey: "reservation_ready:1"}
	if err := s.SaveReservation(r, e); err != nil {
		t.Fatal(err)
	}
	// 重试同一个去重键不会多写一条
	if err := s.SaveReservation(r, &Event{Kind: EventReservationReady, Key: "reservation:1", DedupeKey: "reservation_ready:1"}); err != nil {
		t.Fatal(err)
	}
	events, _ := s.EventsAfter(0, 10)
	if len(events) != 1 || events[0].JobID != nil {
		t.Fatalf("事件: %+v", events)
	}
	got, _ = s.GetReservation(r.ID)
	if !slices.Equal(got.GPUsHeld, []int{0, 2}) || *got.ReadyAt != 200 || *got.EndsAt != 3800 {
		t.Fatalf("保存后: %+v", got)
	}

	ended := int64(4000)
	r.Status, r.EndedAt, r.EndReason = ReservationEnded, &ended, "到期"
	if err := s.SaveReservation(r); err != nil {
		t.Fatal(err)
	}
	open, err := s.OpenReservations()
	if err != nil || len(open) != 1 || open[0].ID != pinned.ID {
		t.Fatalf("未结束的预约: %+v %v", open, err)
	}
}

func TestSaveReservationAtomic(t *testing.T) {
	s := openTest(t)
	r := &Reservation{GPUsReq: 1, Status: ReservationWaiting, CreatedAt: 1}
	if err := s.InsertReservation(r); err != nil {
		t.Fatal(err)
	}
	r.GPUsHeld = []int{1}
	jobID := int64(5)
	// 带任务 id 的事件不合法：整次保存回滚，卡号也不写进去
	if err := s.SaveReservation(r, &Event{Kind: EventReservationReady, JobID: &jobID}); err == nil {
		t.Fatal("应拒绝带任务 id 的预约事件")
	}
	got, _ := s.GetReservation(r.ID)
	if len(got.GPUsHeld) != 0 {
		t.Fatalf("回滚后不应有已收的卡: %+v", got)
	}
	missing := &Reservation{ID: 99, Status: ReservationWaiting}
	if err := s.SaveReservation(missing); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的预约: %v", err)
	}
	if _, err := s.GetReservation(99); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的预约: %v", err)
	}
}

func TestReservationClone(t *testing.T) {
	d := int64(60)
	r := &Reservation{GPUsHeld: []int{1}, Duration: &d}
	c := r.Clone()
	c.GPUsHeld[0], *c.Duration = 9, 1
	if r.GPUsHeld[0] != 1 || *r.Duration != 60 || c.GPUsPinned == nil {
		t.Fatalf("Clone 应深拷贝: %+v %+v", r, c)
	}
}
