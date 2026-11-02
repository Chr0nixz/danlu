package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"danlu/internal/auth"
	"danlu/internal/notify"
)

func (s *Server) notifyTest(w http.ResponseWriter, r *http.Request) {
	if s.Notify == nil {
		writeErr(w, 409, notify.ErrDisabled.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	err := s.Notify.Test(ctx)
	if errors.Is(err, notify.ErrDisabled) || errors.Is(err, notify.ErrBusy) {
		writeErr(w, 409, err.Error())
		return
	}
	if err != nil {
		writeErr(w, 502, err.Error())
		return
	}
	s.Store.Audit(auth.ClientIP(r), "notify_test", "测试手机推送")
	writeJSON(w, 200, map[string]bool{"ok": true})
}
