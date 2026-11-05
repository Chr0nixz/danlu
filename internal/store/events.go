package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	EventJobStarted   = "job_started"
	EventJobFinished  = "job_finished"
	EventJobRequeued  = "job_requeued"
	EventQueuePaused  = "queue_paused"
	EventQueueResumed = "queue_resumed"
	EventQueueDrained = "queue_drained"
	EventAlertRaised  = "alert_raised"
	EventAlertCleared = "alert_cleared"
)

// Event 的 id 是读取游标；DedupeKey 标识同一件事，写库重试时保持不变。
// Key 留给告警，用于区分哪张卡、哪个分区等，不承担去重职责。
type Event struct {
	ID        int64           `json:"id"`
	TS        int64           `json:"ts"`
	Kind      string          `json:"kind"`
	Key       string          `json:"key"`
	JobID     *int64          `json:"job_id"`
	Data      json.RawMessage `json:"data"`
	DedupeKey string          `json:"-"`
}

// Transition 把任务和对应事件一起写入：任何一步失败都不留下半次状态变化。
// events 可以包含之前尚未落库的事件，按发生顺序传入；重复的去重键不会再次写入。
func (s *Store) Transition(j *Job, events ...*Event) error {
	return s.transition(j, "", "", events...)
}

// TransitionWithSetting 把任务、运行状态和事件一起提交，避免任务已失败而自动暂停丢失。
func (s *Store) TransitionWithSetting(j *Job, key, value string, events ...*Event) error {
	if key == "" {
		return errors.New("运行状态键不能为空")
	}
	return s.transition(j, key, value, events...)
}

func (s *Store) transition(j *Job, key, value string, events ...*Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := saveJob(tx, j); err != nil {
		return err
	}
	if key != "" {
		if err := setSetting(tx, key, value); err != nil {
			return err
		}
	}
	finished := false
	for _, e := range events {
		if e == nil || e.Kind == "" {
			return fmt.Errorf("任务 #%d 的事件不合法", j.ID)
		}
		if e.Kind == EventQueueDrained {
			if !finished || e.JobID != nil {
				return fmt.Errorf("任务 #%d 的队列排空事件不合法", j.ID)
			}
			// 仍有待重试的收尾时，库里还有活动任务，等最后一次收尾成功再记排空。
			var active bool
			if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM jobs WHERE status IN ('queued','running'))`).Scan(&active); err != nil {
				return err
			}
			if active {
				continue
			}
		} else if e.JobID == nil || *e.JobID != j.ID {
			return fmt.Errorf("任务 #%d 的事件不合法", j.ID)
		}
		if e.Kind == EventJobFinished {
			// 启动中的任务也可能被取消。读事务中的值，避免占位时读出的开关过期。
			data := string(e.Data)
			if data == "" {
				data = "{}"
			}
			if err := tx.QueryRow(`SELECT json_set(?, '$.notify_done',
				json(CASE WHEN notify_done THEN 'true' ELSE 'false' END)) FROM jobs WHERE id=?`, data, j.ID).Scan(&data); err != nil {
				return err
			}
			e.Data = json.RawMessage(data)
			finished = true
		}
		if err := insertEvent(tx, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SetSettingWithEvent 写运行状态和系统事件；队列暂停状态与 queue_paused 事件不会分开提交。
func (s *Store) SetSettingWithEvent(key, value string, event *Event) error {
	if key == "" || event == nil || event.Kind == "" {
		return errors.New("运行状态或事件不合法")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := setSetting(tx, key, value); err != nil {
		return err
	}
	if err := insertEvent(tx, event); err != nil {
		return err
	}
	return tx.Commit()
}

func setSetting(tx *sql.Tx, key, value string) error {
	_, err := tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?,?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`, key, value, time.Now().Unix())
	return err
}

func insertEvent(tx *sql.Tx, e *Event) error {
	if e.TS == 0 {
		e.TS = time.Now().Unix()
	}
	if e.DedupeKey == "" {
		e.DedupeKey = rand.Text()
	}
	if len(e.Data) == 0 {
		e.Data = json.RawMessage(`{}`)
	}
	if !json.Valid(e.Data) {
		return fmt.Errorf("事件 %s 的数据不是 JSON", e.Kind)
	}
	var jobID any
	if e.JobID != nil {
		jobID = *e.JobID
	}
	_, err := tx.Exec(`INSERT INTO events (ts, kind, key, job_id, data, dedupe_key)
		VALUES (?,?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING`,
		e.TS, e.Kind, e.Key, jobID, string(e.Data), e.DedupeKey)
	if err != nil {
		return err
	}
	return tx.QueryRow(`SELECT id FROM events WHERE dedupe_key=?`, e.DedupeKey).Scan(&e.ID)
}

// AppendEvents 将一次采样里的告警变化一起提交；失败时由调度器重试同一组去重键。
func (s *Store) AppendEvents(events ...*Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, e := range events {
		if e == nil || e.Kind == "" {
			return errors.New("事件不合法")
		}
		if err := insertEvent(tx, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) LastEventID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT COALESCE(MAX(id),0) FROM events`).Scan(&id)
	return id, err
}

// LatestAlerts 包含已恢复的告警，下一次出现时以这条记录为前驱生成去重键。
func (s *Store) LatestAlerts() (map[string]Event, error) {
	rows, err := s.db.Query(`SELECT id, ts, kind, key, job_id, data, dedupe_key FROM events
		WHERE id IN (SELECT MAX(id) FROM events WHERE kind IN ('alert_raised','alert_cleared') GROUP BY key)`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	alerts := map[string]Event{}
	for rows.Next() {
		var e Event
		var jobID sql.NullInt64
		var data string
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.Key, &jobID, &data, &e.DedupeKey); err != nil {
			return nil, err
		}
		if jobID.Valid {
			e.JobID = &jobID.Int64
		}
		e.Data = json.RawMessage(data)
		alerts[e.Key] = e
	}
	return alerts, rows.Err()
}

// SetSettings 用于一起提交通知游标和待发批次，避免重启后重复消费事件。
func (s *Store) SetSettings(values map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for key, value := range values {
		if key == "" {
			return errors.New("运行状态键不能为空")
		}
		if err := setSetting(tx, key, value); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// EventsAfter 按游标顺序读取，供通知等消费者记住最后处理的事件 id。
func (s *Store) EventsAfter(after int64, limit int) ([]Event, error) {
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT id, ts, kind, key, job_id, data, dedupe_key
		FROM events WHERE id > ? ORDER BY id LIMIT ?`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []Event{}
	for rows.Next() {
		var e Event
		var jobID sql.NullInt64
		var data string
		if err := rows.Scan(&e.ID, &e.TS, &e.Kind, &e.Key, &jobID, &data, &e.DedupeKey); err != nil {
			return nil, err
		}
		if jobID.Valid {
			e.JobID = &jobID.Int64
		}
		e.Data = json.RawMessage(data)
		events = append(events, e)
	}
	return events, rows.Err()
}
