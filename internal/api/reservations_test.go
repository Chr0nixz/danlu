package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"danlu/internal/gpu"
	"danlu/internal/scheduler"
	"danlu/internal/store"
)

// stubExec 只实现建 Manager 时用到的 BootID；预约不会碰执行层，调用别的方法会直接崩
type stubExec struct{ scheduler.Executor }

func (stubExec) BootID() string { return "boot" }

func newReservationServer(t *testing.T) (*Server, http.Handler) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mon := gpu.NewMonitor(emptyProcsSource{})
	mon.Refresh(context.Background())
	s := &Server{GPUs: mon, Store: st, Mgr: scheduler.New(st, mon, stubExec{}, scheduler.Options{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/gpus", s.gpus)
	mux.HandleFunc("GET /api/reservations", s.listReservations)
	mux.HandleFunc("POST /api/reservations", s.createReservation)
	mux.HandleFunc("POST /api/reservations/{id}/release", s.changeReservation("reservation_release", s.Mgr.ReleaseReservation))
	mux.HandleFunc("POST /api/reservations/{id}/extend", s.changeReservation("reservation_extend", s.Mgr.ExtendReservation))
	return s, mux
}

func call(t *testing.T, h http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestReservationAPI(t *testing.T) {
	s, h := newReservationServer(t)
	for body, want := range map[string]int{
		`{"gpus": 3}`:                   400,
		`{"pinned_gpus": [5]}`:          400,
		`{"gpus": 1, "duration": 60}`:   400,
		`{"gpus": "x"}`:                 400,
		`{"gpus": 1, "duration": 3600}`: 200,
	} {
		if w := call(t, h, "POST", "/api/reservations", body); w.Code != want {
			t.Fatalf("%s → %d %s，期望 %d", body, w.Code, w.Body.String(), want)
		}
	}
	if e := lastAudit(t, s.Store); e.Action != "reservation_create" || !strings.Contains(e.Detail, "#1 1 张") {
		t.Fatalf("操作记录: %+v", e)
	}

	w := call(t, h, "POST", "/api/reservations", `{"pinned_gpus": [1], "note": "调试"}`)
	var res store.Reservation
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Status != store.ReservationActive || len(res.GPUsHeld) != 1 {
		t.Fatalf("创建: %s", w.Body.String())
	}

	w = call(t, h, "GET", "/api/gpus", "")
	var gpus struct {
		GPUs []struct {
			Index       int     `json:"index"`
			Reservation *resRef `json:"reservation"`
		} `json:"gpus"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &gpus); err != nil {
		t.Fatal(err)
	}
	if r0, r1 := gpus.GPUs[0].Reservation, gpus.GPUs[1].Reservation; r0 == nil || r0.ID != 1 || r0.EndsAt == nil || r1 == nil || r1.Note != "调试" {
		t.Fatalf("GPU 上的预约: %s", w.Body.String())
	}

	if w := call(t, h, "POST", "/api/reservations/1/extend", `{}`); w.Code != 200 {
		t.Fatalf("延长: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, h, "POST", "/api/reservations/2/extend", `{}`); w.Code != 400 {
		t.Fatalf("不定时的预约不能延长: %d", w.Code)
	}
	if w := call(t, h, "POST", "/api/reservations/2/release", `{}`); w.Code != 200 {
		t.Fatalf("释放: %d %s", w.Code, w.Body.String())
	}
	if e := lastAudit(t, s.Store); e.Action != "reservation_release" || !strings.Contains(e.Detail, "GPU 1 调试") {
		t.Fatalf("操作记录: %+v", e)
	}
	for path, want := range map[string]int{
		"/api/reservations/2/release": 409,
		"/api/reservations/9/release": 404,
		"/api/reservations/x/release": 400,
	} {
		if w := call(t, h, "POST", path, `{}`); w.Code != want {
			t.Fatalf("%s → %d，期望 %d", path, w.Code, want)
		}
	}

	w = call(t, h, "GET", "/api/reservations", "")
	var list struct {
		Reservations []store.Reservation `json:"reservations"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Reservations) != 1 || list.Reservations[0].ID != 1 {
		t.Fatalf("列表: %s", w.Body.String())
	}
}

func TestGpusWithoutManagerHasNullReservation(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	mon := gpu.NewMonitor(emptyProcsSource{})
	mon.Refresh(context.Background())
	rec := httptest.NewRecorder()
	(&Server{GPUs: mon, Store: st}).gpus(rec, httptest.NewRequest(http.MethodGet, "/api/gpus", nil))
	if !strings.Contains(rec.Body.String(), `"reservation":null`) {
		t.Fatalf("没有预约时应为 null: %s", rec.Body.String())
	}
}
