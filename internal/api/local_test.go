package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"danlu/internal/auth"
)

// 本机命令行入口（FEATURES 5.1）：socket 上同一个用户不需要入口和会话，别的 uid 一律 403。

func newLocalServer(t *testing.T) *Server {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上没有 euid")
	}
	s := newJobServer(t)
	s.Auth = auth.New(s.Store, auth.Options{Entrance: "abcdef", Secret: []byte("test-secret"), Username: "admin", SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	s.Static = fstest.MapFS{"index.html": {Data: []byte("<html>")}}
	return s
}

// localDo 模拟 socket 上的请求：对端 uid 放在连接的 context 里（serve 的 ConnContext 这样做）
func localDo(h http.Handler, uid int, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r = r.WithContext(auth.WithLocalPeer(r.Context(), uid))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLocalHandlerSkipsGateAndSession(t *testing.T) {
	s := newLocalServer(t)
	h := s.LocalHandler()
	if w := localDo(h, os.Geteuid(), "GET", "/api/jobs", ""); w.Code != 200 {
		t.Fatalf("同一个用户应直接可用: %d %s", w.Code, w.Body)
	}
	// 同样的请求走 TCP（没有对端 uid）：没过入口，空 404
	r := httptest.NewRequest("GET", "/api/jobs", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != 404 || w.Body.Len() != 0 {
		t.Fatalf("TCP 上没有 gate 应是空 404: %d %q", w.Code, w.Body)
	}
	// 页面不经 socket 提供
	if w := localDo(h, os.Geteuid(), "GET", "/", ""); w.Code != 404 || decodeErr(t, w).Code != "not_found" {
		t.Fatalf("socket 上的页面路径应 404: %d %s", w.Code, w.Body)
	}
	// 写操作照样要求 JSON
	if w := localDo(h, os.Geteuid(), "POST", "/api/jobs/1/cancel", ""); w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("非 JSON 的写请求应 415: %d", w.Code)
	}
}

func TestLocalHandlerRejectsOtherUID(t *testing.T) {
	s := newLocalServer(t)
	for _, uid := range []int{os.Geteuid() + 1, -1} {
		w := localDo(s.LocalHandler(), uid, "GET", "/api/jobs", "")
		if w.Code != 403 || decodeErr(t, w).Code != "socket_uid_mismatch" {
			t.Fatalf("uid %d 应 403 socket_uid_mismatch: %d %s", uid, w.Code, w.Body)
		}
	}
}

func TestLocalSubmitKey(t *testing.T) {
	s := newLocalServer(t)
	h := s.LocalHandler()
	body, _ := json.Marshal(map[string]any{"workdir": t.TempDir(), "cmd": "true", "submit_key": "k1"})
	first := localDo(h, os.Geteuid(), "POST", "/api/jobs", string(body))
	if first.Code != 201 {
		t.Fatalf("第一次提交应 201: %d %s", first.Code, first.Body)
	}
	var j struct {
		ID        int64  `json:"id"`
		SubmitKey string `json:"submit_key"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &j); err != nil || j.SubmitKey != "k1" {
		t.Fatalf("任务对象应带 submit_key: %s %v", first.Body, err)
	}
	if a := lastAudit(t, s.Store); a.IP != "cli" || a.Action != "submit" {
		t.Fatalf("操作记录应记为 cli: %+v", a)
	}
	again := localDo(h, os.Geteuid(), "POST", "/api/jobs", string(body))
	var j2 struct {
		ID int64 `json:"id"`
	}
	if again.Code != 200 || json.Unmarshal(again.Body.Bytes(), &j2) != nil || j2.ID != j.ID {
		t.Fatalf("重复提交应 200 并返回 #%d: %d %s", j.ID, again.Code, again.Body)
	}
	conflict, _ := json.Marshal(map[string]any{"workdir": t.TempDir(), "cmd": "false", "submit_key": "k1"})
	w := localDo(h, os.Geteuid(), "POST", "/api/jobs", string(conflict))
	if b := decodeErr(t, w); w.Code != 409 || b.Code != "job_submit_key_conflict" || b.Params["id"] != float64(j.ID) {
		t.Fatalf("内容不同应 409: %d %s", w.Code, w.Body)
	}
	bad, _ := json.Marshal(map[string]any{"workdir": t.TempDir(), "cmd": "true", "submit_key": "a b"})
	if w := localDo(h, os.Geteuid(), "POST", "/api/jobs", string(bad)); w.Code != 400 || decodeErr(t, w).Code != "job_submit_key_invalid" {
		t.Fatalf("格式不对应 400: %d %s", w.Code, w.Body)
	}
	// 没有 key 的提交照旧 201 且不出现 submit_key 字段
	plain, _ := json.Marshal(map[string]any{"workdir": t.TempDir(), "cmd": "true"})
	w = localDo(h, os.Geteuid(), "POST", "/api/jobs", string(plain))
	if _, ok := rawBody(t, w)["submit_key"]; w.Code != 201 || ok {
		t.Fatalf("不带 key: %d %s", w.Code, w.Body)
	}
}

// 没过入口的空 404 不带安全响应头：那几个头是一眼认出丹炉的指纹。过了入口的页面照常带上
func TestSecurityHeadersOnlyAfterGate(t *testing.T) {
	s := newLocalServer(t)
	h := s.Handler()
	for _, path := range []string{"/", "/api/me", "/wrong"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 || w.Body.Len() != 0 {
			t.Fatalf("%s 没过入口应是空 404: %d %q", path, w.Code, w.Body)
		}
		for _, k := range []string{"X-Frame-Options", "X-Content-Type-Options", "Referrer-Policy", "Content-Security-Policy"} {
			if v := w.Header().Get(k); v != "" {
				t.Fatalf("%s 没过入口的 404 不应带 %s: %q", path, k, v)
			}
		}
	}
	// 过入口：拿到 gate cookie 再看首页
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/abcdef", nil))
	if w.Code != http.StatusFound {
		t.Fatalf("入口应 302: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Header().Get("X-Frame-Options") != "DENY" || w.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("过了入口的页面应带安全响应头: %d %v", w.Code, w.Header())
	}
}
