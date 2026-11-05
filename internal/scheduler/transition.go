package scheduler

import (
	"encoding/json"
	"fmt"
	"log"
	"slices"
	"time"

	"danlu/internal/logs"
	"danlu/internal/store"
)

// transition 是改变任务状态的唯一入口，调用方必须持有 m.mu。
// running、pid=0 只是占位；进程真正启动后才记录 job_started。
func (m *Manager) transition(j *store.Job, to, reason string) error {
	hadRecipe := j.Recipe != nil
	j.Status = to
	var events []*store.Event
	switch to {
	case store.StatusRunning:
		if j.PID > 0 {
			if m.runStats[j.ID] == nil {
				now := time.Now()
				m.runStats[j.ID] = &runStat{profile: j.Profile.Clone(), lastSave: now, gpuAt: now}
			}
			events = append(events, startedEvent(j))
		}
	case store.StatusQueued:
		j.PID, j.PIDStart, j.StartedAt, j.GPUsAssigned, j.MasterPort, j.BootID = 0, 0, nil, nil, 0, ""
		j.EndedAt, j.ExitCode, j.CancelRequested, j.FailReason = nil, nil, false, ""
		j.FailKind, j.ExtSeen, j.OOMBase, j.TracebackOffset = "", false, -1, nil
		j.Recipe = nil
		j.Profile = nil
		delete(m.runStats, j.ID)
		events = append(events, jobEvent(j, store.EventJobRequeued, map[string]any{"reason": reason}))
	case store.StatusSucceeded, store.StatusFailed, store.StatusCancelled:
		if stat := m.runStats[j.ID]; stat != nil {
			j.Profile = stat.profile.Clone()
			delete(m.runStats, j.ID)
		}
		now := time.Now().Unix()
		j.EndedAt, j.FailReason = &now, ""
		if to == store.StatusFailed {
			j.FailReason = reason
		} else {
			j.FailKind, j.TracebackOffset = "", nil
		}
		// danlu 可能在进程启动后、保存 pid 前退出；收尾时把启动事件一起补上。
		if j.PID > 0 {
			events = append(events, startedEvent(j))
		}
		var duration int64
		if j.StartedAt != nil {
			duration = max(0, now-*j.StartedAt)
		}
		var errorLine string
		var tracebackOffset *int64
		if to == store.StatusFailed {
			errorLine = logs.ErrorLine(m.ex.LogPath(j.ID))
			tracebackOffset = j.TracebackOffset
		}
		events = append(events, jobEvent(j, store.EventJobFinished, map[string]any{
			"status": to, "name": j.Name, "reason": reason, "fail_kind": j.FailKind, "error_line": errorLine,
			"exit_code": j.ExitCode, "duration": duration, "ext_seen": j.ExtSeen,
			"traceback_offset": tracebackOffset,
		}))
	default:
		panic("未知的任务状态: " + to)
	}
	err := m.persist(j, true, events...)
	if j.Finished() {
		m.queueManifest(j)
	} else if to == store.StatusQueued && hadRecipe {
		m.queueManifest(j, true)
	} else if j.Recipe != nil {
		m.queueManifest(j)
	}
	return err
}

func jobEvent(j *store.Job, kind string, data any) *store.Event {
	b, _ := json.Marshal(data)
	id := j.ID
	e := &store.Event{TS: time.Now().Unix(), Kind: kind, JobID: &id, Data: b}
	if kind != store.EventJobRequeued {
		// 一个任务最多启动一次、结束一次；即使退出后补记，也沿用同一个键。
		e.DedupeKey = fmt.Sprintf("%s:%d", kind, j.ID)
	}
	return e
}

func startedEvent(j *store.Job) *store.Event {
	gpus := slices.Clone(j.GPUsAssigned)
	if gpus == nil {
		gpus = []int{}
	}
	e := jobEvent(j, store.EventJobStarted, map[string]any{"gpus": gpus, "pid": j.PID})
	if j.StartedAt != nil {
		e.TS = *j.StartedAt
	}
	return e
}

// save 只保存状态不变的字段；之前未落库的状态事件仍然会随它一起重试。
func (m *Manager) save(j *store.Job) error { return m.persist(j, false) }

func (m *Manager) persist(j *store.Job, transition bool, events ...*store.Event) error {
	events = append(m.pendingEvents[j.ID], events...)
	var err error
	var pause *QueueState
	finish := slices.IndexFunc(events, func(e *store.Event) bool { return e.Kind == store.EventJobFinished })
	if j.Status == store.StatusFailed && finish >= 0 {
		var event *store.Event
		pause, event, err = m.failFastPauseLocked(j)
		if event != nil {
			events = append(events, event)
		}
	}
	// 无法读取队列或任务时不跳过暂停判断，保留结束事件供下一轮重试。
	if err == nil {
		if pause != nil {
			data, marshalErr := json.Marshal(pause)
			if marshalErr != nil {
				err = marshalErr
			} else {
				err = m.st.TransitionWithSetting(j, queueStateKey, string(data), events...)
			}
		} else if transition || len(events) > 0 {
			toCommit := events
			if finish >= 0 && len(m.starting) == 0 {
				if queue, queueErr := m.queueStateLocked(); queueErr == nil && !queue.Paused {
					toCommit = append(toCommit, &store.Event{Kind: store.EventQueueDrained, TS: events[finish].TS,
						DedupeKey: fmt.Sprintf("queue_drained:%d", j.ID)})
				}
			}
			err = m.st.Transition(j, toCommit...)
		} else {
			err = m.st.SaveJob(j)
		}
	}
	if err != nil {
		log.Printf("保存任务 #%d 失败: %v", j.ID, err)
		m.pending[j.ID], m.pendingEvents[j.ID] = j, events
		return err
	}
	delete(m.pending, j.ID)
	delete(m.pendingEvents, j.ID)
	if pause != nil {
		m.pendingQueue = nil
	}
	return nil
}

// jobsByStatus 以 pending 为准，覆盖库里的旧状态，包括尚未保存的取消和重新排队。
func (m *Manager) jobsByStatus(status string) ([]*store.Job, error) {
	jobs, err := m.st.JobsByStatus(status)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*store.Job, len(jobs))
	for _, j := range jobs {
		byID[j.ID] = j
	}
	for id, j := range m.pending {
		if j.Status == status {
			byID[id] = j
		} else {
			delete(byID, id)
		}
	}
	jobs = jobs[:0]
	for _, j := range byID {
		jobs = append(jobs, j)
	}
	slices.SortFunc(jobs, func(a, b *store.Job) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return jobs, nil
}
