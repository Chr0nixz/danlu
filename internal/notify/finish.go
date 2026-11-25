package notify

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"danlu/internal/logs"
	"danlu/internal/store"
)

const maxNoticeItems = 8

type completion struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Reason   string `json:"reason,omitempty"`
	Status   string `json:"status,omitempty"`
	Duration int64  `json:"duration,omitempty"`
	// 结构化字段：发送时按当前语言合成文案。旧持久化批次没有它们，回退 Reason 原文。
	FailKind   string `json:"fail_kind,omitempty"`
	FailDetail string `json:"fail_detail,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
}

// line 在发送时合成：有结构化字段就按当前语言生成，旧批次回退持久化的中文 Reason。
func (s *Service) line(c completion) string {
	switch c.Status {
	case store.StatusSucceeded:
		return s.msg("line.succeeded", map[string]any{"id": c.ID, "name": c.Name, "duration": s.humanDuration(c.Duration)})
	case store.StatusCancelled:
		return s.msg("line.cancelled", map[string]any{"id": c.ID, "name": c.Name, "duration": s.humanDuration(c.Duration)})
	default:
		reason := c.Reason
		if c.FailKind != "" {
			reason = s.failureReason(c.FailKind, c.FailDetail, c.ExitCode)
		}
		return s.msg("line.failed", map[string]any{"id": c.ID, "name": c.Name, "reason": reason})
	}
}

type finishTally struct {
	Count     int `json:"count"`
	Succeeded int `json:"succeeded,omitempty"`
	Cancelled int `json:"cancelled,omitempty"`
}

func (t *finishTally) add(status string) {
	t.Count++
	switch status {
	case store.StatusSucceeded:
		t.Succeeded++
	case store.StatusCancelled:
		t.Cancelled++
	}
}

type batch struct {
	At      int64 `json:"at"`
	FirstID int64 `json:"first_id"`
	finishTally
	Items []completion `json:"items"`
}

func (b *batch) clone() *batch {
	if b == nil {
		return nil
	}
	c := *b
	c.Items = slices.Clone(b.Items)
	return &c
}

type queueSummary struct {
	FirstID  int64 `json:"first_id"`
	Duration int64 `json:"duration"`
	finishTally
	Failures []completion `json:"failures,omitempty"`
}

// Batch 和 QueueBatch 共用窗口。前者已结批或早于汇总开启，后者可以被本批汇总替换。
func (s *state) firstBatch() *batch {
	if s.Batch != nil {
		return s.Batch
	}
	return s.QueueBatch
}

func mergeBatches(earlier, current *batch) *batch {
	if earlier == nil {
		return current
	}
	if current != nil {
		earlier.Count += current.Count
		earlier.Succeeded += current.Succeeded
		earlier.Cancelled += current.Cancelled
		room := max(0, maxNoticeItems-len(earlier.Items))
		earlier.Items = append(earlier.Items, current.Items[:min(room, len(current.Items))]...)
	}
	return earlier
}

func consumeFinished(next *state, e store.Event, paused bool) *completion {
	var data struct {
		Status     string `json:"status"`
		Name       string `json:"name"`
		FailKind   string `json:"fail_kind"`
		FailDetail string `json:"fail_detail"`
		ExitCode   *int   `json:"exit_code"`
		Reason     string `json:"reason"`
		Duration   int64  `json:"duration"`
		NotifyDone bool   `json:"notify_done"`
	}
	if json.Unmarshal(e.Data, &data) != nil || e.JobID == nil {
		return nil
	}
	item := completion{ID: *e.JobID, Name: oneLine(data.Name, 200), Status: data.Status, Duration: max(0, data.Duration)}
	switch data.Status {
	case store.StatusFailed:
		item.Reason = failureReasonZH(data.FailKind, data.ExitCode)
		if data.FailKind == "host_oom" && data.Reason == logs.ReasonMaybeHostOOM {
			item.Reason = "可能是主机内存不足"
		}
		item.FailKind, item.ExitCode = data.FailKind, data.ExitCode
		// start 的 fail_detail 是自由文本的启动错误，推送不展示，也不必存进持久化批次
		if data.FailKind != "start" {
			item.FailDetail = data.FailDetail
		}
		if item.FailDetail == "" && data.FailKind == "host_oom" {
			// 细分码上线前的事件：按中文原文推断三态
			switch data.Reason {
			case logs.ReasonMaybeHostOOM:
				item.FailDetail = logs.DetailOOMGuess137
			case logs.ReasonWorkerHostOOM:
				item.FailDetail = logs.DetailOOMWorker
			}
		}
	case store.StatusSucceeded, store.StatusCancelled:
	default:
		return nil
	}

	target := &next.Batch
	if next.QueueSince != nil && e.ID > *next.QueueSince {
		if next.Summary == nil {
			next.Summary = &queueSummary{FirstID: e.ID}
		}
		next.Summary.add(data.Status)
		next.Summary.Duration += item.Duration
		if data.Status == store.StatusFailed && len(next.Summary.Failures) < maxNoticeItems {
			next.Summary.Failures = append(next.Summary.Failures, item)
		}
		target = &next.QueueBatch
	}
	if !paused && (data.Status == store.StatusFailed || data.NotifyDone) {
		if *target == nil {
			*target = &batch{At: e.TS, FirstID: e.ID}
		}
		(*target).add(data.Status)
		if len((*target).Items) < maxNoticeItems {
			(*target).Items = append((*target).Items, item)
		}
	}
	return &item
}

func fastPauseFor(e store.Event, id *int64) bool {
	if e.Kind != store.EventQueuePaused || e.JobID == nil || id == nil || *e.JobID != *id {
		return false
	}
	var data struct {
		Kind string `json:"kind"`
	}
	return json.Unmarshal(e.Data, &data) == nil && data.Kind == "fail_fast"
}

func (s *state) pausesBatched(e store.Event) bool {
	if !fastPauseFor(e, e.JobID) {
		return false
	}
	for _, b := range []*batch{s.Batch, s.QueueBatch} {
		if b != nil && slices.ContainsFunc(b.Items, func(item completion) bool { return item.ID == *e.JobID }) {
			return true
		}
	}
	return false
}

func (s *state) takeFailure(id int64) *completion {
	for _, target := range []**batch{&s.Batch, &s.QueueBatch} {
		b := *target
		if b == nil {
			continue
		}
		i := slices.IndexFunc(b.Items, func(item completion) bool { return item.ID == id && item.Reason != "" })
		if i >= 0 {
			item := b.Items[i]
			b.Items = slices.Delete(b.Items, i, i+1)
			b.Count--
			if b.Count == 0 {
				*target = nil
			}
			return &item
		}
	}
	return nil
}

func (s *Service) flushBatch(next *state) {
	b := mergeBatches(next.Batch, next.QueueBatch)
	titleKey, verbKey := "title.finished", "verb.finished"
	click := s.baseURL + "/jobs"
	switch {
	case b.Succeeded == b.Count:
		titleKey = "title.succeeded"
	case b.Cancelled == b.Count:
		titleKey = "title.cancelled"
	case b.Succeeded+b.Cancelled == 0:
		titleKey, verbKey, click = "title.failed", "verb.failed", click+"?status=failed"
	}
	lines := make([]string, 0, len(b.Items)+1)
	for _, item := range b.Items {
		lines = append(lines, s.line(item))
	}
	if b.Count > len(b.Items) {
		lines = append(lines, s.msg("batch.more", map[string]any{"n": b.Count - len(b.Items), "verb": s.msg(verbKey, nil)}))
	}
	if b.Count == 1 && len(b.Items) > 0 {
		click = fmt.Sprintf("%s/jobs/%d", s.baseURL, b.Items[0].ID)
	}
	next.Outbox = append(next.Outbox, Message{ID: fmt.Sprintf("danlu-%s-%d", next.Instance, b.FirstID),
		Title: s.hostname + " · " + s.msg(titleKey, nil), Body: strings.Join(lines, "\n"), Click: click, Priority: 3})
	next.Batch, next.QueueBatch = nil, nil
}

func (s *Service) drain(next *state, e store.Event) {
	q := next.Summary
	if q == nil || q.Count < 2 {
		next.Batch = mergeBatches(next.Batch, next.QueueBatch)
	} else {
		failed := q.Count - q.Succeeded - q.Cancelled
		lines := []string{s.msg("drain.summary", map[string]any{
			"total": q.Count, "ok": q.Succeeded, "failed": failed, "cancelled": q.Cancelled, "duration": s.humanDuration(q.Duration),
		})}
		for _, item := range q.Failures {
			lines = append(lines, s.line(item))
		}
		if failed > len(q.Failures) {
			lines = append(lines, s.msg("drain.more", map[string]any{"n": failed - len(q.Failures)}))
		}
		next.Outbox = append(next.Outbox, Message{ID: fmt.Sprintf("danlu-%s-%d", next.Instance, e.ID),
			Title: s.hostname + " · " + s.msg("title.drained", nil), Body: strings.Join(lines, "\n"), Priority: 3,
			Click: fmt.Sprintf("%s/jobs?finished_from_event=%d&finished_to_event=%d", s.baseURL, q.FirstID, e.ID)})
	}
	next.QueueBatch, next.Summary = nil, nil
}

// failureReasonZH 是持久化批次里 Reason 字段的中文原文（历史格式，供旧批次回退）；
// 发送时的本地化合成走 Service.failureReason。
func failureReasonZH(kind string, exit *int) string {
	if reason := map[string]string{"cuda_oom": "CUDA 显存不足", "host_oom": "主机内存不足", "nccl": "NCCL 通信超时或出错",
		"exception": "Python 异常", "command": "命令或路径出错", "reboot": "服务器重启，任务中断", "killed": "进程被外部终止", "start": "任务启动失败", "signal": "进程被信号终止"}[kind]; reason != "" {
		return reason
	}
	if exit != nil {
		return fmt.Sprintf("退出码 %d", *exit)
	}
	return "任务异常结束"
}
