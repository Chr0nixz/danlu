package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"danlu/internal/auth"
	"danlu/internal/settings"
	"danlu/internal/store"
)

// security 是当前生效的安全设置；没有 Auth 时（单测）用配置文件里的值。
func (s *Server) security() auth.Security {
	if s.Auth == nil {
		return auth.SecurityDefaults(s.Cfg)
	}
	return s.Auth.Security()
}

// entranceURL 是带安全入口的完整地址。入口可能在面板上改过，所以不用 Cfg.URL()。
func (s *Server) entranceURL(sec auth.Security) string {
	return s.Cfg.BaseURL() + "/" + sec.Entrance
}

// securityView 是设置页「安全设置」的数据，时长都以秒为单位。
func (s *Server) securityView(sec auth.Security) map[string]any {
	return map[string]any{
		"entrance":     sec.Entrance,
		"base_url":     s.Cfg.BaseURL(),
		"url":          s.entranceURL(sec),
		"session_ttl":  int64(sec.SessionTTL / time.Second),
		"max_failures": sec.MaxFailures,
		"lockout":      int64(sec.Lockout / time.Second),
	}
}

func (s *Server) updateSecurity(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Entrance    *string `json:"entrance"`
		SessionTTL  *int64  `json:"session_ttl"`
		MaxFailures *int    `json:"max_failures"`
		Lockout     *int64  `json:"lockout"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	// 没传的项保持原样
	var p auth.SecurityPatch
	if body.Entrance != nil {
		e := strings.TrimSpace(*body.Entrance)
		p.Entrance = &e
	}
	p.SessionTTL, p.Lockout = secondsPtr(body.SessionTTL), secondsPtr(body.Lockout)
	p.MaxFailures = body.MaxFailures
	sec, changes, err := s.Auth.UpdateSecurity(w, r, p)
	if settings.IsInputError(err) {
		writeErrE(w, 400, err)
		return
	}
	if err != nil {
		writeErr(w, 500, "保存安全设置失败: "+err.Error())
		return
	}
	if len(changes) > 0 {
		s.Store.Audit(auth.ClientIP(r), "security_change", strings.Join(changes, "；"))
	}
	writeJSON(w, 200, map[string]any{"security": s.securityView(sec)})
}

func secondsPtr(v *int64) *time.Duration {
	if v == nil {
		return nil
	}
	// 防溢出：超过一百年的值按一百年算，反正会被范围检查拒绝
	d := time.Duration(min(max(*v, -1), 100*365*24*3600)) * time.Second
	return &d
}

// ---------- 登录会话 ----------

func (s *Server) listSessions(w http.ResponseWriter, r *http.Request) {
	list, err := s.Store.ListSessions(s.Auth.CurrentSessionHash(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"sessions": list})
}

func (s *Server) revokeSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if cur := s.Auth.CurrentSessionHash(r); cur != "" && store.SessionID(cur) == id {
		writeErrC(w, 400, "session_current", "这是当前设备的会话，请用「退出登录」", nil)
		return
	}
	err := s.Store.DeleteSessionByID(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErrC(w, 404, "session_not_found", "会话不存在或已经过期", nil)
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.Store.Audit(auth.ClientIP(r), "session_revoke", "注销 1 个登录会话")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) revokeOtherSessions(w http.ResponseWriter, r *http.Request) {
	n, err := s.Store.DeleteSessionsExceptCount(s.Auth.CurrentSessionHash(r))
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if n > 0 {
		s.Store.Audit(auth.ClientIP(r), "session_revoke", fmt.Sprintf("注销其他 %d 个登录会话", n))
	}
	writeJSON(w, 200, map[string]any{"revoked": n})
}
