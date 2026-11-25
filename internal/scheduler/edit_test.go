package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/store"
)

// 修改排队任务与批量操作（FEATURES 3.1）、队列顺序与暂缓（FEATURES 3.2）

func (h *harness) startedCmds() map[int64]string {
	h.ex.mu.Lock()
	defer h.ex.mu.Unlock()
	cmds := map[int64]string{}
	for _, s := range h.ex.started {
		cmds[s.JobID] = s.Cmd
	}
	return cmds
}

// queueOrder 是库里排队任务的顺序（和接口的 queue_pos 同一个顺序）
func (h *harness) queueOrder() []int64 {
	h.t.Helper()
	queued, err := h.st.Store.JobsByStatus(store.StatusQueued)
	if err != nil {
		h.t.Fatal(err)
	}
	var ids []int64
	for _, j := range queued {
		ids = append(ids, j.ID)
	}
	return ids
}

func expectCode(t *testing.T, err error, code string, conflict bool) {
	t.Helper()
	var ve *ValidationError
	if !errors.As(err, &ve) || ve.Code != code || ve.Conflict != conflict {
		t.Fatalf("错误应为 %s（冲突=%t）: %#v", code, conflict, err)
	}
}

func editReq(workdir, cmd string) SubmitReq {
	return SubmitReq{Workdir: workdir, Cmd: cmd, GPUs: 1}
}

// 编辑先于调度读取：新命令生效，编号和位置不变，改前的字段记在 job_edited 里
func TestEditBeforeScheduleUsesNewCommand(t *testing.T) {
	h := newHarness(t, 1)
	dir := t.TempDir()
	a, b := h.add(1), h.add(1)
	got, err := h.m.Edit(b, SubmitReq{Workdir: dir, Cmd: "  python train.py --lr 3e-4  ", GPUs: 1, Memo: "改小学习率", Env: map[string]string{"SEED": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != b || got.Name != "j" || got.Cmd != "python train.py --lr 3e-4" || got.Workdir != dir || got.Env["SEED"] != "1" {
		t.Fatalf("修改结果不对（名字留空应保留原名）: %+v", got)
	}
	if order := h.queueOrder(); !slices.Equal(order, []int64{a, b}) {
		t.Fatalf("修改后排队位置变了: %v", order)
	}
	events := h.events(b, store.EventJobEdited)
	var before map[string]any
	if err := json.Unmarshal(events[0].Data, &before); err != nil || before["cmd"] != "x" || before["workdir"] != "/w" || before["name"] != "j" {
		t.Fatalf("job_edited 应记下改前的字段: %s (%v)", events[0].Data, err)
	}
	if ok, err := h.st.HasJobEvent(b, store.EventJobEdited); err != nil || !ok {
		t.Fatalf("详情页读不到修改标记: %v %v", ok, err)
	}
	h.tick() // a 先跑
	h.ex.finish(a, 0)
	h.tick()
	h.expect(b, store.StatusRunning)
	if cmd := h.startedCmds()[b]; cmd != "python train.py --lr 3e-4" {
		t.Fatalf("启动用的是旧命令: %q", cmd)
	}
	h.events(b, store.EventJobEdited, store.EventJobStarted)
}

// 调度先占位：编辑一行也改不到，返回"已经开始"，启动用的仍是占位时读出的命令
func TestEditAfterReserveRejected(t *testing.T) {
	h := newHarness(t, 1)
	release := make(chan struct{})
	h.ex.loadEnv = func(context.Context) error { <-release; return nil }
	a := h.add(1)
	h.m.step(false, true) // 占位，还在锁外准备
	_, err := h.m.Edit(a, editReq(t.TempDir(), "python new.py"))
	expectCode(t, err, "job_already_started", true)
	if !IsConflict(err) {
		t.Fatal("接口应按状态冲突返回 409")
	}
	close(release)
	h.m.launches.Wait()
	h.m.captures.Wait()
	h.m.manifests.Wait()
	if cmd := h.startedCmds()[a]; cmd != "x" {
		t.Fatalf("占位后的修改不应生效: %q", cmd)
	}
	h.events(a, store.EventJobStarted)
	// 结束的任务也不能改
	h.ex.finish(a, 0)
	h.tick()
	_, err = h.m.Edit(a, editReq(t.TempDir(), "python new.py"))
	expectCode(t, err, "job_not_queued", true)
}

// 写库失败待重试的任务：库里还是排队，内存里已经取消，不能改
func TestEditPendingRejected(t *testing.T) {
	h := newHarness(t, 0)
	a := h.add(0)
	h.st.setFail(func(j *store.Job) bool { return j.Status == store.StatusCancelled })
	if _, err := h.m.Cancel(a); err == nil {
		t.Fatal("取消写库失败应返回错误")
	}
	h.expect(a, store.StatusQueued)
	_, err := h.m.Edit(a, editReq(t.TempDir(), "python new.py"))
	expectCode(t, err, "job_state_saving", true)
	_, err = h.m.Hold(a, true)
	expectCode(t, err, "job_state_saving", true)
	_, err = h.m.Move(a, MoveTop)
	expectCode(t, err, "job_state_saving", true)
	_, err = h.m.Batch(BatchReq{IDs: []int64{a}, Action: BatchReplace, Find: "x", Replace: "y"})
	expectCode(t, err, "job_state_saving", true)
	h.st.setFail(nil)
	h.tick()
	h.expect(a, store.StatusCancelled)
	if j := h.job(a); j.Cmd != "x" {
		t.Fatalf("命令被改了: %q", j.Cmd)
	}
}

func TestEditValidation(t *testing.T) {
	h := newHarness(t, 2)
	a := h.add(1)
	_, err := h.m.Edit(a, editReq("/no/such/dir", "python x.py"))
	expectCode(t, err, "job_workdir_missing", false)
	_, err = h.m.Edit(a, editReq(t.TempDir(), "   "))
	expectCode(t, err, "job_cmd_empty", false)
	_, err = h.m.Edit(a, SubmitReq{Workdir: t.TempDir(), Cmd: "x", Env: map[string]string{"CUDA_VISIBLE_DEVICES": "0"}})
	expectCode(t, err, "job_env_reserved", false)
	_, err = h.m.Edit(a, SubmitReq{Workdir: t.TempDir(), Cmd: "x", GPUs: 3})
	expectCode(t, err, "job_gpus_exceed", false)
	h.events(a)

	// rerun_of、code_from、submit_key 不变；指定卡号去重排好
	other := int64(999)
	j, err := h.m.Edit(a, SubmitReq{Name: "新名字", Workdir: t.TempDir(), Cmd: "x", PinnedGPUs: []int{1, 0, 1}, RerunOf: &other, CodeFrom: &other, SubmitKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	got := h.job(a)
	if got.RerunOf != nil || got.CodeFrom != nil || got.SubmitKey != "" || got.Name != "新名字" || !slices.Equal(got.GPUsPinned, []int{0, 1}) || got.GPUsReq != 2 {
		t.Fatalf("修改结果不对: %+v / 返回 %+v", got, j)
	}
}

// 批量替换：先预览，确认后一次改完，命令里没有要找的文字的任务不动
func TestBatchReplacePreviewAndApply(t *testing.T) {
	h := newHarness(t, 0)
	a, b, c := h.add(0), h.add(0), h.add(0)
	for id, cmd := range map[int64]string{a: "python train.py --lr=3e-3", b: "python train.py --lr=1e-3", c: "python eval.py"} {
		j := h.job(id)
		j.Cmd = cmd
		if err := h.st.EditQueued([]*store.Job{j}); err != nil {
			t.Fatal(err)
		}
	}
	h.m.PauseQueue("manual", "", nil) // 别让调度器先把它们跑了
	req := BatchReq{IDs: []int64{a, b, c, a}, Action: BatchReplace, Find: "train.py", Replace: "train.py --amp", DryRun: true}
	res, err := h.m.Batch(req)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 3 || !res.Items[0].Changed || res.Items[0].After != "python train.py --amp --lr=3e-3" || res.Items[2].Changed {
		t.Fatalf("预览不对: %+v", res.Items)
	}
	if h.job(a).Cmd != "python train.py --lr=3e-3" {
		t.Fatal("预览不应落库")
	}
	h.events(a)
	req.DryRun = false
	if _, err := h.m.Batch(req); err != nil {
		t.Fatal(err)
	}
	if h.job(a).Cmd != "python train.py --amp --lr=3e-3" || h.job(b).Cmd != "python train.py --amp --lr=1e-3" || h.job(c).Cmd != "python eval.py" {
		t.Fatalf("替换结果不对: %q %q %q", h.job(a).Cmd, h.job(b).Cmd, h.job(c).Cmd)
	}
	h.events(a, store.EventJobEdited)
	h.events(c)

	_, err = h.m.Batch(BatchReq{IDs: []int64{a}, Action: BatchReplace, Find: h.job(a).Cmd, Replace: " "})
	expectCode(t, err, "batch_cmd_empty", false)
	_, err = h.m.Batch(BatchReq{IDs: []int64{a}, Action: BatchReplace})
	expectCode(t, err, "batch_find_empty", false)
	_, err = h.m.Batch(BatchReq{Action: BatchCancel})
	expectCode(t, err, "batch_empty", false)
	_, err = h.m.Batch(BatchReq{IDs: []int64{a}, Action: "delete"})
	expectCode(t, err, "batch_action_invalid", false)
}

// 批量中途有任务已经开始：整批放弃，说明是哪个任务，其余的一个也不改
func TestBatchAbortsWhenOneStarted(t *testing.T) {
	h := newHarness(t, 1)
	release := make(chan struct{})
	h.ex.loadEnv = func(context.Context) error { <-release; return nil }
	a, b := h.add(1), h.add(1)
	h.m.step(false, true) // a 占位
	for _, action := range []string{BatchReplace, BatchCancel, BatchHold, BatchTop} {
		_, err := h.m.Batch(BatchReq{IDs: []int64{b, a}, Action: action, Find: "x", Replace: "y"})
		expectCode(t, err, "job_already_started", true)
		if ve := (*ValidationError)(nil); !errors.As(err, &ve) || ve.Params["id"] != a {
			t.Fatalf("%s: 错误应指出已经开始的 #%d: %v", action, a, err)
		}
	}
	close(release)
	h.m.launches.Wait()
	h.m.captures.Wait()
	h.m.manifests.Wait()
	if j := h.job(b); j.Cmd != "x" || j.Held || j.Status != store.StatusQueued {
		t.Fatalf("整批放弃时 b 不应被改: %+v", j)
	}
	h.events(b)
}

// 库里的修改是一个事务：有一行不在排队，整批回滚
func TestEditQueuedRollsBackWholeBatch(t *testing.T) {
	h := newHarness(t, 1)
	a, b := h.add(1), h.add(1)
	h.tick() // a 开始运行
	ja, jb := h.job(a), h.job(b)
	jb.Cmd, ja.Cmd = "new-b", "new-a"
	err := h.st.EditQueued([]*store.Job{jb, ja}, jobEvent(jb, store.EventJobEdited, editable(jb)))
	if !errors.Is(err, store.ErrNotQueued) {
		t.Fatalf("应返回 ErrNotQueued: %v", err)
	}
	if h.job(b).Cmd != "x" {
		t.Fatal("同一批里的 b 不应被改")
	}
	h.events(b)
	if err := h.st.SetHeld([]int64{b, a}, true); !errors.Is(err, store.ErrNotQueued) || h.job(b).Held {
		t.Fatalf("暂缓也应整批回滚: %v", err)
	}
}

func TestBatchCancel(t *testing.T) {
	h := newHarness(t, 0)
	h.m.PauseQueue("manual", "", nil)
	a, b, c := h.add(0), h.add(0), h.add(0)
	res, err := h.m.Batch(BatchReq{IDs: []int64{a, c}, Action: BatchCancel})
	if err != nil || len(res.Items) != 2 || !res.Items[0].Changed {
		t.Fatalf("批量取消: %+v %v", res, err)
	}
	h.expect(a, store.StatusCancelled)
	h.expect(c, store.StatusCancelled)
	h.expect(b, store.StatusQueued)
	h.events(a, store.EventJobFinished)
}

// 置顶的任务先启动
func TestMoveTopStartsFirst(t *testing.T) {
	h := newHarness(t, 1)
	a, b, c := h.add(1), h.add(1), h.add(1)
	if _, err := h.m.Move(c, MoveTop); err != nil {
		t.Fatal(err)
	}
	if order := h.queueOrder(); !slices.Equal(order, []int64{c, a, b}) {
		t.Fatalf("置顶后顺序 %v", order)
	}
	h.tick()
	h.expect(c, store.StatusRunning, 0)
	h.expect(a, store.StatusQueued)
	events := h.events(c, store.EventJobMoved, store.EventJobStarted)
	var moved map[string]any
	if err := json.Unmarshal(events[0].Data, &moved); err != nil || moved["from"] != 3.0 || moved["to"] != 1.0 || moved["move"] != "top" {
		t.Fatalf("job_moved 数据不对: %s", events[0].Data)
	}
	h.events(a) // 被挤动的任务不记事件
}

func TestMoveUpDownBottom(t *testing.T) {
	h := newHarness(t, 0)
	h.m.PauseQueue("manual", "", nil)
	a, b, c, d := h.add(0), h.add(0), h.add(0), h.add(0)
	steps := []struct {
		id   int64
		to   string
		want []int64
	}{
		{c, MoveUp, []int64{a, c, b, d}},
		{c, MoveUp, []int64{c, a, b, d}},
		{c, MoveUp, []int64{c, a, b, d}}, // 已经在最前面：不动
		{a, MoveDown, []int64{c, b, a, d}},
		{c, MoveBottom, []int64{b, a, d, c}},
		{d, MoveTop, []int64{d, b, a, c}},
		{c, MoveDown, []int64{d, b, a, c}},
	}
	for i, s := range steps {
		if _, err := h.m.Move(s.id, s.to); err != nil {
			t.Fatal(err)
		}
		if order := h.queueOrder(); !slices.Equal(order, s.want) {
			t.Fatalf("第 %d 步 %s #%d 后顺序 %v，期望 %v", i+1, s.to, s.id, order, s.want)
		}
	}
	// 移到末尾之后提交的任务排在它后面
	e := h.add(0)
	if order := h.queueOrder(); !slices.Equal(order, []int64{d, b, a, c, e}) {
		t.Fatalf("新任务应排在队尾: %v", order)
	}
	_, err := h.m.Move(a, "middle")
	expectCode(t, err, "job_move_invalid", false)
	// 批量置顶：选中的按原来的先后挪到最前面
	if _, err := h.m.Batch(BatchReq{IDs: []int64{e, b}, Action: BatchTop}); err != nil {
		t.Fatal(err)
	}
	if order := h.queueOrder(); !slices.Equal(order, []int64{b, e, d, a, c}) {
		t.Fatalf("批量置顶后顺序 %v", order)
	}
}

// 暂缓的队首不挡后面的任务；恢复后按原来的位置参与调度
func TestHeldHeadDoesNotBlock(t *testing.T) {
	h := newHarness(t, 1)
	a, b, c := h.add(1), h.add(1), h.add(1)
	if _, err := h.m.Hold(a, true); err != nil {
		t.Fatal(err)
	}
	h.tick()
	h.expect(a, store.StatusQueued)
	h.expect(b, store.StatusRunning, 0)
	waits := h.m.Waits()
	if waits[a].Kind != WaitHeld || waits[c].Kind != WaitGPUs {
		t.Fatalf("等待原因: a=%+v c=%+v", waits[a], waits[c])
	}
	if order := h.queueOrder(); !slices.Equal(order, []int64{a, c}) {
		t.Fatalf("暂缓的任务应留在原位: %v", order)
	}
	if _, err := h.m.Hold(a, false); err != nil {
		t.Fatal(err)
	}
	h.ex.finish(b, 0)
	h.tick()
	h.expect(a, store.StatusRunning, 0) // 恢复后仍在 c 前面
	h.expect(c, store.StatusQueued)
	h.events(a, store.EventJobHeld, store.EventJobHeld, store.EventJobStarted)
	// 已经是那个状态时不记事件
	if _, err := h.m.Hold(c, false); err != nil {
		t.Fatal(err)
	}
	h.events(c)
}

// danlu 停止时放回队列的任务回到原来的位置：期间别的任务挪动了也一样
func TestRequeuedJobKeepsPosition(t *testing.T) {
	h := newHarness(t, 1)
	h.ex.loadEnv = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
	a, b, c := h.add(1), h.add(1), h.add(1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.m.Run(ctx); close(done) }()
	h.m.Wake()
	for i := 0; h.job(a).Status != store.StatusRunning; i++ {
		if i == 100 {
			t.Fatal("任务没有占位")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := h.m.Move(c, MoveTop); err != nil { // a 在准备，c 挪到剩下的队首
		t.Fatal(err)
	}
	cancel()
	within(t, 2*time.Second, "Run", func() { <-done })
	h.expect(a, store.StatusQueued)
	if order := h.queueOrder(); !slices.Equal(order, []int64{a, c, b}) {
		t.Fatalf("放回队列后 a 应回到最前面: %v", order)
	}
	h.events(a, store.EventJobRequeued)
}

// 暂缓的任务不触发"队首等待外部进程"告警；告警落在第一个没有暂缓的任务上
func TestHeldJobDoesNotRaiseBlockedAlert(t *testing.T) {
	h := newHarness(t, 1)
	a, b := h.add(1), h.add(1)
	h.m.Hold(a, true)
	h.gpus.set(func(f *fakeGPUs) { f.gpus[0].Procs = []gpu.Proc{{PID: 1234}} })
	h.tick()
	h.tick()
	var keys []string
	for _, e := range alertEvents(t, h.st.Store) {
		keys = append(keys, e.Key)
	}
	if !slices.Equal(keys, []string{fmt.Sprintf("blocked:%d", b)}) {
		t.Fatalf("告警应只落在 #%d 上: %v", b, keys)
	}
	if w := h.m.Waits()[a]; w.Kind != WaitHeld || w.Blocker != nil {
		t.Fatalf("暂缓任务的等待原因: %+v", w)
	}
	// 队首也暂缓了：没有队首，告警恢复
	h.m.Hold(b, true)
	h.tick()
	events := alertEvents(t, h.st.Store)
	if last := events[len(events)-1]; last.Kind != store.EventAlertCleared || last.Key != fmt.Sprintf("blocked:%d", b) {
		t.Fatalf("全部暂缓后告警应恢复: %+v", events)
	}
}

// 只剩暂缓的任务时算跑完：发排空；快速失败也不暂停
func TestOnlyHeldJobsLeftCountsAsDrained(t *testing.T) {
	h := newHarness(t, 1)
	h.m.opt.FailFast = time.Hour
	a, b := h.add(1), h.add(1)
	h.tick()
	h.expect(a, store.StatusRunning)
	h.m.Hold(b, true)
	h.ex.finish(a, 1) // 快速失败
	h.tick()
	h.expect(a, store.StatusFailed)
	if q, _ := h.m.QueueState(); q.Paused {
		t.Fatalf("只剩暂缓任务时不应快速失败暂停: %+v", q)
	}
	if n := len(h.kindEvents(store.EventQueueDrained)); n != 1 {
		t.Fatalf("应记一次排空，实际 %d", n)
	}
	// 还有没暂缓的任务时照常暂停
	h.m.Hold(b, false)
	h.tick()
	h.expect(b, store.StatusRunning)
	c := h.add(1)
	h.ex.finish(b, 1)
	h.tick()
	if q, _ := h.m.QueueState(); !q.Paused || q.JobID == nil || *q.JobID != b {
		t.Fatalf("还有排队任务时应快速失败暂停: %+v", q)
	}
	h.expect(c, store.StatusQueued)
}
