package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"danlu/internal/auth"
	"danlu/internal/config"
	"danlu/internal/retention"
	"danlu/internal/settings"
)

// effectiveConfig 是盖上了面板设置的一份配置副本，给体检用。s.Cfg 由多个请求同时读，不能改它。
func (s *Server) effectiveConfig() *config.Config {
	if s.Settings == nil {
		return s.Cfg
	}
	c := *s.Cfg
	s.Settings.Current().ApplyTo(&c)
	return &c
}

// panelSettings 把运行设置、上次自动清理和面板偏好加进 GET /api/settings 的响应。
func (s *Server) panelSettings(resp map[string]any) error {
	if s.Settings != nil {
		resp["runtime"] = s.Settings.View(settings.GroupRuntime)
		resp["retention"] = s.Settings.View(settings.GroupRetention)
	}
	if s.Pruner != nil {
		if st, ok := s.Pruner.Last(); ok {
			resp["prune"] = st
		}
	}
	prefs, err := settings.LoadPrefs(s.Store)
	if err != nil {
		return err
	}
	resp["prefs"] = prefs
	return nil
}

// updateRuntime 保存「调度与告警」或「数据保留」里改了的项：请求体是 {"名字": 数值或 null}，null 恢复成 config.toml 的值。
func (s *Server) updateRuntime(g settings.Group) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.Settings == nil {
			writeErrC(w, 404, "not_found", "接口不存在", nil)
			return
		}
		var body settings.Patch
		if !readJSON(w, r, &body) {
			return
		}
		_, changes, err := s.Settings.Update(g, body)
		if settings.IsInputError(err) {
			writeErrE(w, 400, err)
			return
		}
		if err != nil {
			writeErr(w, 500, "保存设置失败: "+err.Error())
			return
		}
		if len(changes) > 0 {
			action := "settings_change"
			if g == settings.GroupRetention {
				action = "retention_change"
			}
			s.Store.Audit(auth.ClientIP(r), action, strings.Join(changes, "；"))
			// 放锁之后再叫醒：调度器下一轮立即用上新值，清理器按新策略马上清理一次
			if s.Mgr != nil {
				s.Mgr.Wake()
			}
			if g == settings.GroupRetention && s.Pruner != nil {
				s.Pruner.Kick()
			}
		}
		writeJSON(w, 200, map[string]any{string(g): s.Settings.View(g)})
	}
}

// previewTimeout 限制保存前的预览：要走一遍数据目录，存储很慢时不能拖到 HTTP 写超时。
const previewTimeout = 20 * time.Second

// previewRetention 按请求里的保留策略（和保存时同样的格式与校验）算一遍会清理什么，不删任何东西。
func (s *Server) previewRetention(w http.ResponseWriter, r *http.Request) {
	if s.Settings == nil || s.Pruner == nil {
		writeErrC(w, 404, "not_found", "接口不存在", nil)
		return
	}
	var body settings.Patch
	if !readJSON(w, r, &body) {
		return
	}
	v, err := s.Settings.Preview(settings.GroupRetention, body)
	if settings.IsInputError(err) {
		writeErrE(w, 400, err)
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), previewTimeout)
	defer cancel()
	rep, err := s.Pruner.Preview(ctx, v.KeepDays, v.MaxTotalGB<<30)
	switch {
	case errors.Is(err, retention.ErrBusy):
		writeErrC(w, 409, "retention_busy", "正在自动清理，请稍后再预览", nil)
		return
	case errors.Is(err, context.DeadlineExceeded):
		writeErrC(w, 504, "retention_preview_timeout", "统计任务文件超时，存储可能很慢，请稍后再试", nil)
		return
	case err != nil && len(rep.Errors) == 0:
		writeErr(w, 500, "预览失败: "+err.Error())
		return
	}
	// 个别任务出错时照样给出其余的结果，错误列在 errors 里
	items := rep.Jobs
	if len(items) > 50 {
		items = items[:50]
	}
	writeJSON(w, 200, map[string]any{
		"jobs": len(rep.Jobs), "envs": len(rep.Envs), "bytes": rep.Bytes, "total_bytes": rep.TotalBytes,
		"items": items, "errors": rep.Errors,
	})
}

// updatePrefs 保存面板偏好里改了的项：请求体是 {"名字": 值或 null}，null 恢复默认。
func (s *Server) updatePrefs(w http.ResponseWriter, r *http.Request) {
	var body map[string]json.RawMessage
	if !readJSON(w, r, &body) {
		return
	}
	prefs, changes, err := settings.UpdatePrefs(s.Store, body)
	if settings.IsInputError(err) {
		writeErrE(w, 400, err)
		return
	}
	if err != nil {
		writeErr(w, 500, "保存偏好失败: "+err.Error())
		return
	}
	if len(changes) > 0 {
		s.Store.Audit(auth.ClientIP(r), "prefs_change", strings.Join(changes, "；"))
	}
	writeJSON(w, 200, map[string]any{"prefs": prefs})
}
