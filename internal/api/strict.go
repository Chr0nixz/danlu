package api

import (
	"net/http"

	"danlu/internal/auth"
	"danlu/internal/strict"
)

// strictView 是设置页「严格限卡」一节要的全部信息。installed 只看助手文件在不在（不调 sudo），
// ok/reason 是上次检查的结果；从没查过时 reason 是 checking。
type strictView struct {
	Enabled       bool   `json:"enabled"`
	Installed     bool   `json:"installed"`
	OK            bool   `json:"ok"`
	Reason        string `json:"reason,omitempty"`
	Detail        string `json:"detail,omitempty"`
	HelperVersion string `json:"helper_version,omitempty"`
	CheckedAt     int64  `json:"checked_at,omitempty"`
	InstallCmd    string `json:"install_cmd"`
}

func (s *Server) strictStatus() strictView {
	v := strictView{Installed: strict.Installed(), Reason: strict.ReasonChecking}
	if s.Settings != nil {
		v.Enabled = s.Settings.Current().Strict
	}
	if s.Strict != nil {
		st := s.Strict.Status()
		v.OK, v.Reason, v.Detail, v.HelperVersion, v.CheckedAt = st.OK, st.Reason, st.Detail, st.HelperVersion, st.CheckedAt
	}
	exe := "danlu"
	if s.Runner != nil && s.Runner.Exe != "" {
		exe = s.Runner.Exe
	}
	v.InstallCmd = "sudo " + exe + " strict install"
	return v
}

// updateStrict 开关严格限卡。打开前同步查一次助手（经 sudo，最多 20 秒），查不过就拒绝：
// 开着但用不了会让整个队列停住。只影响之后启动的任务。
func (s *Server) updateStrict(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeErrC(w, 400, "strict_enabled_missing", "缺少 enabled", nil)
		return
	}
	if s.Settings == nil || s.Strict == nil {
		writeErrC(w, 409, "strict_unavailable", "严格限卡的 root 助手不可用：这个 danlu 没有配置严格限卡",
			map[string]any{"reason": strict.ReasonFailed, "detail": "这个 danlu 没有配置严格限卡"})
		return
	}
	if *body.Enabled {
		st := s.Strict.Recheck(r.Context())
		if !st.OK {
			writeErrC(w, 409, "strict_unavailable", "严格限卡的 root 助手不可用："+st.Detail,
				map[string]any{"reason": st.Reason, "detail": st.Detail})
			return
		}
	}
	if err := s.Settings.SetStrict(*body.Enabled); err != nil {
		writeErr(w, 500, "保存严格限卡开关失败: "+err.Error())
		return
	}
	action, note := "strict_disable", "关闭严格限卡"
	if *body.Enabled {
		action, note = "strict_enable", "开启严格限卡"
	}
	s.Store.Audit(auth.ClientIP(r), action, note)
	if s.Mgr != nil {
		s.Mgr.Wake()
	}
	writeJSON(w, 200, s.strictStatus())
}
