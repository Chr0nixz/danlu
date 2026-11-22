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
		writeErrE(w, 409, notify.ErrDisabled)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	err := s.Notify.Test(ctx)
	if errors.Is(err, notify.ErrDisabled) || errors.Is(err, notify.ErrBusy) {
		writeErrE(w, 409, err)
		return
	}
	if err != nil {
		writeErrC(w, 502, "notify_send_failed", err.Error(), map[string]any{"detail": err.Error()})
		return
	}
	s.Store.Audit(auth.ClientIP(r), "notify_test", "测试手机推送")
	writeJSON(w, 200, map[string]bool{"ok": true})
}
