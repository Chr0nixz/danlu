package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/http/cookiejar"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danlu/internal/store"
)

func setup(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	hash, _ := HashPassword("secret-pw")
	a := New(st, Options{Entrance: "k3x9p2qa7m4z", Secret: []byte("0123456789abcdef"),
		Username: "admin", PasswordHash: hash, SessionTTL: time.Hour, MaxFailures: 3, Lockout: time.Minute})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password string }
		json.NewDecoder(r.Body).Decode(&body)
		if _, err := a.Login(w, r, body.Username, body.Password); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(err.Error()))
		}
	})
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("me")) })
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("spa")) })
	srv := httptest.NewServer(a.Middleware(mux))
	t.Cleanup(srv.Close)
	jar, _ := cookiejar.New(nil)
	return srv, &http.Client{Jar: jar}
}

func status(t *testing.T, c *http.Client, method, u, body string) int {
	t.Helper()
	req, _ := http.NewRequest(method, u, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func TestGateAndLogin(t *testing.T) {
	srv, c := setup(t)
	for _, p := range []string{"/", "/api/me", "/login", "/assets/x.js", "/wrongentry"} {
		if got := status(t, c, "GET", srv.URL+p, ""); got != 404 {
			t.Errorf("未过入口访问 %s 应为 404，实际 %d", p, got)
		}
	}
	if got := status(t, c, "GET", srv.URL+"/k3x9p2qa7m4z", ""); got != 200 {
		t.Fatalf("入口跳转后应拿到页面，实际 %d", got)
	}
	if got := status(t, c, "GET", srv.URL+"/api/me", ""); got != 401 {
		t.Fatalf("未登录访问 API 应为 401，实际 %d", got)
	}
	if got := status(t, c, "POST", srv.URL+"/api/login", `{"username":"admin","password":"bad"}`); got != 401 {
		t.Fatalf("错误密码应为 401，实际 %d", got)
	}
	if got := status(t, c, "POST", srv.URL+"/api/login", `{"username":"admin","password":"secret-pw"}`); got != 200 {
		t.Fatalf("正确密码应为 200，实际 %d", got)
	}
	if got := status(t, c, "GET", srv.URL+"/api/me", ""); got != 200 {
		t.Fatalf("登录后访问 API 应为 200，实际 %d", got)
	}
}

func TestForgedGateRejected(t *testing.T) {
	srv, c := setup(t)
	u, _ := url.Parse(srv.URL)
	c.Jar.SetCookies(u, []*http.Cookie{{Name: gateCookie, Value: "9999999999.forgedsignature"}})
	if got := status(t, c, "GET", srv.URL+"/", ""); got != 404 {
		t.Fatalf("伪造的 gate cookie 应被拒绝，实际 %d", got)
	}
}

func TestLockoutAfterFailures(t *testing.T) {
	srv, c := setup(t)
	status(t, c, "GET", srv.URL+"/k3x9p2qa7m4z", "")
	for i := 0; i < 3; i++ {
		status(t, c, "POST", srv.URL+"/api/login", `{"username":"admin","password":"bad"}`)
	}
	req, _ := http.NewRequest("POST", srv.URL+"/api/login", strings.NewReader(`{"username":"admin","password":"secret-pw"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var buf [256]byte
	n, _ := resp.Body.Read(buf[:])
	if resp.StatusCode != 401 || !strings.Contains(string(buf[:n]), "尝试次数过多") {
		t.Fatalf("锁定期间正确密码也应被拒绝，实际 %d %s", resp.StatusCode, buf[:n])
	}
}

func TestNonJSONWriteRejected(t *testing.T) {
	srv, c := setup(t)
	status(t, c, "GET", srv.URL+"/k3x9p2qa7m4z", "")
	req, _ := http.NewRequest("POST", srv.URL+"/api/login", strings.NewReader("username=admin"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("表单提交应被拒绝，实际 %d", resp.StatusCode)
	}
}

func TestDeviceToken(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(st, Options{Entrance: "k3x9p2qa7m4z", Secret: []byte("0123456789abcdef"), SessionTTL: time.Hour})
	srv := httptest.NewServer(a.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })))
	t.Cleanup(srv.Close)
	id, tok, err := NewDeviceToken(st, "laptop")
	if err != nil {
		t.Fatal(err)
	}
	do := func(method, path, auth string) int {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	// 没有 gate cookie、没有登录，有效令牌也能访问 API
	if got := do("GET", "/api/me", "Bearer "+tok); got != 200 {
		t.Fatalf("有效令牌应为 200，实际 %d", got)
	}
	if got := do("POST", "/api/jobs", "bearer "+tok); got != 200 {
		t.Fatalf("Bearer 不区分大小写，写操作也应通过，实际 %d", got)
	}
	// 无效令牌和没过入口一样是 404，不暴露这是需要登录的服务
	for _, h := range []string{"Bearer dl_wrong", "Bearer "} {
		if got := do("GET", "/api/me", h); got != 404 {
			t.Fatalf("%q 应为 404，实际 %d", h, got)
		}
	}
	toks, _ := st.ListDeviceTokens()
	if len(toks) != 1 || toks[0].LastUsedAt == nil {
		t.Fatalf("使用后应记录最后使用时间: %+v", toks)
	}
	st.RevokeDeviceToken(id)
	if got := do("GET", "/api/me", "Bearer "+tok); got != 404 {
		t.Fatalf("吊销后应立即失效，实际 %d", got)
	}
}
