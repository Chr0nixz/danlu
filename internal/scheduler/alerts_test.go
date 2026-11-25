package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
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
	// 面板拿到的是机器码：按指定卡等待，挡路的是外部进程
	if w := h.m.Waits()[next]; w.Kind != WaitPinned || len(w.Pinned) != 1 || w.Pinned[0] != 0 || w.Blocker == nil || w.Blocker.Kind != BlockExternal || w.Blocker.PID != 1234 || w.Blocker.GPU != 0 {
		t.Fatalf("结构化等待原因不对: %+v %+v", w, w.Blocker)
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

func TestUnknownMemoryBlockingReasonAndAlert(t *testing.T) {
	h := newHarness(t, 1)
	id := h.add(1)
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].UnknownMemMB = 2048 })
	h.tick()
	if h.job(id).Status != store.StatusQueued {
		t.Fatal("未知显存占用时 GPU 任务应保持排队")
	}
	if reason := h.m.WaitReasons()[id]; !strings.Contains(reason, "未知显存占用") {
		t.Fatalf("队列等待原因应说明未知显存占用: %q", reason)
	}
	if w := h.m.Waits()[id]; w.Kind != WaitGPUs || w.Need != 1 || w.Free != 0 || w.Blocker == nil || w.Blocker.Kind != BlockUnknownMem || w.Blocker.MemMB != 2048 {
		t.Fatalf("结构化等待原因不对: %+v %+v", w, w.Blocker)
	}
	if len(alertEvents(t, h.st.Store)) != 0 {
		t.Fatal("只有一份采样挡着时不应告警")
	}
	h.tick() // 下一份采样还挡着
	var found bool
	for _, event := range alertEvents(t, h.st.Store) {
		if event.Kind != store.EventAlertRaised || event.Key != fmt.Sprintf("blocked:%d", id) {
			continue
		}
		var data store.AlertData
		if err := json.Unmarshal(event.Data, &data); err != nil || data.Type != "blocked_unknown" {
			t.Fatalf("未知显存阻塞告警类型错误: %+v (%v)", data, err)
		}
		found = true
	}
	if !found {
		t.Fatal("未知显存阻塞应记录告警事件")
	}
}

// 任务刚结束时，旧采样里还留着它的进程：队首被挡一份采样就过去了，不该推送"队首被阻塞"
func TestBlockedAlertNeedsTwoSamples(t *testing.T) {
	h := newHarness(t, 1)
	head := h.add(1)
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs = []gpu.Proc{{PID: 4321, JobID: 99}} }) // 已经结束的任务 #99 的进程
	h.tick()
	if w := h.m.Waits()[head]; w.Blocker == nil || w.Blocker.Kind != BlockJobProc {
		t.Fatalf("等待原因照样说明挡路的进程: %+v", w)
	}
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs = nil })
	h.tick()
	h.expect(head, store.StatusRunning)
	if events := alertEvents(t, h.st.Store); len(events) != 0 {
		t.Fatalf("只挡了一份采样不应告警: %+v", events)
	}
}

// 认不出归属的进程（同一用户、正在退出或是僵尸）：卡不分配，但不算外部进程，不告警、不记 ext_seen
func TestOwnerUnknownProcessIsNotExternal(t *testing.T) {
	h := newHarness(t, 2)
	running := h.add(1)
	h.tick()
	h.expect(running, store.StatusRunning)
	card := h.job(running).GPUsAssigned[0]
	other := 1 - card
	h.gpus.set(func(f *fakeGPUs) {
		f.gpus[card].Procs = []gpu.Proc{{PID: 1001, JobID: running}, {PID: 1002, OwnerUnknown: true}}
		f.gpus[other].Procs = []gpu.Proc{{PID: 2002, OwnerUnknown: true}}
	})
	head := h.add(1)
	for range 3 {
		h.tick()
	}
	h.expect(head, store.StatusQueued)
	if w := h.m.Waits()[head]; w.Blocker == nil || w.Blocker.Kind != BlockOwnerUnknown || w.Blocker.PID != 2002 {
		t.Fatalf("等待原因应说明归属不明的进程: %+v", w)
	}
	if events := alertEvents(t, h.st.Store); len(events) != 0 {
		t.Fatalf("归属不明的进程不应告警: %+v", events)
	}
	h.ex.finish(running, 1)
	h.tick()
	if j := h.job(running); j.ExtSeen {
		t.Fatalf("归属不明的进程不应记成运行期间出现过外部进程: %+v", j)
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
	// 查询出错的条目没有 ID，只有上次的分区 ID（和真实采样的形状一致）
	d.disks[0] = system.Disk{Path: "/data", Data: true, Error: "暂时不可用", LastID: "1"}
	d.disks[1].Warn = false
	updateAlerts(t, h.m, time.Now())
	if events := alertEvents(t, h.st.Store); len(events) != 3 || events[2].Key != "disk_low:10" {
		t.Fatalf("分区 ID 前缀相同不应相互影响: %+v", events)
	}
	d.disks[0] = system.Disk{ID: "1", Path: "/data", Data: true, AvailableBytes: 100 << 30}
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

// 任务的进程跑到了没分给它的卡上（程序自己选卡、EGL 渲染按自己的编号选卡）：这张卡不能再分出去，
// 等待原因要说清是哪个任务的进程，而不是笼统的"外部进程"。
func TestStrayJobProcessBlocksCard(t *testing.T) {
	h := newHarness(t, 2)
	a := h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 0)
	h.gpus.set(func(f *fakeGPUs) {
		f.gpus[1].Procs = []gpu.Proc{{PID: 4321, JobID: a, Graphics: true, UsedMemMB: 2048}}
	})
	b := h.add(1)
	h.tick()
	h.expect(b, store.StatusQueued)
	want := fmt.Sprintf("GPU 1 有任务 #%d 的进程（PID 4321）", a)
	if reason := h.m.WaitReasons()[b]; !strings.Contains(reason, want) {
		t.Fatalf("等待原因 %q 里没有 %q", reason, want)
	}
	h.gpus.set(func(f *fakeGPUs) { f.gpus[1].Procs = nil })
	h.tick()
	h.expect(b, store.StatusRunning, 1)
}

// 别的任务的渲染进程落到自己的卡上：几十个进程只记一条告警，写明是哪个任务。
func TestStrayJobProcessesMergedIntoOneAlert(t *testing.T) {
	h := newHarness(t, 2)
	a := h.add(1)
	b := h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 0)
	h.expect(b, store.StatusRunning, 1)
	h.gpus.set(func(f *fakeGPUs) {
		f.gpus[0].Procs = []gpu.Proc{{PID: 10, JobID: a}, {PID: 2002, JobID: b, Graphics: true}, {PID: 2001, JobID: b, Graphics: true}, {PID: 2003, JobID: b}}
	})
	h.tick()
	updateAlerts(t, h.m, time.Now())
	events := alertEvents(t, h.st.Store)
	// 另一条是 b 自己的越界告警（stray:b），见 TestStrayAlert
	if len(events) != 2 || events[0].Key != fmt.Sprintf("external:%d:job%d", a, b) || *events[0].JobID != a ||
		events[1].Key != fmt.Sprintf("stray:%d", b) {
		t.Fatalf("应只有一条按任务合并的告警: %+v", events)
	}
	var data store.AlertData
	if err := json.Unmarshal(events[0].Data, &data); err != nil || data.OtherJob != b || data.PID != 2001 || !slices.Equal(data.GPUs, []int{0}) {
		t.Fatalf("告警数据错误: %+v %v", data, err)
	}
	// 进程增减不再重复告警
	h.gpus.set(func(f *fakeGPUs) {
		f.gpus[0].Procs = append(f.gpus[0].Procs, gpu.Proc{PID: 2004, JobID: b, Graphics: true})
	})
	h.tick()
	updateAlerts(t, h.m, time.Now())
	if n := len(alertEvents(t, h.st.Store)); n != 2 {
		t.Fatalf("同一对任务重复告警: %d 条", n)
	}
}

func strPtr(s string) *string { return &s }

// 任务自己的进程跑到没分给它的卡上（这里是预约收下的卡）：一个任务一条告警，带上能分清原因的事实；
// 运行期间进程时有时无不重复告警，任务结束才清
func TestStrayAlert(t *testing.T) {
	h := newHarness(t, 4)
	r := h.reserve(ReservationReq{PinnedGPUs: []int{0}})
	a := h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning, 1)
	stray := func() {
		h.gpus.set(func(f *fakeGPUs) {
			f.gpus[0].Procs = []gpu.Proc{{PID: 301, JobID: a, CUDAVisible: strPtr("0,1"), UsedMemMB: 8000}}
			f.gpus[1].Procs = []gpu.Proc{{PID: 300, JobID: a, CUDAVisible: strPtr("1")}}
			f.gpus[2].Procs = []gpu.Proc{{PID: 302, JobID: a, Graphics: true, CUDAVisible: strPtr("1")}}
		})
	}
	stray()
	updateAlerts(t, h.m, time.Now())
	events := alertEvents(t, h.st.Store)
	if len(events) != 1 || events[0].Key != fmt.Sprintf("stray:%d", a) || *events[0].JobID != a {
		t.Fatalf("应有一条越界告警: %+v", events)
	}
	var data store.AlertData
	if err := json.Unmarshal(events[0].Data, &data); err != nil {
		t.Fatal(err)
	}
	if data.Type != "stray" || !slices.Equal(data.GPUs, []int{0, 2}) || !slices.Equal(data.Assigned, []int{1}) ||
		!slices.Equal(data.Reserved, []int{0}) || data.PID != 301 || data.Graphics ||
		data.CUDAVisible == nil || *data.CUDAVisible != "0,1" {
		t.Fatalf("告警数据错误: %+v（预约 #%d）", data, r.ID)
	}
	if h.m.Reservations()[0].Status != store.ReservationActive {
		t.Fatal("任务越界不改变预约")
	}

	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs, f.gpus[2].Procs = nil, nil })
	updateAlerts(t, h.m, time.Now())
	stray()
	h.gpus.set(func(f *fakeGPUs) { f.err = errors.New("驱动出错") })
	updateAlerts(t, h.m, time.Now())
	h.gpus.set(func(f *fakeGPUs) { f.err = nil })
	updateAlerts(t, h.m, time.Now())
	if n := len(alertEvents(t, h.st.Store)); n != 1 {
		t.Fatalf("运行期间越界进程时有时无不应重复告警: %d 条", n)
	}

	h.ex.finish(a, 0)
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs, f.gpus[1].Procs, f.gpus[2].Procs = nil, nil, nil })
	h.tick()
	updateAlerts(t, h.m, time.Now())
	events = alertEvents(t, h.st.Store)
	if len(events) != 2 || events[1].Kind != store.EventAlertCleared || events[1].Key != fmt.Sprintf("stray:%d", a) {
		t.Fatalf("任务结束后越界告警应清除: %+v", events)
	}
}

func TestStrayAlertCause(t *testing.T) {
	gpus := []gpu.GPU{{Index: 0}, {Index: 1}, {Index: 2}}
	j := &store.Job{ID: 7, GPUsAssigned: []int{1, 2}}
	if strayAlert(j, gpus, nil) != nil {
		t.Fatal("没有越界进程时不告警")
	}
	gpus[0].Procs = []gpu.Proc{{PID: 9, JobID: 7, Graphics: true, CUDAVisible: strPtr("1,2")}}
	if a := strayAlert(j, gpus, nil); a == nil || !a.Graphics || a.CUDAVisible != nil {
		t.Fatalf("只有图形进程、选卡变量没改：应记成图形渲染: %+v", a)
	}
	gpus[0].Procs = []gpu.Proc{{PID: 9, JobID: 7, CUDAVisible: strPtr("1,2")}}
	if a := strayAlert(j, gpus, nil); a == nil || a.Graphics || a.CUDAVisible != nil {
		t.Fatalf("计算进程的选卡变量没改（运行中改的看不到）：不写原因: %+v", a)
	}
	gpus[0].Procs = []gpu.Proc{{PID: 9, JobID: 7, CUDAVisible: strPtr("")}}
	if a := strayAlert(j, gpus, nil); a == nil || a.CUDAVisible == nil || *a.CUDAVisible != "" {
		t.Fatalf("选卡变量被去掉应记空串: %+v", a)
	}
}
