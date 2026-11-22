package scheduler

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/runner"
	"danlu/internal/store"
	"danlu/internal/strict"
)

// fakeGate 模拟 strict.Monitor：Fail 记下来，状态由测试直接改
type fakeGate struct {
	mu    sync.Mutex
	st    strict.Status
	fails []error
}

func (g *fakeGate) Status() strict.Status {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.st
}

func (g *fakeGate) Fail(err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.fails = append(g.fails, err)
	g.st = strict.Status{Reason: strict.ReasonFailed, Detail: err.Error(), CheckedAt: time.Now().Unix()}
}

func (g *fakeGate) set(st strict.Status) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.st = st
}

func strictHarness(t *testing.T, nGPU int) (*harness, *fakeGate) {
	h := newHarness(t, nGPU)
	h.gpus.set(func(f *fakeGPUs) {
		for i := range f.gpus {
			f.gpus[i].BusID = []string{"00000000:08:00.0", "00000000:09:00.0", "00000000:0E:00.0", "00000000:11:00.0"}[i]
		}
	})
	gate := &fakeGate{st: strict.Status{OK: true, CheckedAt: 1}}
	h.m.opt.Strict, h.m.opt.StrictGate = true, gate
	return h, gate
}

func TestStrictStart(t *testing.T) {
	h, _ := strictHarness(t, 4)
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs = []gpu.Proc{{PID: 9, UsedMemMB: 4096}} })
	a := h.add(2)
	h.tick()
	h.expect(a, store.StatusRunning, 1, 2)
	h.ex.mu.Lock()
	spec := h.ex.started[0]
	h.ex.mu.Unlock()
	if !spec.Strict || !slices.Equal(spec.GPUBuses, []string{"00000000:09:00.0", "00000000:0E:00.0"}) {
		t.Fatalf("严格任务要带上分到的卡的总线号: %+v", spec)
	}
	j := h.job(a)
	if j.Unit != "danlu-job-test-1.service" || j.PID == 0 || j.OOMBase != -1 {
		t.Fatalf("应记下单元名和 pid，不用整个 danlu cgroup 的 OOM 基线: unit=%q pid=%d base=%d", j.Unit, j.PID, j.OOMBase)
	}

	// 自己 cgroup 的 OOM 计数说了算，即使整个 danlu cgroup 的计数没变
	h.ex.mu.Lock()
	h.ex.jobOOM = map[int64]int64{a: 1}
	h.ex.mu.Unlock()
	h.ex.crash(a)
	h.tick()
	if j := h.job(a); j.FailKind != "host_oom" {
		t.Fatalf("严格任务 OOM 归类错误: %+v", j)
	}

	// 关掉以后新任务走普通启动
	h.m.opt.Strict = false
	b := h.add(1)
	h.tick()
	h.expect(b, store.StatusRunning)
	if j := h.job(b); j.Unit != "" {
		t.Fatalf("普通任务没有单元名: %q", j.Unit)
	}
}

func TestStrictGateHoldsQueue(t *testing.T) {
	h, gate := strictHarness(t, 2)
	gate.set(strict.Status{Reason: strict.ReasonNoSudo, CheckedAt: 1})
	a, b := h.add(1), h.add(0)
	h.tick()
	h.expect(a, store.StatusQueued)
	h.expect(b, store.StatusQueued) // 不用卡的任务也等着：开着严格限卡就不能有不隔离的任务
	updateAlerts(t, h.m, time.Now())
	if r := h.m.WaitReasons()[a]; !strings.Contains(r, "严格限卡不可用") || !strings.Contains(r, "sudo danlu strict install") {
		t.Fatalf("等待原因要写明怎么修: %q", r)
	}
	events := alertEvents(t, h.st.Store)
	if len(events) != 1 || events[0].Key != "strict_unavailable" || events[0].JobID != nil {
		t.Fatalf("应有一条严格限卡告警: %+v", events)
	}
	var data store.AlertData
	if json.Unmarshal(events[0].Data, &data) != nil || data.Reason != strict.ReasonNoSudo {
		t.Fatalf("告警只带原因代码: %s", events[0].Data)
	}

	// 启动后还没查完：不告警，但照样不启动
	gate.set(strict.Status{Reason: strict.ReasonChecking})
	h.tick()
	h.expect(a, store.StatusQueued)

	gate.set(strict.Status{OK: true, CheckedAt: 2})
	h.tick()
	h.expect(a, store.StatusRunning)
	h.expect(b, store.StatusRunning)
	updateAlerts(t, h.m, time.Now())
	if events := alertEvents(t, h.st.Store); len(events) != 2 || events[1].Kind != store.EventAlertCleared {
		t.Fatalf("助手恢复后告警应清除: %+v", events)
	}
}

// 助手出错：第一次放回队列、叫后台复查；复查通过后同一任务再失败就判失败，不来回重试。绝不退回不隔离运行
func TestStrictStartFailure(t *testing.T) {
	h, gate := strictHarness(t, 1)
	h.ex.mu.Lock()
	h.ex.strictErr = &strict.Error{Reason: strict.ReasonFailed, Detail: "systemd-run 失败"}
	h.ex.mu.Unlock()
	a := h.add(1)
	h.tick()
	h.expect(a, store.StatusQueued)
	if len(gate.fails) != 1 {
		t.Fatalf("启动失败应通知助手状态: %d 次", len(gate.fails))
	}
	h.tick()
	h.expect(a, store.StatusQueued) // 复查之前不再启动
	h.ex.mu.Lock()
	if n := len(h.ex.started); n != 0 {
		t.Fatalf("不能退回不隔离启动: %d", n)
	}
	h.ex.mu.Unlock()

	gate.set(strict.Status{OK: true, CheckedAt: 2})
	h.tick()
	j := h.job(a)
	if j.Status != store.StatusFailed || j.FailKind != "start" || !strings.Contains(j.FailReason, "systemd-run 失败") {
		t.Fatalf("复查通过后再失败应判失败: %+v", j)
	}

	if len(gate.fails) != 2 {
		t.Fatalf("第二次失败同样要通知助手状态: %d 次", len(gate.fails))
	}

	// 不是助手的问题（比如写脚本失败）直接判失败，也不把助手标成不可用
	gate.set(strict.Status{OK: true, CheckedAt: 3})
	h.ex.mu.Lock()
	h.ex.strictErr = errors.New("磁盘满了")
	h.ex.mu.Unlock()
	b := h.add(0)
	h.tick()
	if j := h.job(b); j.Status != store.StatusFailed || j.FailKind != "start" || len(gate.fails) != 2 {
		t.Fatalf("其他启动错误直接判失败: %+v（通知 %d 次）", j, len(gate.fails))
	}
}

// 在锁外调助手的几百毫秒里被取消：进程已经起来了，记下 pid 再按运行中的任务取消
func TestStrictCancelDuringStart(t *testing.T) {
	h, _ := strictHarness(t, 1)
	a := h.add(1)
	h.ex.mu.Lock()
	h.ex.strictHook = func(runner.Spec) {
		if _, err := h.m.Cancel(a); err != nil {
			t.Error(err)
		}
	}
	h.ex.mu.Unlock()
	h.tick()
	j := h.job(a)
	if j.Status != store.StatusRunning || !j.CancelRequested || j.PID == 0 || j.Unit == "" {
		t.Fatalf("应记下 pid 并进入取消流程: %+v", j)
	}
	h.ex.mu.Lock()
	terms := slices.Clone(h.ex.terms)
	h.ex.mu.Unlock()
	if !slices.Contains(terms, a) {
		t.Fatalf("应给已经起来的进程发结束信号: %v", terms)
	}
	h.ex.finish(a, 143)
	h.tick()
	h.expect(a, store.StatusCancelled)
}
