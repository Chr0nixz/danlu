// Package auth 实现安全入口、登录会话和登录限速。
//
// 请求处理顺序：
//
//	GET /<入口>          → 设置 gate cookie，跳转到 /
//	没有有效 gate cookie → 一律 404，空响应体，扫描器认不出这是什么服务
//	/api/login          → 只需要 gate
//	其他 /api/*         → 还需要登录会话，否则 401
//	其他路径（前端页面）  → 只需要 gate；前端发现 401 会自己跳到登录页
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"danlu/internal/store"

	"golang.org/x/crypto/bcrypt"
)

const (
	gateCookie    = "danlu_gate"
	sessionCookie = "danlu_session"
)

var (
	ErrBadCredentials = errors.New("用户名或密码错误")
	ErrLocked         = errors.New("尝试次数过多")
)

type Options struct {
	Entrance     string
	Secret       []byte
	Secure       bool // 走 HTTPS 时给 cookie 加 Secure
	Username     string
	PasswordHash string
	SessionTTL   time.Duration
	MaxFailures  int
	Lockout      time.Duration
}

type Auth struct {
	opt   Options
	store *store.Store
	mu    sync.RWMutex // 保护 opt.PasswordHash
}

func New(st *store.Store, opt Options) *Auth { return &Auth{opt: opt, store: st} }

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

func (a *Auth) SetPasswordHash(h string) {
	a.mu.Lock()
	a.opt.PasswordHash = h
	a.mu.Unlock()
}

func (a *Auth) CheckPassword(pw string) bool {
	a.mu.RLock()
	h := a.opt.PasswordHash
	a.mu.RUnlock()
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(pw)) == nil
}

func (a *Auth) Username() string { return a.opt.Username }

func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/"+a.opt.Entrance {
			a.setGate(w)
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		if !a.gateValid(r) {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			// 写操作必须是 JSON：浏览器跨站时无法不经预检就发出 JSON 请求，借此防 CSRF
			if r.Method != http.MethodGet && r.Method != http.MethodHead &&
				!strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, `{"error":"需要 application/json"}`, http.StatusUnsupportedMediaType)
				return
			}
			if r.URL.Path != "/api/login" && !a.SessionValid(r) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusUnauthorized)
				w.Write([]byte(`{"error":"未登录或登录已过期"}`))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// ---------- gate cookie：值为 "过期时间.HMAC" ----------

func (a *Auth) gateSig(exp string) string {
	m := hmac.New(sha256.New, a.opt.Secret)
	m.Write([]byte("gate:" + a.opt.Entrance + ":" + exp))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (a *Auth) setGate(w http.ResponseWriter) {
	ttl := a.opt.SessionTTL
	exp := strconv.FormatInt(time.Now().Add(ttl).Unix(), 10)
	http.SetCookie(w, &http.Cookie{
		Name: gateCookie, Value: exp + "." + a.gateSig(exp), Path: "/",
		MaxAge: int(ttl.Seconds()), HttpOnly: true, Secure: a.opt.Secure,
		// Lax：从书签或外部链接打开入口地址时也要能带上
		SameSite: http.SameSiteLaxMode,
	})
}

func (a *Auth) gateValid(r *http.Request) bool {
	c, err := r.Cookie(gateCookie)
	if err != nil {
		return false
	}
	exp, sig, ok := strings.Cut(c.Value, ".")
	if !ok || !hmac.Equal([]byte(sig), []byte(a.gateSig(exp))) {
		return false
	}
	t, err := strconv.ParseInt(exp, 10, 64)
	return err == nil && time.Now().Unix() < t
}

// ---------- 会话 ----------

func hashToken(tok string) string {
	sum := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(sum[:])
}

func (a *Auth) SessionValid(r *http.Request) bool {
	c, err := r.Cookie(sessionCookie)
	return err == nil && c.Value != "" && a.store.SessionValid(hashToken(c.Value))
}

// CurrentSessionHash 用于"改密码后注销其他会话"。
func (a *Auth) CurrentSessionHash(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return hashToken(c.Value)
	}
	return ""
}

// Login 校验用户名密码，成功后写会话 cookie。锁定时返回 ErrLocked 和剩余时间。
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, user, pass string) (time.Duration, error) {
	ip := ClientIP(r)
	if until := a.store.LockedUntil(ip); !until.IsZero() {
		return time.Until(until), ErrLocked
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.opt.Username)) == 1
	passOK := a.CheckPassword(pass) // 用户名错也照样跑一遍 bcrypt，避免按耗时猜用户名
	if !userOK || !passOK {
		locked, err := a.store.RecordFailure(ip, a.opt.MaxFailures, a.opt.Lockout)
		if err != nil {
			return 0, err
		}
		a.store.Audit(ip, "login_fail", "user="+user)
		if locked {
			a.store.Audit(ip, "login_locked", fmt.Sprintf("锁定 %s", a.opt.Lockout))
			return a.opt.Lockout, ErrLocked
		}
		return 0, ErrBadCredentials
	}
	_ = a.store.ResetFailures(ip)
	_ = a.store.PurgeExpiredSessions()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return 0, err
	}
	tok := base64.RawURLEncoding.EncodeToString(raw)
	if err := a.store.CreateSession(hashToken(tok), a.opt.SessionTTL, ip, r.UserAgent()); err != nil {
		return 0, err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: tok, Path: "/",
		MaxAge: int(a.opt.SessionTTL.Seconds()), HttpOnly: true, Secure: a.opt.Secure,
		SameSite: http.SameSiteStrictMode,
	})
	a.store.Audit(ip, "login_ok", "user="+user)
	return 0, nil
}

func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = a.store.DeleteSession(hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1,
		HttpOnly: true, Secure: a.opt.Secure, SameSite: http.SameSiteStrictMode})
}

// ClientIP 直接取 TCP 对端地址。danlu 不放在反向代理后面，所以不信任 X-Forwarded-For。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
