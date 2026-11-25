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

// 锁外调助手期间被取消，助手随后失败：记成取消，不能放回队列、等助手恢复后照常启动占卡
func TestStrictCancelledWhileStartFails(t *testing.T) {
	h, gate := strictHarness(t, 1)
	a := h.add(1)
	h.ex.mu.Lock()
	h.ex.strictErr = &strict.Error{Reason: strict.ReasonFailed, Detail: "systemd-run 失败"}
	h.ex.strictHook = func(runner.Spec) {
		if _, err := h.m.Cancel(a); err != nil {
			t.Error(err)
		}
	}
	h.ex.mu.Unlock()
	h.tick()
	h.expect(a, store.StatusCancelled)
	gate.set(strict.Status{OK: true, CheckedAt: 2})
	h.ex.mu.Lock()
	h.ex.strictErr, h.ex.strictHook = nil, nil
	h.ex.mu.Unlock()
	h.tick()
	h.expect(a, store.StatusCancelled)
	if n := len(h.ex.startedIDs()); n != 0 {
		t.Fatalf("取消了的任务不能再启动: %d 次", n)
	}
}

// 助手报错，但单元其实已经起来了（回答没传回来）：认领它，不放回队列再起一份
func TestStrictStartErrorButUnitRunning(t *testing.T) {
	h, _ := strictHarness(t, 1)
	a := h.add(1)
	h.ex.mu.Lock()
	h.ex.strictFn = func(s runner.Spec) (runner.Started, error) {
		h.ex.pidFile[s.JobID], h.ex.alive[4242] = 4242, true
		return runner.Started{}, &strict.Error{Reason: strict.ReasonFailed, Detail: "助手的回答无法解析"}
	}
	h.ex.mu.Unlock()
	h.tick()
	j := h.job(a)
	if j.Status != store.StatusRunning || j.PID != 4242 || j.PIDStart != startOf(4242) {
		t.Fatalf("起来了的单元应该被认领: %+v", j)
	}
	h.tick()
	h.expect(a, store.StatusRunning)
	h.ex.mu.Lock()
	h.ex.alive[4242], h.ex.exit[a] = false, 0
	h.ex.mu.Unlock()
	h.tick()
	h.expect(a, store.StatusSucceeded)
}

// 助手报错、认领不到：先按标记清掉可能已经起来的进程，再放回队列
func TestStrictStartErrorKillsStrays(t *testing.T) {
	h, _ := strictHarness(t, 1)
	a := h.add(1)
	h.ex.mu.Lock()
	h.ex.leftovers = 2
	h.ex.strictErr = &strict.Error{Reason: strict.ReasonFailed, Detail: "等助手超时"}
	h.ex.mu.Unlock()
	h.tick()
	h.expect(a, store.StatusQueued)
	h.ex.mu.Lock()
	left := h.ex.leftovers
	h.ex.mu.Unlock()
	if left != 0 {
		t.Fatalf("放回队列前应按标记清掉残留进程，还剩 %d 个", left)
	}
}

// 助手说成功，但服务已经结束、拿不到活着的包装脚本
func TestStrictStartWithoutPID(t *testing.T) {
	h, _ := strictHarness(t, 2)

	// 没有 pid 文件：包装脚本没运行（job-exec 先失败了）。判启动失败，不当成"从没启动过"每轮重试
	a := h.add(1)
	h.ex.mu.Lock()
	h.ex.strictFn = func(s runner.Spec) (runner.Started, error) {
		h.ex.started = append(h.ex.started, s)
		return runner.Started{Unit: "danlu-job-test.service"}, nil
	}
	h.ex.mu.Unlock()
	h.tick()
	h.tick()
	j := h.job(a)
	if j.Status != store.StatusFailed || j.FailKind != "start" || !strings.Contains(j.FailReason, "包装脚本没有运行") {
		t.Fatalf("没有运行包装脚本应判启动失败: %+v", j)
	}
	if n := len(h.ex.startedIDs()); n != 1 {
		t.Fatalf("不能反复重试: 启动了 %d 次", n)
	}

	// 有 pid 文件和退出码：一启动就跑完了，按退出码收尾
	b := h.add(1)
	h.ex.mu.Lock()
	h.ex.strictFn = func(s runner.Spec) (runner.Started, error) {
		h.ex.pidFile[s.JobID], h.ex.exit[s.JobID] = 4343, 0
		return runner.Started{Unit: "danlu-job-test-b.service"}, nil
	}
	h.ex.mu.Unlock()
	h.tick()
	if j := h.job(b); j.Status != store.StatusSucceeded || j.Unit != "danlu-job-test-b.service" {
		t.Fatalf("跑完了的任务应按退出码收尾: %+v", j)
	}
}

// 取值里的 NUL 在严格任务 NUL 分隔的环境文件里会拆出一条新变量，提交时就拒绝
func TestSubmitRejectsNULInEnvValue(t *testing.T) {
	h := newHarness(t, 1)
	_, err := h.m.Submit(SubmitReq{Workdir: t.TempDir(), Cmd: "true", Env: map[string]string{"FOO": "a\x00CUDA_VISIBLE_DEVICES=0,1"}})
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code != "job_env_value_nul" || ve.Params["name"] != "FOO" {
		t.Fatalf("应拒绝含 NUL 的环境变量值: %v", err)
	}
}
