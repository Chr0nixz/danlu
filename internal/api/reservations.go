package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"danlu/internal/auth"
	"danlu/internal/scheduler"
	"danlu/internal/store"
)

// ---------- GPU 预约 ----------

// resRef 是 /api/gpus 里每张卡带的预约摘要：只有这张卡已经被预约收下时才有
type resRef struct {
	ID     int64  `json:"id"`
	Note   string `json:"note"`
	Status string `json:"status"`
	EndsAt *int64 `json:"ends_at"`
}

// heldBy 是卡号 → 收下这张卡的预约。没有调度器（单测）时为空。
func (s *Server) heldBy() map[int]*resRef {
	held := map[int]*resRef{}
	if s.Mgr == nil {
		return held
	}
	for _, r := range s.Mgr.Reservations() {
		ref := &resRef{ID: r.ID, Note: r.Note, Status: r.Status, EndsAt: r.EndsAt}
		for _, g := range r.GPUsHeld {
			held[g] = ref
		}
	}
	return held
}

func (s *Server) listReservations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"reservations": s.Mgr.Reservations()})
}

func reservationDetail(r *store.Reservation) string {
	cards := fmt.Sprintf("%d 张", r.GPUsReq)
	if len(r.GPUsPinned) > 0 {
		cards = fmt.Sprintf("GPU %s", store.JoinInts(r.GPUsPinned))
	}
	detail := fmt.Sprintf("#%d %s", r.ID, cards)
	if r.Note != "" {
		detail += " " + r.Note
	}
	return detail
}

func (s *Server) createReservation(w http.ResponseWriter, r *http.Request) {
	var req scheduler.ReservationReq
	if !readJSON(w, r, &req) {
		return
	}
	res, err := s.Mgr.CreateReservation(req)
	if scheduler.IsValidation(err) {
		writeErrE(w, 400, err)
		return
	}
	if err != nil {
		writeErr(w, 500, "创建预约失败: "+err.Error())
		return
	}
	s.Store.Audit(auth.ClientIP(r), "reservation_create", reservationDetail(res))
	writeJSON(w, 200, res)
}

func (s *Server) changeReservation(action string, change func(int64) (*store.Reservation, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
		if err != nil {
			writeErrC(w, 400, "reservation_id_invalid", "预约 id 无效", nil)
			return
		}
		var body struct{}
		if !readJSON(w, r, &body) {
			return
		}
		res, err := change(id)
		switch {
		case errors.Is(err, store.ErrNotFound):
			writeErrC(w, 404, "reservation_not_found", "预约不存在", nil)
		case errors.Is(err, scheduler.ErrReservationEnded):
			writeErrE(w, 409, err)
		case scheduler.IsValidation(err):
			writeErrE(w, 400, err)
		case err != nil:
			writeErr(w, 500, err.Error())
		default:
			s.Store.Audit(auth.ClientIP(r), action, reservationDetail(res))
			writeJSON(w, 200, res)
		}
	}
}
