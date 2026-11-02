package scheduler

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/store"
	"danlu/internal/system"
)

func updateAlerts(t *testing.T, m *Manager, now time.Time) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.updateAlertsLocked(now); err != nil {
		t.Fatal(err)
	}
}

func alertEvents(t *testing.T, st *store.Store) []store.Event {
	t.Helper()
	events, err := st.EventsAfter(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var out []store.Event
	for _, e := range events {
		if e.Kind == store.EventAlertRaised || e.Kind == store.EventAlertCleared {
			out = append(out, e)
		}
	}
	return out
}

func TestGPUAlertDebounceRecoveryAndRestart(t *testing.T) {
	h := newHarness(t, 1)
	ts := time.Now()
	h.gpus.set(func(f *fakeGPUs) { f.err = errors.New("模拟驱动错误 /private/path") })
	updateAlerts(t, h.m, ts)
	updateAlerts(t, h.m, ts.Add(59*time.Second))
	if len(alertEvents(t, h.st.Store)) != 0 {
		t.Fatal("GPU 短暂报错不应告警")
	}
	updateAlerts(t, h.m, ts.Add(time.Minute))
	updateAlerts(t, h.m, ts.Add(2*time.Minute))
	if events := alertEvents(t, h.st.Store); len(events) != 1 || strings.Contains(string(events[0].Data), "/private") {
		t.Fatalf("应只记一次结构化告警: %+v", events)
	}
	h.m = h.manager()
	updateAlerts(t, h.m, ts.Add(3*time.Minute))
	if len(alertEvents(t, h.st.Store)) != 1 {
		t.Fatal("重启后不应重复 GPU 告警")
	}
	h.gpus.set(func(f *fakeGPUs) { f.err = nil })
	updateAlerts(t, h.m, ts.Add(4*time.Minute))
	updateAlerts(t, h.m, ts.Add(5*time.Minute))
	if events := alertEvents(t, h.st.Store); len(events) != 2 || events[1].Kind != store.EventAlertCleared {
		t.Fatalf("恢复事件错误: %+v", events)
	}
	h.gpus.set(func(f *fakeGPUs) { f.err = errors.New("再次错误") })
	updateAlerts(t, h.m, ts.Add(6*time.Minute))
	updateAlerts(t, h.m, ts.Add(7*time.Minute))
	if len(alertEvents(t, h.st.Store)) != 3 {
		t.Fatal("恢复后再次故障应再报一次")
	}
}

func TestExternalProcessMergedAndBlockingReason(t *testing.T) {
	h := newHarness(t, 2)
	running := h.add(2)
	h.tick()
	h.gpus.set(func(f *fakeGPUs) {
		for i := range f.gpus {
			f.gpus[i].Procs = []gpu.Proc{{PID: 1234, JobID: 0}}
		}
	})
	h.tick()
	updateAlerts(t, h.m, time.Now())
	events := alertEvents(t, h.st.Store)
	if len(events) != 1 || events[0].JobID == nil || *events[0].JobID != running {
		t.Fatalf("相同任务与进程不应按卡重复告警: %+v", events)
	}
	var data store.AlertData
	if err := json.Unmarshal(events[0].Data, &data); err != nil || len(data.GPUs) != 2 {
		t.Fatalf("多卡事实未合并: %+v %v", data, err)
	}
	h.ex.finish(running, 0)
	next := h.add(1, 0)
	h.tick()
	if reason := h.m.WaitReasons()[next]; !strings.Contains(reason, "PID 1234") || !strings.Contains(reason, "指定 GPU") {
		t.Fatalf("等待原因不明确: %q", reason)
	}
	count := len(alertEvents(t, h.st.Store))
	h.m = h.manager()
	h.tick()
	if len(alertEvents(t, h.st.Store)) != count {
		t.Fatal("重启后重复队首阻塞告警")
	}
	h.gpus.set(func(f *fakeGPUs) {
		for i := range f.gpus {
			f.gpus[i].Procs = nil
		}
	})
	h.tick()
	h.expect(next, store.StatusRunning)
	if reason := h.m.WaitReasons()[next]; reason != "" {
		t.Fatalf("启动后残留等待原因: %q", reason)
	}
}

type alertDisk struct {
	disks []system.Disk
	at    time.Time
	err   error
}

func (d *alertDisk) Latest() ([]system.Disk, time.Time, error) { return d.disks, d.at, d.err }

func TestDiskAlertsPreserveUnknownSamplesAndRestore(t *testing.T) {
	h := newHarness(t, 1)
	d := &alertDisk{at: time.Now(), disks: []system.Disk{{ID: "1", Warn: true, Data: true, AvailableBytes: 1 << 30}, {ID: "10", Warn: true}}}
	h.m.opt.Disks = d
	updateAlerts(t, h.m, time.Now())
	if len(alertEvents(t, h.st.Store)) != 2 {
		t.Fatal("每个分区应各记一条")
	}
	h.m = h.manager()
	h.m.opt.Disks = d
	d.at = time.Time{}
	updateAlerts(t, h.m, time.Now())
	if len(alertEvents(t, h.st.Store)) != 2 {
		t.Fatal("重启后未采样不能宣布恢复")
	}
	d.at = time.Now()
	d.disks[0].Error = "暂时不可用"
	d.disks[1].Warn = false
	updateAlerts(t, h.m, time.Now())
	if events := alertEvents(t, h.st.Store); len(events) != 3 || events[2].Key != "disk_low:10" {
		t.Fatalf("分区 ID 前缀相同不应相互影响: %+v", events)
	}
	d.disks[0].Error, d.disks[0].Warn = "", false
	updateAlerts(t, h.m, time.Now())
	if len(alertEvents(t, h.st.Store)) != 4 {
		t.Fatal("恢复后应清除磁盘告警")
	}
}

type alertFailStore struct {
	JobStore
	fail bool
}

func (s *alertFailStore) AppendEvents(events ...*store.Event) error {
	if s.fail {
		return errors.New("模拟告警写入失败")
	}
	return s.JobStore.AppendEvents(events...)
}

func TestAlertWriteFailureRetriesWithoutRepeating(t *testing.T) {
	h := newHarness(t, 1)
	d := &alertDisk{at: time.Now(), disks: []system.Disk{{ID: "disk", Warn: true}}}
	h.m.opt.Disks = d
	f := &alertFailStore{JobStore: h.st, fail: true}
	h.m.st = f
	h.m.mu.Lock()
	err := h.m.updateAlertsLocked(time.Now())
	h.m.mu.Unlock()
	if err == nil || len(alertEvents(t, h.st.Store)) != 0 {
		t.Fatal("写入失败不应留下半次变化")
	}
	f.fail = false
	updateAlerts(t, h.m, time.Now())
	updateAlerts(t, h.m, time.Now())
	if len(alertEvents(t, h.st.Store)) != 1 {
		t.Fatal("重试应只记一次事件")
	}
}
