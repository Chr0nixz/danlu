package auth

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"danlu/internal/store"
	"danlu/internal/totp"
)

func setup(t *testing.T) (*httptest.Server, *http.Client) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := SetPassword(st, "secret-pw"); err != nil {
		t.Fatal(err)
	}
	a := New(st, Options{Entrance: "k3x9p2qa7m4z", Secret: []byte("0123456789abcdef"),
		Username: "admin", SessionTTL: time.Hour, MaxFailures: 3, Lockout: time.Minute})

	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password, Code string }
		json.NewDecoder(r.Body).Decode(&body)
		if _, err := a.Login(w, r, body.Username, body.Password, body.Code); err != nil {
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

// 同一 IP 并发猜密码：只有前 MaxFailures 次真正校验密码，锁定生效后一直有效
func TestConcurrentFailuresRespectLockout(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := SetPassword(st, "secret-pw"); err != nil {
		t.Fatal(err)
	}
	a := New(st, Options{Entrance: "entry123", Secret: []byte("0123456789abcdef"), Username: "admin",
		SessionTTL: time.Hour, MaxFailures: 2, Lockout: time.Minute})
	const n = 21
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r := httptest.NewRequest("POST", "/api/login", nil)
			r.RemoteAddr = "203.0.113.5:4321"
			_, err := a.Login(httptest.NewRecorder(), r, "admin", "bad", "")
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	bad := 0
	for err := range errs {
		switch {
		case errors.Is(err, ErrBadCredentials):
			bad++
		case !errors.Is(err, ErrLocked):
			t.Fatalf("意外的错误: %v", err)
		}
	}
	entries, err := st.ListAudit(100, 0)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	for _, e := range entries {
		if e.Action == "login_fail" {
			failures++
		}
	}
	if bad != 1 || failures != 2 {
		t.Fatalf("应该只校验 2 次密码（第 2 次触发锁定）：密码错误 %d 次，失败审计 %d 条", bad, failures)
	}
	if st.LockedUntil("203.0.113.5").IsZero() {
		t.Fatal("并发失败后锁定被清掉了")
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

func TestPasswordInDatabase(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	a := New(st, Options{Username: "admin"})
	if a.CheckPassword("") {
		t.Fatal("还没有密码时不能通过")
	}
	if _, err := ImportPasswordHash(st, ""); err == nil {
		t.Fatal("库里和配置里都没有密码时应该报错")
	}
	// 旧版本的安装：哈希在 config.toml 里，第一次启动时导入
	old, _ := HashPassword("from-config")
	if imported, err := ImportPasswordHash(st, old); err != nil || !imported {
		t.Fatalf("应该导入配置里的哈希: %v %v", imported, err)
	}
	if !a.CheckPassword("from-config") {
		t.Fatal("导入后应该能用旧密码登录")
	}
	// 改过密码以后，配置里残留的旧哈希不再导入
	if err := SetPassword(st, "new-password"); err != nil {
		t.Fatal(err)
	}
	if imported, _ := ImportPasswordHash(st, old); imported {
		t.Fatal("库里已经有密码时不应该再导入")
	}
	if a.CheckPassword("from-config") || !a.CheckPassword("new-password") {
		t.Fatal("改密码应该立即生效，不用重启")
	}
}

func TestTOTPLoginAndReplayProtection(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := SetPassword(st, "secret-pw"); err != nil {
		t.Fatal(err)
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	confirm, confirmStep, err := totp.Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	// 启用时确认用过的时间窗一并登记，之后不能再拿它登录
	if err := ConfigureTOTP(st, secret, confirmStep); err != nil {
		t.Fatal(err)
	}
	if on, err := TOTPEnabled(st); err != nil || !on {
		t.Fatalf("启用后应显示已开启: %v %v", on, err)
	}
	if err := ConfigureTOTP(st, secret, confirmStep+1); err == nil {
		t.Fatal("已开启时再次启用应报错，不能悄悄换掉密钥")
	}
	a := New(st, Options{Entrance: "entry123", Secret: []byte("0123456789abcdef"), Username: "admin",
		SessionTTL: time.Hour, MaxFailures: 3, Lockout: time.Minute})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Username, Password, Code string }
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, err := a.Login(w, r, body.Username, body.Password, body.Code)
		switch {
		case errors.Is(err, ErrLocked):
			w.WriteHeader(http.StatusTooManyRequests)
		case err != nil:
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	srv := httptest.NewServer(a.Middleware(mux))
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	status(t, c, "GET", srv.URL+"/entry123", "")
	login := func(code string) int {
		t.Helper()
		return status(t, c, "POST", srv.URL+"/api/login", `{"username":"admin","password":"secret-pw","code":"`+code+`"}`)
	}
	// 密码正确、还没填验证码只是让登录页显示输入框，次数再多也不能把人锁住
	for i := range 4 {
		if got := login(""); got != 401 {
			t.Fatalf("第 %d 次缺验证码应返回 401 而不是锁定，实际 %d", i+1, got)
		}
	}
	if got := login(confirm); got != 401 {
		t.Fatalf("启用时确认用过的验证码不能再登录，实际 %d", got)
	}
	next, _, err := totp.Code(secret, now.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got := login(next); got != 200 {
		t.Fatalf("下一个时间窗的验证码应允许登录，实际 %d", got)
	}
	if got := login(next); got != 401 {
		t.Fatalf("同一时间窗验证码不能重用，实际 %d", got)
	}
	// 错误验证码要计入限速：重放已记 1 次，再错 2 次达到上限
	wrong, _, _ := totp.Code(secret, now.Add(-time.Hour))
	if got := login(wrong); got != 401 {
		t.Fatalf("错误验证码应返回 401，实际 %d", got)
	}
	if got := login(wrong); got != 429 {
		t.Fatalf("验证码错误次数达到上限应锁定，实际 %d", got)
	}

	if err := DisableTOTP(st); err != nil {
		t.Fatal(err)
	}
	if on, err := TOTPEnabled(st); err != nil || on {
		t.Fatalf("关闭后应显示未开启: %v %v", on, err)
	}
}
