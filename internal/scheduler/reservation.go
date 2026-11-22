package scheduler

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"danlu/internal/gpu"
	"danlu/internal/store"
)

// GPU 预约：给丹炉以外的任务（SSH、网页终端里手动起的训练和调试）占卡。
//
// 预约一创建就开始收卡：此刻空闲的卡先收下，之后丹炉任务释放的卡先给预约、再给排队任务，
// 一张张收到凑够为止。凑齐前已收的卡也不分给任务。卡全部到位后才开始计时。
//
// 预约只由 Manager 写，内存里的列表是准的：调度器自己做的变化（收卡、就绪、到期）写库失败时
// 内存为准、下一轮重试；用户的操作（创建、释放、延长）先写库，成功了才改内存。

type ReservationReq struct {
	GPUs       int    `json:"gpus"`
	PinnedGPUs []int  `json:"pinned_gpus"`
	Duration   *int64 `json:"duration"`
	Note       string `json:"note"`
}

const (
	minReservationSeconds = 10 * 60
	maxReservationSeconds = 7 * 24 * 3600
	maxOpenReservations   = 16
	// reservationWarnBefore：自动释放前多久推送提醒；时长不超过它的预约不提醒
	reservationWarnBefore = 10 * 60
	reservationExtend     = 3600
	maxReservationNote    = 100
)

var ErrReservationEnded = errors.New("预约已经结束")

func reservationKey(id int64) string { return fmt.Sprintf("reservation:%d", id) }

func reservationEvent(r *store.Reservation, kind, dedupe string, now time.Time) *store.Event {
	data := store.ReservationEventData{ID: r.ID, GPUs: slices.Clone(r.GPUsHeld), Note: r.Note, Duration: r.Duration, EndsAt: r.EndsAt, Reason: r.EndReason}
	if data.GPUs == nil {
		data.GPUs = []int{}
	}
	b, _ := json.Marshal(data)
	return &store.Event{TS: now.Unix(), Kind: kind, Key: reservationKey(r.ID), Data: b, DedupeKey: dedupe}
}

// Reservations 返回没有结束的预约，按创建顺序。不取调度锁；返回的切片只读。
func (m *Manager) Reservations() []store.Reservation {
	if v := m.resView.Load(); v != nil {
		return *v
	}
	return []store.Reservation{}
}

func (m *Manager) publishReservationsLocked() {
	view := make([]store.Reservation, 0, len(m.res))
	for _, r := range m.res {
		if r.Status != store.ReservationEnded {
			view = append(view, *r.Clone())
		}
	}
	m.resView.Store(&view)
}

// loadReservationsLocked 第一次用到时从库里读一次，之后以内存为准。
func (m *Manager) loadReservationsLocked() error {
	if m.resReady {
		return nil
	}
	list, err := m.st.OpenReservations()
	if err != nil {
		if m.resError != err.Error() {
			log.Printf("读取 GPU 预约失败，暂停分配需要 GPU 的任务: %v", err)
		}
		m.resError = err.Error()
		return err
	}
	m.res, m.resReady, m.resError = list, true, ""
	m.publishReservationsLocked()
	return nil
}

// heldCardsLocked 是已经收下的卡 → 预约 id。
func (m *Manager) heldCardsLocked() map[int]int64 {
	held := map[int]int64{}
	for _, r := range m.res {
		if r.Status == store.ReservationEnded {
			continue
		}
		for _, g := range r.GPUsHeld {
			held[g] = r.ID
		}
	}
	return held
}

func (m *Manager) findReservation(id int64) *store.Reservation {
	for _, r := range m.res {
		if r.ID == id {
			return r
		}
	}
	return nil
}

// saveReservationLocked 保存调度器自己做的变化。失败时内存为准，事件留着下一轮一起重试。
func (m *Manager) saveReservationLocked(r *store.Reservation, events ...*store.Event) error {
	events = slices.Concat(m.resDirty[r.ID], events)
	if err := m.st.SaveReservation(r, events...); err != nil {
		log.Printf("保存预约 #%d 失败: %v", r.ID, err)
		m.resDirty[r.ID] = events
		return err
	}
	m.reservationSavedLocked(r)
	return nil
}

func (m *Manager) reservationSavedLocked(r *store.Reservation) {
	delete(m.resDirty, r.ID)
	if r.Status == store.ReservationEnded {
		m.res = slices.DeleteFunc(m.res, func(x *store.Reservation) bool { return x.ID == r.ID })
		delete(m.resWarned, r.ID)
	}
}

func (m *Manager) retryReservationsLocked() {
	for _, r := range slices.Clone(m.res) {
		if _, dirty := m.resDirty[r.ID]; dirty {
			m.saveReservationLocked(r)
		}
	}
}

// reservationsLocked 每轮调度前调用：先处理到期和提醒（不需要 GPU 数据），再收卡（需要可信的 GPU 数据）。
// gpus 和 schedule 用的是同一份采样，排队任务不会抢在预约前面拿到刚空出来的卡。
func (m *Manager) reservationsLocked(now time.Time, gpus []gpu.GPU, gpuErr error) {
	if err := m.loadReservationsLocked(); err != nil {
		return
	}
	defer m.publishReservationsLocked()
	ts := now.Unix()
	for _, r := range slices.Clone(m.res) {
		if r.Status != store.ReservationActive || r.EndsAt == nil {
			continue
		}
		if ts >= *r.EndsAt {
			r.Status, r.EndedAt, r.EndReason = store.ReservationEnded, &ts, "到期自动释放"
			log.Printf("预约 #%d 到期，释放 GPU %v", r.ID, r.GPUsHeld)
			m.saveReservationLocked(r, reservationEvent(r, store.EventReservationEnded, fmt.Sprintf("reservation_ended:%d", r.ID), now))
			continue
		}
		long := r.ReadyAt != nil && *r.EndsAt-*r.ReadyAt > reservationWarnBefore
		if long && ts >= *r.EndsAt-reservationWarnBefore && m.resWarned[r.ID] != *r.EndsAt {
			// 去重键带上到期时间：延长以后会再提醒一次；重启后重复写同一个键也不会多推
			m.resWarned[r.ID] = *r.EndsAt
			m.saveReservationLocked(r, reservationEvent(r, store.EventReservationExpiring,
				fmt.Sprintf("reservation_expiring:%d:%d", r.ID, *r.EndsAt), now))
		}
	}
	if gpuErr != nil || len(gpus) == 0 {
		return
	}
	waiting := slices.ContainsFunc(m.res, func(r *store.Reservation) bool { return r.Status == store.ReservationWaiting })
	if !waiting {
		return
	}
	running, err := m.jobsByStatus(store.StatusRunning)
	if err != nil {
		return
	}
	taken := map[int]bool{}
	for _, j := range running {
		for _, g := range j.GPUsAssigned {
			taken[g] = true
		}
	}
	for g := range m.heldCardsLocked() {
		taken[g] = true
	}
	exists := map[int]bool{}
	var idle []int
	for _, g := range gpus {
		exists[g.Index] = true
		if len(g.Procs) == 0 && g.UnknownMemMB == 0 {
			idle = append(idle, g.Index)
		}
	}
	slices.Sort(idle)
	// 按张数收卡时，先挑排队任务没有指定的卡，免得白白挡住指定了卡号的任务
	wanted := map[int]bool{}
	if slices.ContainsFunc(m.res, func(r *store.Reservation) bool {
		return r.Status == store.ReservationWaiting && len(r.GPUsPinned) == 0
	}) {
		if queued, err := m.jobsByStatus(store.StatusQueued); err == nil {
			for _, j := range queued {
				for _, g := range j.GPUsPinned {
					wanted[g] = true
				}
			}
		}
	}
	for _, r := range slices.Clone(m.res) {
		if r.Status != store.ReservationWaiting {
			continue
		}
		before := len(r.GPUsHeld)
		take := func(g int) {
			r.GPUsHeld = append(r.GPUsHeld, g)
			taken[g] = true
		}
		if len(r.GPUsPinned) > 0 {
			// 指定的卡只要丹炉没在用就收下，卡上的外部进程可能就是用户自己的任务
			for _, g := range r.GPUsPinned {
				if exists[g] && !taken[g] && !slices.Contains(r.GPUsHeld, g) {
					take(g)
				}
			}
		} else {
			for _, pass := range []bool{false, true} {
				for _, g := range idle {
					if len(r.GPUsHeld) >= r.Want() {
						break
					}
					if !taken[g] && wanted[g] == pass {
						take(g)
					}
				}
			}
		}
		if len(r.GPUsHeld) == before {
			continue
		}
		slices.Sort(r.GPUsHeld)
		var events []*store.Event
		if len(r.GPUsHeld) >= r.Want() {
			r.Status, r.ReadyAt = store.ReservationActive, &ts
			if r.Duration != nil {
				ends := ts + *r.Duration
				r.EndsAt = &ends
			}
			log.Printf("预约 #%d 就绪：GPU %v", r.ID, r.GPUsHeld)
			events = append(events, reservationEvent(r, store.EventReservationReady, fmt.Sprintf("reservation_ready:%d", r.ID), now))
		}
		m.saveReservationLocked(r, events...)
	}
}

func normalizeNote(note string) string { return strings.Join(strings.Fields(note), " ") }

// CreateReservation 新建预约并立刻收一次卡。
func (m *Manager) CreateReservation(req ReservationReq) (*store.Reservation, error) {
	note := normalizeNote(req.Note)
	if len([]rune(note)) > maxReservationNote {
		return nil, invalid("reservation_note_too_long", map[string]any{"max": maxReservationNote}, "用途最多 %d 字", maxReservationNote)
	}
	pinned := slices.Clone(req.PinnedGPUs)
	slices.Sort(pinned)
	pinned = slices.Compact(pinned)
	n := req.GPUs
	if len(pinned) > 0 {
		n = len(pinned)
	}
	if n < 1 || n > 64 {
		return nil, invalid("reservation_gpus_invalid", nil, "要预约的卡数不合法")
	}
	if len(pinned) > 0 && pinned[0] < 0 {
		return nil, invalid("gpu_missing", map[string]any{"gpu": pinned[0]}, "GPU %d 不存在", pinned[0])
	}
	if gpus, _, err := m.gpus.Latest(); err == nil && len(gpus) > 0 {
		if n > len(gpus) {
			return nil, invalid("reservation_gpus_exceed", map[string]any{"n": n, "total": len(gpus)}, "要预约 %d 张卡，但这台机器只有 %d 张", n, len(gpus))
		}
		for _, p := range pinned {
			if !slices.ContainsFunc(gpus, func(g gpu.GPU) bool { return g.Index == p }) {
				return nil, invalid("gpu_missing", map[string]any{"gpu": p}, "GPU %d 不存在", p)
			}
		}
	}
	if d := req.Duration; d != nil && (*d < minReservationSeconds || *d > maxReservationSeconds) {
		return nil, invalid("reservation_duration_range", nil, "自动释放时间要在 10 分钟到 7 天之间")
	}
	if pinned == nil {
		pinned = []int{}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.loadReservationsLocked(); err != nil {
		return nil, err
	}
	open := 0
	for _, r := range m.res {
		if r.Status != store.ReservationEnded {
			open++
		}
	}
	if open >= maxOpenReservations {
		return nil, invalid("reservation_too_many_open", map[string]any{"max": maxOpenReservations}, "未结束的预约最多 %d 个", maxOpenReservations)
	}
	now := time.Now()
	r := &store.Reservation{Note: note, GPUsReq: n, GPUsPinned: pinned, GPUsHeld: []int{}, Duration: req.Duration,
		Status: store.ReservationWaiting, CreatedAt: now.Unix()}
	if err := m.st.InsertReservation(r); err != nil {
		return nil, err
	}
	m.res = append(m.res, r)
	gpus, _, gpuErr := m.gpus.Latest()
	m.reservationsLocked(now, gpus, gpuErr)
	m.Wake()
	return r.Clone(), nil
}

// changeReservationLocked 用于用户的操作：先把改动连同未保存的事件写进库，成功了才改内存。
func (m *Manager) changeReservationLocked(id int64, change func(c *store.Reservation) (*store.Event, error)) (*store.Reservation, error) {
	if err := m.loadReservationsLocked(); err != nil {
		return nil, err
	}
	r := m.findReservation(id)
	if r == nil || r.Status == store.ReservationEnded {
		if _, err := m.st.GetReservation(id); errors.Is(err, store.ErrNotFound) {
			return nil, store.ErrNotFound
		} else if err != nil {
			return nil, err
		}
		return nil, ErrReservationEnded
	}
	c := r.Clone()
	event, err := change(c)
	if err != nil {
		return nil, err
	}
	events := slices.Clone(m.resDirty[id])
	if event != nil {
		events = append(events, event)
	}
	if err := m.st.SaveReservation(c, events...); err != nil {
		return nil, err
	}
	*r = *c
	m.reservationSavedLocked(r)
	m.publishReservationsLocked()
	m.Wake()
	return c.Clone(), nil
}

// ReleaseReservation 手动释放预约。卡上如果还有用户自己的进程，这张卡仍按外部进程的规则不分配。
func (m *Manager) ReleaseReservation(id int64) (*store.Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, err := m.changeReservationLocked(id, func(c *store.Reservation) (*store.Event, error) {
		now := time.Now()
		ts := now.Unix()
		c.Status, c.EndedAt, c.EndReason = store.ReservationEnded, &ts, "手动释放"
		return reservationEvent(c, store.EventReservationEnded, fmt.Sprintf("reservation_ended:%d", c.ID), now), nil
	})
	if err == nil {
		log.Printf("预约 #%d 手动释放：GPU %v", r.ID, r.GPUsHeld)
	}
	return r, err
}

// ExtendReservation 给已就绪、会自动释放的预约延长 1 小时。
func (m *Manager) ExtendReservation(id int64) (*store.Reservation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.changeReservationLocked(id, func(c *store.Reservation) (*store.Event, error) {
		if c.Status != store.ReservationActive || c.EndsAt == nil {
			return nil, invalid("reservation_extend_not_ready", nil, "只有卡已全部到位、会自动释放的预约才能延长")
		}
		now := time.Now().Unix()
		ends := max(*c.EndsAt, now) + reservationExtend
		if ends-now > maxReservationSeconds {
			return nil, invalid("reservation_extend_max", nil, "最多保留到 7 天后")
		}
		*c.Duration += ends - *c.EndsAt
		c.EndsAt = &ends
		return nil, nil
	})
}
