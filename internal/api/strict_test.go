package api

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"danlu/internal/strict"
)

func TestUpdateStrict(t *testing.T) {
	s := newSettingsServer(t)
	var ok atomic.Bool
	var checks atomic.Int32
	s.Strict = strict.NewMonitor(func(context.Context) strict.Status {
		checks.Add(1)
		if ok.Load() {
			return strict.Status{OK: true, HelperVersion: "v-test", CheckedAt: 1}
		}
		return strict.Status{Reason: strict.ReasonNoSudo, Detail: "sudo: a password is required", CheckedAt: 1}
	}, func() bool { return s.Settings.Current().Strict }, nil)

	// 助手查不过：拒绝打开，开关保持关闭
	w := postJSON(t, s.updateStrict, "/api/settings/strict", `{"enabled": true}`)
	var body struct {
		Code   string         `json:"code"`
		Params map[string]any `json:"params"`
	}
	if w.Code != 409 || json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Code != "strict_unavailable" || body.Params["reason"] != "no_sudo" {
		t.Fatalf("助手不可用时应拒绝打开: %d %s", w.Code, w.Body)
	}
	if s.Settings.Current().Strict || checks.Load() != 1 {
		t.Fatalf("拒绝后开关应保持关闭，且只查一次: %v %d", s.Settings.Current().Strict, checks.Load())
	}

	ok.Store(true)
	w = postJSON(t, s.updateStrict, "/api/settings/strict", `{"enabled": true}`)
	var view strictView
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &view) != nil || !view.Enabled || !view.OK || view.HelperVersion != "v-test" ||
		!strings.HasSuffix(view.InstallCmd, " strict install") {
		t.Fatalf("打开失败: %d %s", w.Code, w.Body)
	}
	if a := lastAudit(t, s.Store); a.Action != "strict_enable" {
		t.Fatalf("操作记录: %+v", a)
	}

	// 关闭不用查助手
	n := checks.Load()
	w = postJSON(t, s.updateStrict, "/api/settings/strict", `{"enabled": false}`)
	if w.Code != 200 || s.Settings.Current().Strict || checks.Load() != n {
		t.Fatalf("关闭失败或多查了一次: %d %s", w.Code, w.Body)
	}
	if a := lastAudit(t, s.Store); a.Action != "strict_disable" {
		t.Fatalf("操作记录: %+v", a)
	}
	if w := postJSON(t, s.updateStrict, "/api/settings/strict", `{}`); w.Code != 400 {
		t.Fatalf("缺 enabled 应 400: %d", w.Code)
	}
}
