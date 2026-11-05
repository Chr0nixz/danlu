package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/runner"
	"danlu/internal/store"
)

func TestProfileSamplesPersistAndResume(t *testing.T) {
	h := newHarness(t, 2)
	id := h.add(2)
	h.tick()
	j := h.job(id)
	target := runTarget{id, j.PID, j.PIDStart}
	at := time.Now().Add(time.Second)
	gpus := []gpu.GPU{
		{Index: 0, Util: 20, MemTotalMB: 1000, Procs: []gpu.Proc{{JobID: id, UsedMemMB: 10}, {JobID: id, UsedMemMB: 20}, {UsedMemMB: 800}}},
		{Index: 1, Util: 60, MemTotalMB: 2000, Procs: []gpu.Proc{{JobID: id, UsedMemMB: 50}}},
	}
	procs := map[runTarget]procSample{target: {at: at, stats: runner.ProcessStats{RSSBytes: 1024, RSSKnown: true}}}
	stat := h.m.updateRunStat(j, target, gpus, at, nil, &procs, at)
	h.m.updateRunStat(j, target, gpus, at, nil, &procs, at)
	gpus[0].Util, gpus[1].Util, gpus[0].Procs[0].UsedMemMB = 70, 90, 0
	h.m.updateRunStat(j, target, gpus, at.Add(2*time.Second), nil, &procs, at.Add(2*time.Second))
	procs[target] = procSample{at: at.Add(30 * time.Second), stats: runner.ProcessStats{RSSBytes: 2048, RSSKnown: true}}
	h.m.updateRunStat(j, target, gpus, at.Add(2*time.Second), nil, &procs, at.Add(profileSaveEvery))
	if p := stat.profile; p.GPU.Samples != 2 || p.GPU.AvgUtil != 60 || p.GPU.Cards[0].PeakMemMB != 30 ||
		p.GPU.CoveredSeconds != 2 || p.Host.Samples != 2 || p.Host.PeakRSSBytes != 2048 || p.Host.CoveredSeconds != 30 {
		t.Fatalf("采样统计有误: GPU %+v, 主机 %+v", p.GPU, p.Host)
	}
	h.m = h.manager()
	h.m.Recover()
	j = h.job(id)
	later := at.Add(10 * time.Minute)
	gpus[0].Util, gpus[1].Util = 50, 70
	procs[target] = procSample{at: later, stats: runner.ProcessStats{RSSBytes: 4096, RSSKnown: true}}
	h.m.updateRunStat(j, target, gpus, later, nil, &procs, later)
	h.ex.finish(id, 0)
	h.tick()
	if p := h.job(id).Profile; p == nil || p.GPU.Samples != 3 || p.GPU.AvgUtil != 60 ||
		p.GPU.CoveredSeconds != 2 || p.Host.Samples != 3 || p.Host.PeakRSSBytes != 4096 || p.Host.CoveredSeconds != 30 {
		t.Fatalf("重启或收尾后的资源画像有误: %+v", p)
	}
}

func TestSilentAlertEdgesAndRestart(t *testing.T) {
	h := newHarness(t, 1)
	h.m.opt.SilenceAlert = 30 * time.Minute
	id := h.add(1)
	h.tick()
	j := h.job(id)
	target := runTarget{id, j.PID, j.PIDStart}
	output := time.Unix(*j.StartedAt, 0).Add(time.Second)
	quiet := output.Add(31 * time.Minute)
	stat := h.m.runStats[id]
	stat.observeLog(logSample{at: quiet, modified: output}, j.StartedAt, quiet, h.m.opt.SilenceAlert)
	samples := map[runTarget]procSample{target: {at: quiet.Add(time.Second), stats: runner.ProcessStats{Count: 3, DState: 2}}}
	h.m.procSamples.Store(&samples)
	updateAlerts(t, h.m, quiet.Add(time.Second))
	updateAlerts(t, h.m, quiet.Add(2*time.Second))
	h.m = h.manager()
	h.m.opt.SilenceAlert = 30 * time.Minute
	h.m.Recover()
	stat = h.m.runStats[id]
	updateAlerts(t, h.m, quiet.Add(3*time.Second)) // 日志采样还没回来
	stat.observeLog(logSample{at: quiet, modified: output}, j.StartedAt, quiet, h.m.opt.SilenceAlert)
	updateAlerts(t, h.m, quiet.Add(4*time.Second))
	resumed := quiet.Add(5 * time.Second)
	stat.observeLog(logSample{at: resumed, modified: resumed}, j.StartedAt, resumed, h.m.opt.SilenceAlert)
	updateAlerts(t, h.m, resumed)
	later := resumed.Add(40 * time.Minute)
	stat.observeLog(logSample{at: later, modified: resumed}, j.StartedAt, later, h.m.opt.SilenceAlert)
	updateAlerts(t, h.m, later)
	var silent []store.Event
	for _, e := range alertEvents(t, h.st.Store) {
		if e.Key == "silent:1" {
			silent = append(silent, e)
		}
	}
	if len(silent) != 2 || silent[0].Kind != store.EventAlertRaised || silent[1].Kind != store.EventAlertCleared {
		t.Fatalf("静默告警重复、未清除或未自适应: %+v", silent)
	}
	var data store.AlertData
	if err := json.Unmarshal(silent[0].Data, &data); err != nil || data.Since != output.Unix() || data.DState == nil || *data.DState != 2 {
		t.Fatalf("静默告警采样错误: %s, %v", silent[0].Data, err)
	}
	var restarted runStat
	restarted.observeLog(logSample{at: later, modified: resumed}, j.StartedAt, later, h.m.opt.SilenceAlert)
	if !restarted.silentSince.Equal(resumed) {
		t.Fatal("重启后未重新使用静默下限")
	}
}

func drainEvents(t *testing.T, h *harness) []store.Event {
	t.Helper()
	all, err := h.st.EventsAfter(0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var events []store.Event
	for _, e := range all {
		if e.Kind == store.EventQueueDrained {
			events = append(events, e)
		}
	}
	return events
}

func TestQueueDrainWaitsForPendingFinish(t *testing.T) {
	h := newHarness(t, 1)
	a, b := h.add(0), h.add(0)
	h.tick()
	h.st.setFail(func(j *store.Job) bool { return j.ID == a && j.Finished() })
	h.ex.finish(a, 0)
	h.ex.finish(b, 0)
	h.tick()
	if len(drainEvents(t, h)) != 0 {
		t.Fatal("尚有结束事件待写时提前记了排空")
	}
	if _, err := h.m.SetNotifyDone(a, true); !errors.Is(err, ErrNotifyFinished) {
		t.Fatalf("待写入的终态任务仍可改提醒: %v", err)
	}
	h.st.setFail(nil)
	h.tick()
	h.tick()
	finish := h.events(a, store.EventJobStarted, store.EventJobFinished)[1]
	if drains := drainEvents(t, h); len(drains) != 1 || drains[0].ID <= finish.ID {
		t.Fatalf("重试后排空事件错误: %+v", drains)
	}
}

func TestNotifyToggleDuringLaunchAndDrain(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(0)
	h.tick()
	gate := make(chan struct{})
	h.ex.loadEnv = func(context.Context) error { <-gate; return nil }
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
		h.m.launches.Wait()
	})
	b := h.add(0)
	h.m.step(false, true)
	h.ex.finish(a, 0)
	h.m.step(false, true)
	if len(drainEvents(t, h)) != 0 {
		t.Fatal("任务仍在启动时记录了排空")
	}
	if _, err := h.m.SetNotifyDone(b, true); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Cancel(b); err != nil {
		t.Fatal(err)
	}
	close(gate)
	h.m.launches.Wait()
	finish := h.events(b, store.EventJobFinished)[0]
	var data struct {
		On bool `json:"notify_done"`
	}
	if err := json.Unmarshal(finish.Data, &data); err != nil || !data.On || len(drainEvents(t, h)) != 1 {
		t.Fatalf("取消启动中的任务未读到最新开关或未结批: %s, %v", finish.Data, err)
	}
}

func TestCancelledQueuedJobOnlyDrainsWhenQueueIsOpen(t *testing.T) {
	h := newHarness(t, 1)
	a := h.add(1)
	if _, _, err := h.m.PauseQueue("manual", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := h.m.Cancel(a); err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.m.ResumeQueue(); err != nil {
		t.Fatal(err)
	}
	if len(drainEvents(t, h)) != 0 {
		t.Fatal("队列暂停期间取消或恢复空队列不应记录排空")
	}
	b := h.add(1)
	if _, err := h.m.Cancel(b); err != nil {
		t.Fatal(err)
	}
	if len(drainEvents(t, h)) != 1 {
		t.Fatal("取消未暂停队列的最后一个任务应记录排空")
	}
}
