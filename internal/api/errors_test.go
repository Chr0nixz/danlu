package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danlu/internal/auth"
	"danlu/internal/gpu"
	"danlu/internal/scheduler"
	"danlu/internal/settings"
	"danlu/internal/store"
)

// 错误响应约定：error 仍是中文原文（旧客户端回退用），code 是机器码，params 是插值参数（可缺省）。

type errBody struct {
	Error  string         `json:"error"`
	Code   string         `json:"code"`
	Params map[string]any `json:"params"`
}

func decodeErr(t *testing.T, w *httptest.ResponseRecorder) errBody {
	t.Helper()
	var b errBody
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("响应不是 JSON: %v %s", err, w.Body.String())
	}
	return b
}

// rawBody 按原始键读响应，用来断言某个字段不存在（而不是为空）。
func rawBody(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatalf("响应不是 JSON: %v %s", err, w.Body.String())
	}
	return m
}

func newJobServer(t *testing.T) *Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mon := gpu.NewMonitor(emptyProcsSource{})
	mon.Refresh(context.Background())
	return &Server{Store: st, Mgr: scheduler.New(st, mon, stubExec{}, scheduler.Options{})}
}

// 提交空命令：400 带 job_cmd_empty，中文原文一字不改
func TestSubmitEmptyCmdHasCode(t *testing.T) {
	s := newJobServer(t)
	body, _ := json.Marshal(map[string]any{"workdir": t.TempDir(), "cmd": ""})
	w := postJSON(t, s.submitJob, "/api/jobs", string(body))
	if w.Code != 400 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	b := decodeErr(t, w)
	if b.Code != "job_cmd_empty" || b.Error != "命令不能为空" {
		t.Fatalf("code = %q, error = %q", b.Code, b.Error)
	}
	if _, ok := rawBody(t, w)["params"]; ok {
		t.Fatal("无参数的错误不应输出 params 字段")
	}
}

// 要卡超过机器总数：码和插值参数一起下发
func TestSubmitGpusExceedHasParams(t *testing.T) {
	s := newJobServer(t)
	body, _ := json.Marshal(map[string]any{"workdir": t.TempDir(), "cmd": "x", "gpus": 3})
	w := postJSON(t, s.submitJob, "/api/jobs", string(body))
	if w.Code != 400 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	b := decodeErr(t, w)
	if b.Code != "job_gpus_exceed" || b.Error != "需要 3 张卡，但这台机器只有 2 张" {
		t.Fatalf("code = %q, error = %q", b.Code, b.Error)
	}
	if b.Params["n"] != float64(3) || b.Params["total"] != float64(2) {
		t.Fatalf("params = %v", b.Params)
	}
}

// 错误密码登录：401 带 login_bad_credentials
func TestLoginBadCredentialsHasCode(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := auth.SetPassword(st, "testpass123"); err != nil {
		t.Fatal(err)
	}
	au := auth.New(st, auth.Options{Entrance: "abcdef", Secret: []byte("test-secret"), Username: "admin", SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	s := &Server{Store: st, Auth: au}
	w := postJSON(t, s.login, "/api/login", `{"username":"admin","password":"wrong-pass"}`)
	if w.Code != 401 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	b := decodeErr(t, w)
	if b.Code != "login_bad_credentials" || b.Error != "用户名或密码错误" {
		t.Fatalf("code = %q, error = %q", b.Code, b.Error)
	}
}

// 未知偏好项：400 带 pref_unknown 和 name 参数
func TestUnknownPrefHasCode(t *testing.T) {
	s := newSettingsServer(t)
	w := postJSON(t, s.updatePrefs, "/api/settings/prefs", `{"nope": 1}`)
	if w.Code != 400 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	b := decodeErr(t, w)
	if b.Code != "pref_unknown" || b.Error != "未知偏好项 nope" || b.Params["name"] != "nope" {
		t.Fatalf("code = %q, error = %q, params = %v", b.Code, b.Error, b.Params)
	}
}

// 运行设置范围错误：每项一个码（range_<name>[_zero]），整句本地化，不再下发机器参数
func TestRuntimeRangeHasCode(t *testing.T) {
	s := newSettingsServer(t)
	h := s.updateRuntime(settings.GroupRuntime)
	w := postJSON(t, h, "/api/settings/runtime", `{"kill_grace": 0}`)
	if w.Code != 400 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	b := decodeErr(t, w)
	if b.Code != "range_kill_grace" || b.Error != "取消后强杀等待需要在 1 秒 到 10 分钟 之间" {
		t.Fatalf("code = %q, error = %q", b.Code, b.Error)
	}
	if _, ok := rawBody(t, w)["params"]; ok {
		t.Fatal("整句编码的错误不应输出 params 字段")
	}
	w = postJSON(t, h, "/api/settings/runtime", `{"fail_fast": 5}`)
	if w.Code != 400 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	b = decodeErr(t, w)
	if b.Code != "range_fail_fast_zero" || b.Error != "快速失败暂停需要在 10 秒 到 1 小时 之间，或设为 0 关闭" {
		t.Fatalf("code = %q, error = %q", b.Code, b.Error)
	}
}

// 偏好值不合要求：每项一个码（pref_invalid_<name>），原文带完整约束
func TestInvalidPrefHasCode(t *testing.T) {
	s := newSettingsServer(t)
	w := postJSON(t, s.updatePrefs, "/api/settings/prefs", `{"notify_lang": "fr"}`)
	if w.Code != 400 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	b := decodeErr(t, w)
	if b.Code != "pref_invalid_notify_lang" || b.Error != "推送语言只能是 [zh-CN en] 之一" {
		t.Fatalf("code = %q, error = %q", b.Code, b.Error)
	}
}

// 请求体不是 JSON：400 带 bad_json，detail 参数保留解析器的原话
func TestBadJSONHasCode(t *testing.T) {
	s := newJobServer(t)
	w := postJSON(t, s.submitJob, "/api/jobs", `{`)
	if w.Code != 400 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	b := decodeErr(t, w)
	if b.Code != "bad_json" || !strings.HasPrefix(b.Error, "请求格式错误: ") || b.Params["detail"] == "" {
		t.Fatalf("code = %q, error = %q, params = %v", b.Code, b.Error, b.Params)
	}
}

// 任务 id 解析失败与任务不存在：各自的码
func TestJobIDErrorsHaveCodes(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	s := &Server{Store: st}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	w := call(t, mux, "GET", "/api/jobs/abc", "")
	if b := decodeErr(t, w); w.Code != 400 || b.Code != "job_id_invalid" || b.Error != "任务 id 无效" {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
	w = call(t, mux, "GET", "/api/jobs/999", "")
	if b := decodeErr(t, w); w.Code != 404 || b.Code != "job_not_found" || b.Error != "任务不存在" {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
}

// 哨兵错误经 writeErrE 映射成码，原文不变
func TestWriteErrESentinel(t *testing.T) {
	w := httptest.NewRecorder()
	writeErrE(w, 409, scheduler.ErrReservationEnded)
	if b := decodeErr(t, w); w.Code != 409 || b.Code != "reservation_ended" || b.Error != "预约已经结束" {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body)
	}
}

// 不带码的错误（500 内部错误、writeErrE 认不出的错误）响应里没有 code 字段
func TestUncodedErrorHasNoCodeField(t *testing.T) {
	// 真实的 500 路径：库已关闭，读最近工作目录必然失败
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	s := &Server{Store: st}
	w := httptest.NewRecorder()
	s.recentWorkdirs(w, httptest.NewRequest(http.MethodGet, "/api/workdirs/recent", nil))
	if w.Code != 500 {
		t.Fatalf("status = %d %s", w.Code, w.Body)
	}
	m := rawBody(t, w)
	if _, ok := m["code"]; ok {
		t.Fatalf("500 响应不应带 code: %s", w.Body)
	}
	if m["error"] == "" {
		t.Fatalf("500 响应应保留中文 error: %s", w.Body)
	}

	// writeErrE 对没有码、也不在哨兵表里的错误退回纯文本
	w = httptest.NewRecorder()
	writeErrE(w, 500, errors.New("内部错误"))
	m = rawBody(t, w)
	if _, ok := m["code"]; ok {
		t.Fatalf("无码错误不应带 code: %s", w.Body)
	}
	if m["error"] != "内部错误" {
		t.Fatalf("error = %v", m["error"])
	}
}
