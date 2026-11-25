package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// 排队任务的修改（FEATURES 3.1、3.2）：改提交字段、挪位置、暂缓。都只改 status='queued' 的行，
// 由调度器持锁调用；一批里任何一个已经不在排队就整批回滚。事件和修改在同一个事务里写入。

// ErrNotQueued：任务已经不在排队（被调度器占位、启动、取消或已经结束），这一批什么都没改。
var ErrNotQueued = errors.New("任务已经不在排队中")

// NotQueuedError 指出是哪个任务已经不在排队。errors.Is(err, ErrNotQueued) 成立。
type NotQueuedError struct{ ID int64 }

func (e *NotQueuedError) Error() string        { return fmt.Sprintf("任务 #%d 已经不在排队中", e.ID) }
func (e *NotQueuedError) Is(target error) bool { return target == ErrNotQueued }

// QueueOrder 是一个排队任务的新 queue_order。
type QueueOrder struct {
	ID    int64
	Order int64
}

// EditQueued 改排队任务的提交字段：名字、备注、结束提醒、工作目录、conda 环境、命令、环境变量、
// 是否继承 .bashrc、GPU 要求。rerun_of、code_from 和 submit_key 不改；SaveJob 不写这些字段，
// 调度器手里的旧副本落库时不会把它们改回去。
func (s *Store) EditQueued(jobs []*Job, events ...*Event) error {
	return s.updateQueued(events, func(tx *sql.Tx) error {
		for _, j := range jobs {
			env, err := json.Marshal(j.Env)
			if err != nil {
				return err
			}
			res, err := tx.Exec(`UPDATE jobs SET name=?, memo=?, notify_done=?, workdir=?, conda_env=?, cmd=?, env_json=?,
				inherit_bashrc=?, gpus_req=?, gpus_pinned=? WHERE id=? AND status='queued'`,
				j.Name, j.Memo, j.NotifyDone, j.Workdir, j.CondaEnv, j.Cmd, string(env),
				j.InheritBashrc, j.GPUsReq, JoinInts(j.GPUsPinned), j.ID)
			if err := queuedAffected(res, err, j.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetQueueOrder 改排队任务的 queue_order。
func (s *Store) SetQueueOrder(orders []QueueOrder, events ...*Event) error {
	return s.updateQueued(events, func(tx *sql.Tx) error {
		for _, o := range orders {
			res, err := tx.Exec(`UPDATE jobs SET queue_order=? WHERE id=? AND status='queued'`, o.Order, o.ID)
			if err := queuedAffected(res, err, o.ID); err != nil {
				return err
			}
		}
		return nil
	})
}

// SetHeld 暂缓或恢复排队任务。
func (s *Store) SetHeld(ids []int64, held bool, events ...*Event) error {
	return s.updateQueued(events, func(tx *sql.Tx) error {
		for _, id := range ids {
			res, err := tx.Exec(`UPDATE jobs SET held=? WHERE id=? AND status='queued'`, held, id)
			if err := queuedAffected(res, err, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) updateQueued(events []*Event, update func(*sql.Tx) error) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := update(tx); err != nil {
		return err
	}
	for _, e := range events {
		if e == nil || e.Kind == "" || e.JobID == nil {
			return errors.New("排队任务的事件不合法")
		}
		if err := insertEvent(tx, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func queuedAffected(res sql.Result, err error, id int64) error {
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return &NotQueuedError{ID: id}
	}
	return nil
}

// HasJobEvent 报告任务有没有某种事件，详情页据此标出"排队期间修改过"。
func (s *Store) HasJobEvent(id int64, kind string) (bool, error) {
	var ok bool
	err := s.db.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE job_id=? AND kind=?)`, id, kind).Scan(&ok)
	return ok, err
}
