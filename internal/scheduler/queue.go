package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"danlu/internal/store"
)

const queueStateKey = "queue.state"

type QueueState struct {
	Paused bool   `json:"paused"`
	Kind   string `json:"kind,omitempty"`
	Reason string `json:"reason,omitempty"`
	Since  int64  `json:"since,omitempty"`
	JobID  *int64 `json:"job_id,omitempty"`
}

func (m *Manager) QueueState() (QueueState, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.queueStateLocked()
}

func (m *Manager) PauseQueue(kind, reason string, jobID *int64) (QueueState, bool, error) {
	if kind != "manual" && kind != "fail_fast" && kind != "disk" {
		return QueueState{}, false, fmt.Errorf("队列暂停类型无效: %q", kind)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pauseQueueLocked(kind, reason, jobID)
}

func (m *Manager) pauseQueueLocked(kind, reason string, jobID *int64) (QueueState, bool, error) {
	current, err := m.queueStateLocked()
	if err != nil || current.Paused {
		return current, false, err
	}
	if reason = strings.TrimSpace(reason); reason == "" {
		reason = "手动暂停"
	}
	state := QueueState{Paused: true, Kind: kind, Reason: reason, Since: time.Now().Unix()}
	if jobID != nil {
		id := *jobID
		state.JobID = &id
	}
	data, err := json.Marshal(state)
	if err != nil {
		return QueueState{}, false, err
	}
	event := &store.Event{Kind: store.EventQueuePaused, Key: "queue", JobID: state.JobID, Data: data}
	if kind == "disk" {
		m.diskPause, m.diskPauseEvent = &state, event
	}
	if err := m.st.SetSettingWithEvent(queueStateKey, string(data), event); err != nil {
		return QueueState{}, false, err
	}
	if kind == "disk" {
		m.diskPause, m.diskPauseEvent = nil, nil
	}
	m.Wake()
	return state, true, nil
}

func (m *Manager) ResumeQueue() (QueueState, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, err := m.queueStateLocked()
	if err != nil || !current.Paused {
		return current, false, err
	}
	if err := m.canResumeDiskLocked(current); err != nil {
		return current, false, err
	}
	return m.resumeQueueLocked(current)
}

func (m *Manager) resumeQueueLocked(current QueueState) (QueueState, bool, error) {
	if m.pendingQueue != nil {
		// 先提交失败与暂停，再记录恢复；写库仍失败时不能越过保护继续调度。
		if err := m.save(m.pending[*m.pendingQueue.JobID]); err != nil {
			return current, false, fmt.Errorf("保存快速失败暂停后才能恢复队列: %w", err)
		}
	}
	if err := m.retryDiskPauseLocked(); err != nil {
		return current, false, err
	}
	data, err := json.Marshal(current)
	if err != nil {
		return QueueState{}, false, err
	}
	event := &store.Event{Kind: store.EventQueueResumed, Key: "queue", JobID: current.JobID, Data: data}
	clear, err := json.Marshal(QueueState{})
	if err != nil {
		return QueueState{}, false, err
	}
	if err := m.st.SetSettingWithEvent(queueStateKey, string(clear), event); err != nil {
		return QueueState{}, false, err
	}
	m.Wake()
	return QueueState{}, true, nil
}

func (m *Manager) queueStateLocked() (QueueState, error) {
	if m.pendingQueue != nil {
		state := *m.pendingQueue
		id := *state.JobID
		state.JobID = &id
		return state, nil
	}
	if m.diskPause != nil {
		return *m.diskPause, nil
	}
	value, err := m.st.GetSetting(queueStateKey)
	if errors.Is(err, store.ErrNotFound) {
		return QueueState{}, nil
	}
	if err != nil {
		return QueueState{}, err
	}
	var state QueueState
	if err := json.Unmarshal([]byte(value), &state); err != nil {
		return QueueState{}, fmt.Errorf("队列状态无效: %w", err)
	}
	if !state.Paused {
		return QueueState{}, nil
	}
	if state.Since == 0 || (state.Kind != "manual" && state.Kind != "fail_fast" && state.Kind != "disk") {
		return QueueState{}, errors.New("队列暂停状态字段无效")
	}
	return state, nil
}

// failFastPauseLocked 只在结束事件尚未提交时调用；重试沿用第一次暂停的原因和事件。
func (m *Manager) failFastPauseLocked(j *store.Job) (*QueueState, *store.Event, error) {
	if m.pendingQueue != nil && *m.pendingQueue.JobID == j.ID {
		return m.pendingQueue, nil, nil
	}
	if !m.isFastFailure(j) {
		return nil, nil, nil
	}
	current, err := m.queueStateLocked()
	if err != nil || (current.Paused && current.Kind != "disk") {
		return nil, nil, err
	}
	queued, err := m.jobsByStatus(store.StatusQueued)
	if err != nil || len(queued) == 0 {
		return nil, nil, err
	}
	id := j.ID
	state := &QueueState{Paused: true, Kind: "fail_fast", Since: time.Now().Unix(), JobID: &id,
		Reason: fmt.Sprintf("任务 #%d 启动 %d 秒后失败：%s", j.ID, *j.EndedAt-*j.StartedAt, j.FailReason)}
	data, err := json.Marshal(state)
	if err != nil {
		return nil, nil, err
	}
	// 快速失败需要人工确认，不能在磁盘恢复时跟着自动继续。
	m.diskPause, m.diskPauseEvent = nil, nil
	m.pendingQueue = state
	return state, &store.Event{Kind: store.EventQueuePaused, Key: "queue", JobID: &id, Data: data}, nil
}

func (m *Manager) isFastFailure(j *store.Job) bool {
	if m.opt.FailFast <= 0 || j.Status != store.StatusFailed || j.StartedAt == nil || j.EndedAt == nil || *j.EndedAt < *j.StartedAt {
		return false
	}
	return time.Unix(*j.EndedAt, 0).Sub(time.Unix(*j.StartedAt, 0)) < m.opt.FailFast
}
