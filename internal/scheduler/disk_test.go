package scheduler

import (
	"errors"
	"testing"
	"time"

	"danlu/internal/store"
	"danlu/internal/system"
)

type fakeDisk struct {
	available uint64
	err       error
}

func (f *fakeDisk) Latest() ([]system.Disk, time.Time, error) {
	return []system.Disk{{Data: true, AvailableBytes: f.available}}, time.Now(), f.err
}
func attachDisk(h *harness) *fakeDisk {
	f := &fakeDisk{available: 8 << 30}
	h.m.opt.Disks, h.m.opt.DiskMinBytes = f, 2<<30
	return f
}

func TestDiskPauseRecoveryAndHysteresis(t *testing.T) {
	h := newHarness(t, 1)
	disk := attachDisk(h)
	a, next := h.add(1), h.add(1)
	h.tick()
	disk.available = 1 << 30
	h.tick()
	h.expect(a, store.StatusRunning)
	h.expect(next, store.StatusQueued)
	state, err := h.m.QueueState()
	if err != nil || !state.Paused || state.Kind != "disk" {
		t.Fatalf("低磁盘未暂停: %+v %v", state, err)
	}
	h.ex.finish(a, 0)
	h.tick()
	h.expect(a, store.StatusSucceeded)
	disk.available = 3 << 30
	h.tick()
	h.expect(next, store.StatusQueued)
	if _, _, err := h.m.ResumeQueue(); !IsValidation(err) {
		t.Fatalf("达到恢复线之前不应继续: %v", err)
	}
	disk.available = 4 << 30
	h.tick()
	h.expect(next, store.StatusRunning)
	state, err = h.m.QueueState()
	if err != nil || state.Paused {
		t.Fatalf("磁盘恢复后仍暂停: %+v %v", state, err)
	}
	all, err := h.st.EventsAfter(0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var pauses, resumes int
	for _, e := range all {
		if e.Kind == store.EventQueuePaused {
			pauses++
		}
		if e.Kind == store.EventQueueResumed {
			resumes++
		}
	}
	if pauses != 1 || resumes != 1 {
		t.Fatalf("磁盘事件重复或缺失: %d %d", pauses, resumes)
	}
}

func TestDiskPreservesManualAndFailFastPause(t *testing.T) {
	for _, kind := range []string{"manual", "fail_fast"} {
		t.Run(kind, func(t *testing.T) {
			h := newHarness(t, 1)
			disk := attachDisk(h)
			next := h.add(1)
			if _, _, err := h.m.PauseQueue(kind, "等待人工处理", nil); err != nil {
				t.Fatal(err)
			}
			disk.available = 1 << 30
			h.tick()
			if _, _, err := h.m.ResumeQueue(); !IsValidation(err) {
				t.Fatalf("低磁盘不应允许人工绕过: %v", err)
			}
			disk.available = 8 << 30
			h.tick()
			h.expect(next, store.StatusQueued)
			if state, err := h.m.QueueState(); err != nil || state.Kind != kind || state.Reason != "等待人工处理" {
				t.Fatalf("自动恢复覆盖人工暂停: %+v %v", state, err)
			}
		})
	}
}

func TestDiskWriteFailureRemainsBlocked(t *testing.T) {
	h := newHarness(t, 1)
	disk := attachDisk(h)
	disk.available = 1 << 30
	h.st.setQueueFailure(true)
	next := h.add(1)
	h.tick()
	if state, err := h.m.QueueState(); err != nil || !state.Paused || state.Kind != "disk" {
		t.Fatalf("磁盘暂停写库失败后未保留保护: %+v %v", state, err)
	}
	disk.available = 8 << 30
	h.tick()
	h.expect(next, store.StatusQueued)
	h.st.setQueueFailure(false)
	h.tick()
	h.expect(next, store.StatusRunning)
}

func TestDiskUnknownBlocksAndDisabledAllows(t *testing.T) {
	h := newHarness(t, 1)
	disk := attachDisk(h)
	disk.err = errors.New("磁盘数据过期")
	next := h.add(1)
	h.tick()
	h.expect(next, store.StatusQueued)
	h.m.opt.DiskMinBytes = 0
	h.tick()
	h.expect(next, store.StatusRunning)
}

func TestFastFailureDuringDiskPauseRequiresManualResume(t *testing.T) {
	h := newHarness(t, 1)
	disk := attachDisk(h)
	h.m.opt.FailFast = 2 * time.Minute
	a, next := h.add(1), h.add(1)
	h.tick()
	disk.available = 1 << 30
	h.tick()
	h.ex.finish(a, 1)
	h.tick()
	disk.available = 8 << 30
	h.tick()
	h.expect(next, store.StatusQueued)
	if state, err := h.m.QueueState(); err != nil || !state.Paused || state.Kind != "fail_fast" {
		t.Fatalf("磁盘恢复不应解除快速失败暂停: %+v %v", state, err)
	}
}
