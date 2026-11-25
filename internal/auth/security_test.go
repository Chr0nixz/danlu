package auth

import (
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"danlu/internal/settings"
	"danlu/internal/store"
)

func newSecurityAuth(t *testing.T) (*Auth, *store.Store) {
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
		Username: "admin", SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	if err := a.Reload(); err != nil {
		t.Fatal(err)
	}
	return a, st
}

func ptr[T any](v T) *T { return &v }

// 换入口：旧入口和别的设备的 gate 失效，发起修改的浏览器拿到新 gate，开着的长连接只看会话
func TestUpdateEntrance(t *testing.T) {
	a, st := newSecurityAuth(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", func(w http.ResponseWriter, r *http.Request) {
		if _, err := a.Login(w, r, "admin", "secret-pw", ""); err != nil {
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
	var handshake *http.Request // 模拟终端握手时留下的请求
	mux.HandleFunc("POST /api/security", func(w http.ResponseWriter, r *http.Request) {
		handshake = r.Clone(r.Context())
		if _, _, err := a.UpdateSecurity(w, r, SecurityPatch{Entrance: ptr("n3wentrance9")}); err != nil {
			t.Error(err)
		}
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("ok")) })
	srv := httptest.NewServer(a.Middleware(mux))
	t.Cleanup(srv.Close)

	client := func() *http.Client {
		jar, _ := cookiejar.New(nil)
		return &http.Client{Jar: jar}
	}
	me, other := client(), client()
	for _, c := range []*http.Client{me, other} {
		status(t, c, "GET", srv.URL+"/k3x9p2qa7m4z", "")
		if got := status(t, c, "POST", srv.URL+"/api/login", `{}`); got != 200 {
			t.Fatalf("登录应成功，实际 %d", got)
		}
	}
	if got := status(t, me, "POST", srv.URL+"/api/security", `{}`); got != 200 {
		t.Fatalf("保存应成功，实际 %d", got)
	}
	if got := status(t, me, "GET", srv.URL+"/api/me", ""); got != 200 {
		t.Fatalf("发起修改的浏览器应拿到新 gate，实际 %d", got)
	}
	if got := status(t, other, "GET", srv.URL+"/api/me", ""); got != 404 {
		t.Fatalf("别的设备的旧 gate 应失效，实际 %d", got)
	}
	if got := status(t, client(), "GET", srv.URL+"/k3x9p2qa7m4z", ""); got != 404 {
		t.Fatalf("旧入口应失效，实际 %d", got)
	}
	fresh := client()
	if got := status(t, fresh, "GET", srv.URL+"/n3wentrance9", ""); got != 200 {
		t.Fatalf("新入口应可用，实际 %d", got)
	}
	if !a.Authorized(handshake) {
		t.Fatal("握手时的凭证只带旧 gate，但会话仍有效，长连接不应被断开")
	}
	if got, _ := LoadSecurity(st, a.defaults()); got.Entrance != "n3wentrance9" {
		t.Fatalf("新入口应存进数据库，实际 %q", got.Entrance)
	}
}

func TestUpdateSecurityValidation(t *testing.T) {
	a, st := newSecurityAuth(t)
	r := httptest.NewRequest("POST", "/api/settings/security", nil)
	for name, p := range map[string]SecurityPatch{
		"入口太短":     {Entrance: ptr("abc")},
		"入口和页面重名":  {Entrance: ptr("settings")},
		"入口有大写":    {Entrance: ptr("ABCDEFGH")},
		"有效期太短":    {SessionTTL: ptr(time.Minute)},
		"有效期不是整分钟": {SessionTTL: ptr(time.Hour + time.Second)},
		"有效期太长":    {SessionTTL: ptr(400 * 24 * time.Hour)},
		"失败次数为 0":  {MaxFailures: ptr(0)},
		"锁定太长":     {Lockout: ptr(48 * time.Hour)},
	} {
		if _, _, err := a.UpdateSecurity(httptest.NewRecorder(), r, p); !settings.IsInputError(err) {
			t.Errorf("%s：应返回输入错误，实际 %v", name, err)
		}
	}
	if v, err := st.GetSettings(securityKeys...); err != nil || len(v) != 0 {
		t.Fatalf("校验失败时不应写库，实际 %v %v", v, err)
	}
	// 没改的项不写库，以后仍跟着 config.toml
	sec, changes, err := a.UpdateSecurity(httptest.NewRecorder(), r, SecurityPatch{
		Entrance: ptr("k3x9p2qa7m4z"), MaxFailures: ptr(3), Lockout: ptr(30 * time.Minute),
	})
	if err != nil || len(changes) != 2 || sec.MaxFailures != 3 || sec.Lockout != 30*time.Minute {
		t.Fatalf("UpdateSecurity = %+v %v %v", sec, changes, err)
	}
	v, _ := st.GetSettings(securityKeys...)
	if _, ok := v[entranceKey]; ok || v[maxFailuresKey] != "3" || v[lockoutKey] != "1800" {
		t.Fatalf("库里应只有改过的两项，实际 %v", v)
	}
}

// 面板上改的失败上限和锁定时长对下一次登录立即生效
func TestUpdatedLockoutApplies(t *testing.T) {
	a, _ := newSecurityAuth(t)
	if _, _, err := a.UpdateSecurity(httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil),
		SecurityPatch{MaxFailures: ptr(1), Lockout: ptr(2 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/login", nil)
	wait, err := a.Login(httptest.NewRecorder(), r, "admin", "bad", "")
	if err != ErrLocked || wait != 2*time.Hour {
		t.Fatalf("失败上限改成 1 后第一次输错就应锁定 2 小时，实际 %s %v", wait, err)
	}
}

// 缩短有效期：新登录按新时长；别的会话按新时长截止，发起修改的会话从现在算起
func TestShortenSessionTTL(t *testing.T) {
	a, st := newSecurityAuth(t)
	login := func() *http.Request {
		w := httptest.NewRecorder()
		if _, err := a.Login(w, httptest.NewRequest("POST", "/api/login", nil), "admin", "secret-pw", ""); err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest("GET", "/", nil)
		for _, c := range w.Result().Cookies() {
			r.AddCookie(c)
		}
		return r
	}
	me := login()
	if _, _, err := a.UpdateSecurity(httptest.NewRecorder(), me, SecurityPatch{SessionTTL: ptr(30 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if !a.SessionValid(me) {
		t.Fatal("发起修改的会话不应被踢出")
	}
	w := httptest.NewRecorder()
	if _, err := a.Login(w, httptest.NewRequest("POST", "/api/login", nil), "admin", "secret-pw", ""); err != nil {
		t.Fatal(err)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == a.sessionCookie && c.MaxAge != 1800 {
			t.Fatalf("新登录的 cookie 应按 30 分钟，实际 %d 秒", c.MaxAge)
		}
	}
	list, err := st.ListSessions("")
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range list {
		if left := time.Until(time.Unix(s.ExpiresAt, 0)); left > 31*time.Minute {
			t.Fatalf("会话 %s 还剩 %s，应不超过 30 分钟", s.ID, left)
		}
	}
}

// danlu entrance --reset 在另一个进程里改库，运行中的服务过一会儿就跟上
func TestEntranceFromOtherProcess(t *testing.T) {
	a, st := newSecurityAuth(t)
	if a.Security().Entrance != "k3x9p2qa7m4z" {
		t.Fatal("库里没有时应使用配置文件里的入口")
	}
	if err := SetEntrance(st, "fromcli12345"); err != nil {
		t.Fatal(err)
	}
	a.secMu.Lock()
	a.secAt = time.Time{} // 相当于过了 securityRefresh
	a.secMu.Unlock()
	if got := a.Security().Entrance; got != "fromcli12345" {
		t.Fatalf("应读到命令行改的入口，实际 %q", got)
	}
	if err := ResetSecurity(st); err != nil {
		t.Fatal(err)
	}
	if err := a.Reload(); err != nil || a.Security().Entrance != "k3x9p2qa7m4z" {
		t.Fatalf("清掉库里的值后应回到配置文件，实际 %q %v", a.Security().Entrance, err)
	}
	if err := SetEntrance(st, "api"); err == nil {
		t.Fatal("无效入口应被拒绝")
	}
}
