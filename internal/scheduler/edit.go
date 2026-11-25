package scheduler

import (
	"errors"
	"slices"
	"strings"

	"danlu/internal/store"
)

// 修改排队中的任务、批量操作（FEATURES 3.1）和队列顺序、暂缓（FEATURES 3.2）。
//
// 这些操作都持调度锁，并且只改库里 status='queued' 的行：schedule 在锁里读出排队任务并占位，
// 后面 launch 用的是读出来的那一份。所以结果只有两种：在调度读取之前改完，新内容生效；
// 或者任务已经占位，一行也改不到，返回"任务已经开始"。写库失败待重试（pending）的任务、
// 占位后还在准备（starting）的任务也一律拒绝。一批里有一个不行，整批都不改。

// 批量操作
const (
	BatchCancel  = "cancel"
	BatchReplace = "replace"
	BatchHold    = "hold"
	BatchRelease = "release"
	BatchTop     = "top"
)

// 挪动位置
const (
	MoveTop    = "top"
	MoveUp     = "up"
	MoveDown   = "down"
	MoveBottom = "bottom"
)

// maxBatch 是一次批量操作最多的任务数：首页一组最多列 200 个
const maxBatch = 500

// maxFind 是批量替换"查找"和"替换为"的长度上限
const maxFind = 4096

// editable 是排队任务的可改字段，也是 job_edited 事件里记的改前内容。
func editable(j *store.Job) map[string]any {
	pinned := j.GPUsPinned
	if pinned == nil {
		pinned = []int{}
	}
	env := j.Env
	if env == nil {
		env = map[string]string{}
	}
	return map[string]any{"name": j.Name, "memo": j.Memo, "notify_done": j.NotifyDone, "workdir": j.Workdir,
		"conda_env": j.CondaEnv, "cmd": j.Cmd, "env": env, "inherit_bashrc": j.InheritBashrc,
		"gpus": j.GPUsReq, "pinned_gpus": pinned}
}

// queuedLocked 取一个现在可以修改的排队任务。持有 m.mu 时调用。
func (m *Manager) queuedLocked(id int64) (*store.Job, error) {
	if _, ok := m.starting[id]; ok {
		return nil, conflict(invalid("job_already_started", map[string]any{"id": id}, "任务 #%d 已经开始运行，只能修改排队中的任务", id))
	}
	if _, ok := m.pending[id]; ok {
		// 内存里的状态比库里新、还没写进去：库里的"排队"不作数
		return nil, conflict(invalid("job_state_saving", map[string]any{"id": id}, "任务 #%d 的状态还没保存好，稍后再试", id))
	}
	j, err := m.st.GetJob(id)
	if errors.Is(err, store.ErrNotFound) {
		// 批量操作里要说清是哪个编号；接口按码返回 404
		return nil, invalid("job_not_found", map[string]any{"id": id}, "任务 #%d 不存在", id)
	}
	if err != nil {
		return nil, err
	}
	return j, notQueued(j)
}

// IsNotFound：要修改的排队任务不存在（API 返回 404）。
func IsNotFound(err error) bool {
	var ve *ValidationError
	return errors.Is(err, store.ErrNotFound) || errors.As(err, &ve) && ve.Code == "job_not_found"
}

// notQueued：任务不在排队时说明它现在是什么状态
func notQueued(j *store.Job) error {
	switch j.Status {
	case store.StatusQueued:
		return nil
	case store.StatusRunning:
		return conflict(invalid("job_already_started", map[string]any{"id": j.ID}, "任务 #%d 已经开始运行，只能修改排队中的任务", j.ID))
	default:
		return conflict(invalid("job_not_queued", map[string]any{"id": j.ID}, "任务 #%d 已经结束，只能修改排队中的任务", j.ID))
	}
}

// queuedWriteErr 把库里"已经不在排队"翻译成冲突错误。持锁调用时库里的排队任务只有调度器会改，
// 走到这里多半是库被别的进程改过；按库里的实际状态报。
func (m *Manager) queuedWriteErr(err error) error {
	var nq *store.NotQueuedError
	if !errors.As(err, &nq) {
		return err
	}
	j, getErr := m.st.GetJob(nq.ID)
	if getErr != nil || j.Status == store.StatusQueued {
		return conflict(invalid("job_already_started", map[string]any{"id": nq.ID}, "任务 #%d 已经开始运行，只能修改排队中的任务", nq.ID))
	}
	return notQueued(j)
}

// Edit 修改排队中的任务（FEATURES 3.1）。能改的就是提交表单上的字段；rerun_of、code_from 和 submit_key 不变，
// 名字留空时保留原来的名字。编号和排队位置都不变。改前的字段记进 job_edited 事件，和修改在同一个事务里。
// 已经占位过又放回队列的任务，输出目录仍是第一次占位时定下的（和改名一样）。
func (m *Manager) Edit(id int64, req SubmitReq) (*store.Job, error) {
	cur, err := m.st.GetJob(id)
	if err != nil {
		return nil, err
	}
	if err := notQueued(cur); err != nil {
		return nil, err // 已经开始或结束的，不必先做校验
	}
	req.RerunOf, req.CodeFrom, req.SubmitKey = cur.RerunOf, cur.CodeFrom, ""
	if strings.TrimSpace(req.Name) == "" {
		req.Name = cur.Name
	}
	req, pinned, err := m.validate(req)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.queuedLocked(id)
	if err != nil {
		return nil, err
	}
	event := jobEvent(j, store.EventJobEdited, editable(j))
	next := *j
	next.Name, next.Memo, next.NotifyDone, next.Workdir, next.CondaEnv, next.Cmd = req.Name, req.Memo, req.NotifyDone, req.Workdir, req.CondaEnv, req.Cmd
	next.Env, next.InheritBashrc, next.GPUsReq, next.GPUsPinned = req.Env, req.InheritBashrc, req.GPUs, pinned
	if err := m.st.EditQueued([]*store.Job{&next}, event); err != nil {
		return nil, m.queuedWriteErr(err)
	}
	m.st.TouchWorkdir(next.Workdir)
	m.Wake()
	return &next, nil
}

// BatchReq 是对一批排队任务的同一个操作。replace 把命令里的 Find 全部换成 Replace；
// DryRun 只返回预览，不落库。
type BatchReq struct {
	IDs     []int64 `json:"ids"`
	Action  string  `json:"action"`
	Find    string  `json:"find"`
	Replace string  `json:"replace"`
	DryRun  bool    `json:"dry_run"`
}

// BatchItem 是一个任务在这次操作里的结果。替换时带改前改后的命令；Changed 为 false 表示这个任务不受影响
// （命令里没有要找的文字、已经是暂缓的、已经在最前面等）。
type BatchItem struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Changed bool   `json:"changed"`
	Before  string `json:"before,omitempty"`
	After   string `json:"after,omitempty"`
}

type BatchResult struct {
	Action string      `json:"action"`
	DryRun bool        `json:"dry_run"`
	Items  []BatchItem `json:"items"`
}

// Batch 对一批排队任务做同一个操作（FEATURES 3.1、3.2）：取消、替换命令里的文字、暂缓、恢复、置顶。
// 要么全部生效，要么一个也不改：持锁先确认每个任务都还在排队，有一个已经开始或结束就整批放弃，
// 错误里写明是哪个任务。替换、暂缓、恢复、置顶在一个事务里写；取消逐个转换状态，
// 某个写库失败时它留在内存里重试，照样不会启动。
func (m *Manager) Batch(req BatchReq) (*BatchResult, error) {
	switch req.Action {
	case BatchCancel, BatchReplace, BatchHold, BatchRelease, BatchTop:
	default:
		return nil, invalid("batch_action_invalid", map[string]any{"action": req.Action}, "不支持的批量操作: %q", req.Action)
	}
	ids := make([]int64, 0, len(req.IDs))
	for _, id := range req.IDs {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return nil, invalid("batch_empty", nil, "没有选择任务")
	}
	if len(ids) > maxBatch {
		return nil, invalid("batch_too_many", map[string]any{"max": maxBatch}, "一次最多操作 %d 个任务", maxBatch)
	}
	if req.Action == BatchReplace {
		if req.Find == "" {
			return nil, invalid("batch_find_empty", nil, "请填写要查找的文字")
		}
		if len(req.Find) > maxFind || len(req.Replace) > maxFind {
			return nil, invalid("batch_find_too_long", map[string]any{"max": maxFind}, "查找和替换的文字最多 %d 字节", maxFind)
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	jobs := make([]*store.Job, len(ids))
	for i, id := range ids {
		j, err := m.queuedLocked(id)
		if err != nil {
			return nil, err
		}
		jobs[i] = j
	}
	result := &BatchResult{Action: req.Action, DryRun: req.DryRun, Items: make([]BatchItem, len(jobs))}
	for i, j := range jobs {
		result.Items[i] = BatchItem{ID: j.ID, Name: j.Name}
	}
	var err error
	switch req.Action {
	case BatchCancel:
		err = m.batchCancelLocked(jobs, result)
	case BatchReplace:
		err = m.batchReplaceLocked(jobs, req.Find, req.Replace, result)
	case BatchHold, BatchRelease:
		err = m.holdLocked(jobs, req.Action == BatchHold, req.DryRun, result)
	case BatchTop:
		err = m.batchTopLocked(jobs, req.DryRun, result)
	}
	if err != nil {
		return nil, err
	}
	if !req.DryRun {
		m.Wake()
	}
	return result, nil
}

func (m *Manager) batchCancelLocked(jobs []*store.Job, result *BatchResult) error {
	for i := range jobs {
		result.Items[i].Changed = true
	}
	if result.DryRun {
		return nil
	}
	var first error
	for _, j := range jobs {
		// 写库失败时任务留在 pending 里，内存里的"已取消"作数，下一轮重试，不会启动
		if err := m.transition(j, store.StatusCancelled, "用户取消"); err != nil && first == nil {
			first = err
		}
	}
	return first
}

func (m *Manager) batchReplaceLocked(jobs []*store.Job, find, replace string, result *BatchResult) error {
	var changed []*store.Job
	var events []*store.Event
	for i, j := range jobs {
		after := strings.TrimSpace(strings.ReplaceAll(j.Cmd, find, replace))
		item := &result.Items[i]
		item.Before, item.After, item.Changed = j.Cmd, after, after != j.Cmd
		if !item.Changed {
			continue
		}
		if after == "" {
			return invalid("batch_cmd_empty", map[string]any{"id": j.ID}, "替换后任务 #%d 的命令为空", j.ID)
		}
		next := *j
		next.Cmd = after
		changed = append(changed, &next)
		events = append(events, jobEvent(j, store.EventJobEdited, editable(j)))
	}
	if result.DryRun || len(changed) == 0 {
		return nil
	}
	if err := m.st.EditQueued(changed, events...); err != nil {
		return m.queuedWriteErr(err)
	}
	return nil
}

// holdLocked 暂缓或恢复：暂缓的任务留在原位，调度时跳过，也不挡后面的任务；恢复后按原来的位置参与调度。
func (m *Manager) holdLocked(jobs []*store.Job, held, dryRun bool, result *BatchResult) error {
	var ids []int64
	var events []*store.Event
	for i, j := range jobs {
		if j.Held == held {
			continue
		}
		result.Items[i].Changed = true
		ids = append(ids, j.ID)
		events = append(events, jobEvent(j, store.EventJobHeld, map[string]any{"held": held}))
	}
	if dryRun || len(ids) == 0 {
		return nil
	}
	if err := m.st.SetHeld(ids, held, events...); err != nil {
		return m.queuedWriteErr(err)
	}
	return nil
}

// batchTopLocked 把选中的任务按它们现在的先后挪到队首。
func (m *Manager) batchTopLocked(jobs []*store.Job, dryRun bool, result *BatchResult) error {
	queue, err := m.queueLocked()
	if err != nil {
		return err
	}
	selected := map[int64]bool{}
	for _, j := range jobs {
		selected[j.ID] = true
	}
	var order []*store.Job
	for _, j := range queue {
		if selected[j.ID] {
			order = append(order, j)
		}
	}
	for _, j := range queue {
		if !selected[j.ID] {
			order = append(order, j)
		}
	}
	moved, err := m.reorderLocked(queue, order, selected, MoveTop, dryRun)
	if err != nil {
		return err
	}
	for i := range result.Items {
		result.Items[i].Changed = moved[result.Items[i].ID]
	}
	return nil
}

// Move 把一个排队任务置顶、上移、下移或者移到末尾（FEATURES 3.2）。暂缓的任务也占着位置，上移下移会越过它们。
// 已经在那个位置时什么都不做。
func (m *Manager) Move(id int64, to string) (*store.Job, error) {
	switch to {
	case MoveTop, MoveUp, MoveDown, MoveBottom:
	default:
		return nil, invalid("job_move_invalid", map[string]any{"to": to}, "不支持的移动方式: %q", to)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.queuedLocked(id)
	if err != nil {
		return nil, err
	}
	queue, err := m.queueLocked()
	if err != nil {
		return nil, err
	}
	from := slices.IndexFunc(queue, func(q *store.Job) bool { return q.ID == id })
	if from < 0 {
		return j, nil // 库里在排队、却不在可调整的队列里：只会是刚刚被改过，什么都不做
	}
	target := from
	switch to {
	case MoveTop:
		target = 0
	case MoveUp:
		target = max(from-1, 0)
	case MoveDown:
		target = min(from+1, len(queue)-1)
	case MoveBottom:
		target = len(queue) - 1
	}
	if target == from {
		return j, nil
	}
	order := slices.Delete(slices.Clone(queue), from, from+1)
	order = slices.Insert(order, target, queue[from])
	if _, err := m.reorderLocked(queue, order, map[int64]bool{id: true}, to, false); err != nil {
		return nil, err
	}
	m.Wake()
	return m.st.GetJob(id)
}

// queueLocked 是能调整顺序的排队任务，按队列顺序：库里在排队，内存里也没有待重试的副本。
// 待重试的副本（比如放回队列还没写进库）不参与挪动，保留自己的 queue_order。
func (m *Manager) queueLocked() ([]*store.Job, error) {
	queued, err := m.st.JobsByStatus(store.StatusQueued)
	if err != nil {
		return nil, err
	}
	return slices.DeleteFunc(queued, func(j *store.Job) bool {
		_, pending := m.pending[j.ID]
		_, starting := m.starting[j.ID]
		return pending || starting
	}), nil
}

// reorderLocked 按 order 重新排 queue（同一批任务换了先后）：把 queue 现有的 queue_order 值从小到大依次分给 order 里的任务，
// 只写值变了的行。值只在排队任务之间互换，不产生新值：
//   - 放回队列的任务（启动途中被打断）带着自己原来的值回来，不会和别人撞上，回到原来的位置；
//   - 所有值都不大于已经分配过的最大 id，之后提交的任务（queue_order = id）总排在后面。
//
// movers 是用户挪的任务，返回其中位置真的变了的那些，各记一条 job_moved（from/to 是从 1 开始的位置）；
// 被挤动的任务不记。
func (m *Manager) reorderLocked(queue, order []*store.Job, movers map[int64]bool, how string, dryRun bool) (map[int64]bool, error) {
	values := make([]int64, len(queue))
	pos := make(map[int64]int, len(queue))
	for i, j := range queue {
		values[i] = j.QueueOrder
		pos[j.ID] = i
	}
	// 值重复时（只会是绕过 InsertJob 直接写库的旧行）按 id 排的先后不一定是 order 的先后：拉开
	for i := 1; i < len(values); i++ {
		values[i] = max(values[i], values[i-1]+1)
	}
	moved := map[int64]bool{}
	var changes []store.QueueOrder
	var events []*store.Event
	for i, j := range order {
		if j.QueueOrder != values[i] {
			changes = append(changes, store.QueueOrder{ID: j.ID, Order: values[i]})
		}
		if movers[j.ID] && pos[j.ID] != i {
			moved[j.ID] = true
			events = append(events, jobEvent(j, store.EventJobMoved, map[string]any{"from": pos[j.ID] + 1, "to": i + 1, "move": how}))
		}
	}
	if dryRun || len(changes) == 0 {
		return moved, nil
	}
	if err := m.st.SetQueueOrder(changes, events...); err != nil {
		return nil, m.queuedWriteErr(err)
	}
	return moved, nil
}

// Hold 暂缓或恢复一个排队任务。已经是那个状态时什么都不做。
func (m *Manager) Hold(id int64, held bool) (*store.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, err := m.queuedLocked(id)
	if err != nil {
		return nil, err
	}
	if err := m.holdLocked([]*store.Job{j}, held, false, &BatchResult{Items: make([]BatchItem, 1)}); err != nil {
		return nil, err
	}
	m.Wake()
	j.Held = held
	return j, nil
}
