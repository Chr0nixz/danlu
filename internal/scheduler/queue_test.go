package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"danlu/internal/store"
)

func TestFailFastPausesQueueAndPersistsAcrossRestart(t *testing.T) {
	h := newHarness(t, 2)
	h.m.opt.FailFast = 2 * time.Minute
	a, running, next := h.add(1), h.add(1), h.add(1)
	h.tick()
	h.ex.finish(a, 1)
	h.tick()
	h.expect(a, store.StatusFailed)
	h.expect(running, store.StatusRunning)
	h.expect(next, store.StatusQueued)
	state, err := h.m.QueueState()
	if err != nil || !state.Paused || state.Kind != "fail_fast" || state.JobID == nil || *state.JobID != a || !strings.Contains(state.Reason, h.job(a).FailReason) {
		t.Fatalf("快速失败暂停状态 = %+v, %v", state, err)
	}
	h.events(a, store.EventJobStarted, store.EventJobFinished, store.EventQueuePaused)
	h.ex.finish(running, 0)
	h.tick()
	h.expect(running, store.StatusSucceeded)
	h.expect(next, store.StatusQueued)

	h.m = h.manager()
	h.m.opt.FailFast = 2 * time.Minute
	h.m.Recover()
	h.tick()
	if got, err := h.m.QueueState(); err != nil || got.Since != state.Since || got.Reason != state.Reason {
		t.Fatalf("重启后暂停状态改变: %+v, %v", got, err)
	}
	h.expect(next, store.StatusQueued)
	if _, changed, err := h.m.ResumeQueue(); err != nil || !changed {
		t.Fatalf("恢复失败: changed=%t, %v", changed, err)
	}
	// 异步配方回写等无状态变化的保存，不应再次触发同一失败。
	h.m.mu.Lock()
	err = h.m.save(h.job(a))
	h.m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	h.tick()
	h.expect(next, store.StatusRunning)
	h.events(a, store.EventJobStarted, store.EventJobFinished, store.EventQueuePaused, store.EventQueueResumed)
}

func TestFailFastConditions(t *testing.T) {
	for _, tc := range []struct {
		name                                     string
		code                                     int
		cancel, disabled, slow, noQueued, manual bool
	}{
		{name: "成功", code: 0},
		{name: "用户取消", code: 1, cancel: true},
		{name: "关闭", code: 1, disabled: true},
		{name: "超过阈值", code: 1, slow: true},
		{name: "没有排队任务", code: 1, noQueued: true},
		{name: "保留手动暂停", code: 1, manual: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t, 1)
			if !tc.disabled {
				h.m.opt.FailFast = 2 * time.Minute
			}
			a := h.add(1)
			var next int64
			if !tc.noQueued {
				next = h.add(1)
			}
			h.tick()
			if tc.slow {
				j := h.job(a)
				start := time.Now().Add(-3 * time.Minute).Unix()
				j.StartedAt = &start
				if err := h.st.SaveJob(j); err != nil {
					t.Fatal(err)
				}
			}
			if tc.cancel {
				if _, err := h.m.Cancel(a); err != nil {
					t.Fatal(err)
				}
			}
			if tc.manual {
				if _, _, err := h.m.PauseQueue("manual", "等我检查", nil); err != nil {
					t.Fatal(err)
				}
			}
			h.ex.finish(a, tc.code)
			h.tick()
			state, err := h.m.QueueState()
			if err != nil || state.Paused != tc.manual || (tc.manual && (state.Kind != "manual" || state.Reason != "等我检查")) {
				t.Fatalf("不应触发快速失败暂停: %+v, %v", state, err)
			}
			if next != 0 && !tc.manual {
				h.expect(next, store.StatusRunning)
			}
		})
	}
}

func TestFailFastTimeBoundary(t *testing.T) {
	m := &Manager{opt: Options{FailFast: 2 * time.Minute}}
	for _, tc := range []struct {
		name       string
		start, end int64
		want       bool
	}{
		{name: "不足一秒", start: 1000, end: 1000, want: true},
		{name: "阈值内", start: 1000, end: 1119, want: true},
		{name: "正好达到阈值", start: 1000, end: 1120},
		{name: "时钟回退", start: 1000, end: 999},
	} {
		t.Run(tc.name, func(t *testing.T) {
			j := &store.Job{Status: store.StatusFailed, StartedAt: &tc.start, EndedAt: &tc.end}
			if got := m.isFastFailure(j); got != tc.want {
				t.Fatalf("快速失败判断 = %t，期望 %t", got, tc.want)
			}
		})
	}
	if m.isFastFailure(&store.Job{Status: store.StatusFailed}) {
		t.Fatal("尚未启动的排队任务不应触发快速失败暂停")
	}
}

func TestFailFastLaunchFailure(t *testing.T) {
	for _, envFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "启动进程失败", true: "加载环境失败"}[envFailure], func(t *testing.T) {
			h := newHarness(t, 1)
			h.m.opt.FailFast = 2 * time.Minute
			if envFailure {
				h.ex.loadEnv = func(context.Context) error { return errors.New("加载环境出错") }
			} else {
				h.ex.startErr = errors.New("启动进程出错")
			}
			a, next := h.add(1), h.add(1)
			h.tick()
			h.tick()
			h.expect(a, store.StatusFailed)
			h.expect(next, store.StatusQueued)
			if state, err := h.m.QueueState(); err != nil || !state.Paused || state.Kind != "fail_fast" {
				t.Fatalf("启动失败未暂停: %+v, %v", state, err)
			}
			h.events(a, store.EventJobFinished, store.EventQueuePaused)
		})
	}
}

func TestFailFastRecovery(t *testing.T) {
	h := newHarness(t, 1)
	a, next := h.add(1), h.add(1)
	h.tick()
	h.ex.finish(a, 1)
	h.m = h.manager()
	h.m.opt.FailFast = 2 * time.Minute
	h.m.Recover()
	h.tick()
	h.expect(a, store.StatusFailed)
	h.expect(next, store.StatusQueued)
	h.events(a, store.EventJobStarted, store.EventJobFinished, store.EventQueuePaused)
}

func TestFailFastWriteFailureBlocksUntilSaved(t *testing.T) {
	h := newHarness(t, 1)
	h.m.opt.FailFast = 2 * time.Minute
	a, next := h.add(1), h.add(1)
	h.tick()
	h.st.setQueueFailure(true)
	h.ex.finish(a, 1)
	h.tick()
	h.tick()
	h.expect(next, store.StatusQueued)
	h.expect(a, store.StatusRunning) // 数据库事务失败，内存里以 failed 为准。
	if state, err := h.m.QueueState(); err != nil || !state.Paused || state.Kind != "fail_fast" {
		t.Fatalf("尚未落库的自动暂停应立即生效: %+v, %v", state, err)
	}
	if state, changed, err := h.m.ResumeQueue(); err == nil || changed || !state.Paused {
		t.Fatalf("写库失败时不能越过暂停恢复调度: %+v, %t, %v", state, changed, err)
	}
	h.st.setQueueFailure(false)
	if _, changed, err := h.m.ResumeQueue(); err != nil || !changed {
		t.Fatalf("保存恢复后应允许继续: %t, %v", changed, err)
	}
	h.tick()
	h.expect(a, store.StatusFailed)
	h.expect(next, store.StatusRunning)
	h.events(a, store.EventJobStarted, store.EventJobFinished, store.EventQueuePaused, store.EventQueueResumed)
}

func TestFailFastReadFailureRetried(t *testing.T) {
	for _, queueFailure := range []bool{false, true} {
		t.Run(map[bool]string{false: "读取排队任务失败", true: "读取队列状态失败"}[queueFailure], func(t *testing.T) {
			h := newHarness(t, 1)
			h.m.opt.FailFast = 2 * time.Minute
			a, next := h.add(1), h.add(1)
			h.tick()
			h.st.mu.Lock()
			h.st.failQueueRead, h.st.failQueuedRead = queueFailure, !queueFailure
			h.st.mu.Unlock()
			h.ex.finish(a, 1)
			h.tick()
			h.expect(a, store.StatusRunning)
			h.expect(next, store.StatusQueued)
			h.st.mu.Lock()
			h.st.failQueueRead, h.st.failQueuedRead = false, false
			h.st.mu.Unlock()
			h.tick()
			h.expect(a, store.StatusFailed)
			h.expect(next, store.StatusQueued)
			h.events(a, store.EventJobStarted, store.EventJobFinished, store.EventQueuePaused)
		})
	}
}

func TestRecoverClearsInterruptedRecipeOnFinishedJobs(t *testing.T) {
	for _, status := range []string{store.StatusSucceeded, store.StatusFailed, store.StatusCancelled} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t, 1)
			id := h.add(1)
			j := h.job(id)
			now := time.Now().Unix()
			j.Status, j.EndedAt, j.Recipe = status, &now, &store.Recipe{Pending: true}
			if err := h.st.SaveJob(j); err != nil {
				t.Fatal(err)
			}
			h.m = h.manager()
			h.m.Recover()
			h.m.manifests.Wait()
			j = h.job(id)
			if j.Status != status || j.Recipe == nil || j.Recipe.Pending || j.Recipe.EnvError != "danlu 重启时环境记录未完成" {
				t.Fatalf("重启后仍在等待失去的采集者: %+v", j)
			}
			if ok, err := h.st.ClaimPrune(id); err != nil || !ok {
				t.Fatalf("已结束任务仍不能清理: %t %v", ok, err)
			}
		})
	}
}
