package store

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

// GPU 预约的状态：waiting 还在收卡（可能已经收了一部分），active 卡已全部到位，ended 已释放或到期。
const (
	ReservationWaiting = "waiting"
	ReservationActive  = "active"
	ReservationEnded   = "ended"
)

// 预约的事件不属于任何任务，JobID 为空，Key 为 reservation:<id>。
const (
	EventReservationReady    = "reservation_ready"
	EventReservationExpiring = "reservation_expiring"
	EventReservationEnded    = "reservation_ended"
)

// ReservationEventData 是预约事件的数据，通知按它写文案。只放事实，不放钟点：服务器时区可能和看的人不同。
type ReservationEventData struct {
	ID       int64  `json:"id"`
	GPUs     []int  `json:"gpus"`
	Note     string `json:"note,omitempty"`
	Duration *int64 `json:"duration,omitempty"`
	EndsAt   *int64 `json:"ends_at,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

// Reservation 是给丹炉以外的任务占的卡。只有调度器写它；面板读的是调度器发布的快照。
type Reservation struct {
	ID         int64  `json:"id"`
	Note       string `json:"note"`
	GPUsReq    int    `json:"gpus"`
	GPUsPinned []int  `json:"pinned_gpus"`
	GPUsHeld   []int  `json:"held_gpus"`
	// Duration 是卡全部到位后保留多少秒；为空表示一直保留到手动释放
	Duration  *int64 `json:"duration"`
	Status    string `json:"status"`
	CreatedAt int64  `json:"created_at"`
	ReadyAt   *int64 `json:"ready_at"`
	EndsAt    *int64 `json:"ends_at"`
	EndedAt   *int64 `json:"ended_at"`
	EndReason string `json:"end_reason"`
}

func clonePtr(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// Clone 深拷贝，快照给别的 goroutine 读时不会被调度器改到。
func (r *Reservation) Clone() *Reservation {
	c := *r
	c.GPUsPinned = slices.Clone(r.GPUsPinned)
	c.GPUsHeld = slices.Clone(r.GPUsHeld)
	if c.GPUsPinned == nil {
		c.GPUsPinned = []int{}
	}
	if c.GPUsHeld == nil {
		c.GPUsHeld = []int{}
	}
	c.Duration, c.ReadyAt, c.EndsAt, c.EndedAt = clonePtr(r.Duration), clonePtr(r.ReadyAt), clonePtr(r.EndsAt), clonePtr(r.EndedAt)
	return &c
}

// Want 是这个预约一共要几张卡。
func (r *Reservation) Want() int {
	if len(r.GPUsPinned) > 0 {
		return len(r.GPUsPinned)
	}
	return r.GPUsReq
}

func nullable(p *int64) any {
	if p == nil {
		return nil
	}
	return *p
}

func (s *Store) InsertReservation(r *Reservation) error {
	res, err := s.db.Exec(`INSERT INTO gpu_reservations
		(note, gpus_req, gpus_pinned, gpus_held, duration, status, created_at, ready_at, ends_at, ended_at, end_reason)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		truncate(r.Note, 300), r.GPUsReq, JoinInts(r.GPUsPinned), JoinInts(r.GPUsHeld), nullable(r.Duration), r.Status,
		r.CreatedAt, nullable(r.ReadyAt), nullable(r.EndsAt), nullable(r.EndedAt), r.EndReason)
	if err != nil {
		return err
	}
	r.ID, err = res.LastInsertId()
	return err
}

// SaveReservation 把预约和它的事件一起提交；重复的去重键不会再次写入。
func (s *Store) SaveReservation(r *Reservation, events ...*Event) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE gpu_reservations SET gpus_held=?, duration=?, status=?, ready_at=?, ends_at=?, ended_at=?, end_reason=?
		WHERE id=?`, JoinInts(r.GPUsHeld), nullable(r.Duration), r.Status, nullable(r.ReadyAt), nullable(r.EndsAt),
		nullable(r.EndedAt), r.EndReason, r.ID)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	for _, e := range events {
		if e == nil || e.Kind == "" || e.JobID != nil {
			return fmt.Errorf("预约 #%d 的事件不合法", r.ID)
		}
		if err := insertEvent(tx, e); err != nil {
			return err
		}
	}
	return tx.Commit()
}

const reservationCols = `id, note, gpus_req, gpus_pinned, gpus_held, duration, status, created_at, ready_at, ends_at, ended_at, end_reason`

func scanReservation(row interface{ Scan(...any) error }) (*Reservation, error) {
	var (
		r                              Reservation
		pinned, held                   string
		duration, ready, ends, endedAt sql.NullInt64
	)
	if err := row.Scan(&r.ID, &r.Note, &r.GPUsReq, &pinned, &held, &duration, &r.Status, &r.CreatedAt,
		&ready, &ends, &endedAt, &r.EndReason); err != nil {
		return nil, err
	}
	r.GPUsPinned, r.GPUsHeld = ParseInts(pinned), ParseInts(held)
	for _, f := range []struct {
		src sql.NullInt64
		dst **int64
	}{{duration, &r.Duration}, {ready, &r.ReadyAt}, {ends, &r.EndsAt}, {endedAt, &r.EndedAt}} {
		if f.src.Valid {
			v := f.src.Int64
			*f.dst = &v
		}
	}
	return &r, nil
}

// OpenReservations 返回没有结束的预约，按创建顺序（也就是收卡顺序）。
func (s *Store) OpenReservations() ([]*Reservation, error) {
	rows, err := s.db.Query(`SELECT `+reservationCols+` FROM gpu_reservations WHERE status<>? ORDER BY id`, ReservationEnded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*Reservation{}
	for rows.Next() {
		r, err := scanReservation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) GetReservation(id int64) (*Reservation, error) {
	r, err := scanReservation(s.db.QueryRow(`SELECT `+reservationCols+` FROM gpu_reservations WHERE id=?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return r, err
}
