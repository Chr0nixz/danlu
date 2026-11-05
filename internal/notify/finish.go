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
}

func (c completion) line() string {
	switch c.Status {
	case store.StatusSucceeded:
		return fmt.Sprintf("#%d %s：成功，用时 %s", c.ID, c.Name, humanDuration(c.Duration))
	case store.StatusCancelled:
		return fmt.Sprintf("#%d %s：已取消，用时 %s", c.ID, c.Name, humanDuration(c.Duration))
	default:
		// 旧版持久化的批次只有失败项，没有 status。
		return fmt.Sprintf("#%d %s：%s", c.ID, c.Name, c.Reason)
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
		item.Reason = failureReason(data.FailKind, data.ExitCode)
		if data.FailKind == "host_oom" && data.Reason == logs.ReasonMaybeHostOOM {
			item.Reason = "可能是主机内存不足"
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
	kind := "任务结束"
	click := s.baseURL + "/jobs"
	switch {
	case b.Succeeded == b.Count:
		kind = "任务完成"
	case b.Cancelled == b.Count:
		kind = "任务已取消"
	case b.Succeeded+b.Cancelled == 0:
		kind, click = "任务失败", click+"?status=failed"
	}
	lines := make([]string, 0, len(b.Items)+1)
	for _, item := range b.Items {
		lines = append(lines, item.line())
	}
	if b.Count > len(b.Items) {
		verb := "结束"
		if kind == "任务失败" {
			verb = "失败"
		}
		lines = append(lines, fmt.Sprintf("另有 %d 个任务%s，请打开面板查看。", b.Count-len(b.Items), verb))
	}
	if b.Count == 1 && len(b.Items) > 0 {
		click = fmt.Sprintf("%s/jobs/%d", s.baseURL, b.Items[0].ID)
	}
	next.Outbox = append(next.Outbox, Message{ID: fmt.Sprintf("danlu-%s-%d", next.Instance, b.FirstID),
		Title: s.hostname + " · " + kind, Body: strings.Join(lines, "\n"), Click: click, Priority: 3})
	next.Batch, next.QueueBatch = nil, nil
}

func (s *Service) drain(next *state, e store.Event) {
	q := next.Summary
	if q == nil || q.Count < 2 {
		next.Batch = mergeBatches(next.Batch, next.QueueBatch)
	} else {
		failed := q.Count - q.Succeeded - q.Cancelled
		lines := []string{fmt.Sprintf("%d 个任务，%d 成功，%d 失败，%d 取消。合计任务用时 %s。",
			q.Count, q.Succeeded, failed, q.Cancelled, humanDuration(q.Duration))}
		for _, item := range q.Failures {
			lines = append(lines, item.line())
		}
		if failed > len(q.Failures) {
			lines = append(lines, fmt.Sprintf("另有 %d 个任务失败，请打开面板查看。", failed-len(q.Failures)))
		}
		next.Outbox = append(next.Outbox, Message{ID: fmt.Sprintf("danlu-%s-%d", next.Instance, e.ID),
			Title: s.hostname + " · 队列已跑完", Body: strings.Join(lines, "\n"), Priority: 3,
			Click: fmt.Sprintf("%s/jobs?finished_from_event=%d&finished_to_event=%d", s.baseURL, q.FirstID, e.ID)})
	}
	next.QueueBatch, next.Summary = nil, nil
}
