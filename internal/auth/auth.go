// Package auth 实现安全入口、登录会话和登录限速。
//
// 请求处理顺序：
//
//	GET /<入口>          → 设置 gate cookie，跳转到 /
//	没有有效 gate cookie → 一律 404，空响应体，扫描器认不出这是什么服务
//	/api/login          → 只需要 gate
//	其他 /api/*         → 还需要登录会话，否则 401
//	其他路径（前端页面）  → 只需要 gate；前端发现 401 会自己跳到登录页
//
// 桌面客户端不用 cookie，而是带 "Authorization: Bearer <设备令牌>"：令牌有效就跳过
// gate 和会话检查；令牌无效则和没过入口一样返回空 404，服务照样认不出来。
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
	"danlu/internal/totp"

	"golang.org/x/crypto/bcrypt"
)

const (
	gateCookie    = "danlu_gate"
	sessionCookie = "danlu_session"
)

var (
	ErrBadCredentials = errors.New("用户名或密码错误")
	ErrLocked         = errors.New("尝试次数过多")
	ErrNeedTOTP       = errors.New("请输入两步验证码")
	ErrBadTOTP        = errors.New("验证码错误或已使用")
)

const (
	totpSecretKey = "auth.totp_secret"
)

type Options struct {
	Entrance    string
	Secret      []byte
	Secure      bool // 走 HTTPS 时给 cookie 加 Secure
	Username    string
	SessionTTL  time.Duration
	MaxFailures int
	Lockout     time.Duration
}

type Auth struct {
	opt   Options
	store *store.Store

	usedMu sync.Mutex
	used   map[int64]time.Time // 设备令牌上次写入"最后使用时间"的时刻，避免每个请求都写库

	loginMu sync.Mutex
	logins  map[string]*ipLock // 正在登录的 IP，用完即删
}

type ipLock struct {
	mu   sync.Mutex
	refs int
}

func New(st *store.Store, opt Options) *Auth {
	return &Auth{opt: opt, store: st, used: map[int64]time.Time{}, logins: map[string]*ipLock{}}
}

// lockIP 让同一 IP 的登录一个接一个处理。否则并发的请求都在锁定生效前通过检查，各自跑完 bcrypt，
// 失败次数上限就挡不住猜密码。
func (a *Auth) lockIP(ip string) func() {
	a.loginMu.Lock()
	l := a.logins[ip]
	if l == nil {
		l = &ipLock{}
		a.logins[ip] = l
	}
	l.refs++
	a.loginMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		a.loginMu.Lock()
		if l.refs--; l.refs == 0 {
			delete(a.logins, ip)
		}
		a.loginMu.Unlock()
	}
}

func HashPassword(pw string) (string, error) {
	b, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	return string(b), err
}

// 密码哈希存在数据库的 settings 表里，不在 config.toml：服务进程从不写配置文件，
// 面板上改密码不会把命令行刚改过的配置写回旧值；danlu passwd 改完也立即生效。
const passwordKey = "auth.password_hash"

// SetPassword 把新密码的哈希存进数据库，立即生效。面板改密码、danlu passwd、danlu init 都走这里。
func SetPassword(st *store.Store, pw string) error {
	h, err := HashPassword(pw)
	if err != nil {
		return err
	}
	return st.SetSetting(passwordKey, h)
}

// ImportPasswordHash 兼容旧版本的安装：库里还没有密码哈希时，导入 config.toml 里的 auth.password_hash，
// 返回是否导入了。两边都没有时返回错误。
func ImportPasswordHash(st *store.Store, fromConfig string) (bool, error) {
	_, err := st.GetSetting(passwordKey)
	if err == nil {
		return false, nil
	}
	if !errors.Is(err, store.ErrNotFound) {
		return false, err
	}
	if fromConfig == "" {
		return false, errors.New("还没有设置登录密码，请先运行 danlu passwd")
	}
	return true, st.SetSetting(passwordKey, fromConfig)
}

// CheckPassword 每次从库里读哈希（只有登录和改密码时用到）。读不到就当作密码错误。
func (a *Auth) CheckPassword(pw string) bool {
	h, err := a.store.GetSetting(passwordKey)
	if err != nil {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(h), []byte(pw)) == nil
}

func (a *Auth) Username() string { return a.opt.Username }

func TOTPEnabled(st *store.Store) (bool, error) {
	secret, err := st.GetSetting(totpSecretKey)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return secret != "", err
}

func ConfigureTOTP(st *store.Store, secret string, acceptedStep int64) error {
	if _, _, err := totp.Code(secret, time.Unix(acceptedStep*30, 0)); err != nil {
		return err
	}
	created, err := st.SetSettingIfAbsentAndClaimTOTP(totpSecretKey, secret, acceptedStep)
	if err != nil {
		return err
	}
	if !created {
		return errors.New("两步验证已经开启；如需更换，请先执行 danlu totp disable")
	}
	return nil
}

func DisableTOTP(st *store.Store) error {
	return st.DeleteSettings(totpSecretKey)
}

func (a *Auth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tok, device := bearerToken(r)
		switch {
		case device:
			if !a.deviceValid(tok, ClientIP(r)) {
				w.WriteHeader(http.StatusNotFound)
				return
			}
		case r.URL.Path == "/"+a.opt.Entrance:
			a.setGate(w)
			http.Redirect(w, r, "/", http.StatusFound)
			return
		case !a.gateValid(r):
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
			if !device && r.URL.Path != "/api/login" && !a.SessionValid(r) {
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

// Authorized 用请求当初带的凭证重新判断现在还能不能访问 /api，条件和 Middleware 相同。
// 网页终端这类长连接只在握手时经过 Middleware，之后靠它复查：改密码、退出登录、会话到期、
// 吊销设备令牌都会让它返回 false。改密码可能来自另一个进程（danlu passwd），所以每次都查库。
func (a *Auth) Authorized(r *http.Request) bool {
	if tok, device := bearerToken(r); device {
		return a.deviceValid(tok, ClientIP(r))
	}
	return a.gateValid(r) && a.SessionValid(r)
}

// ---------- 设备令牌 ----------

// NewDeviceToken 生成一个设备令牌并入库，返回令牌 id 和令牌本身。令牌只在这里出现一次。
func NewDeviceToken(st *store.Store, name string) (int64, string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return 0, "", err
	}
	tok := "dl_" + base64.RawURLEncoding.EncodeToString(raw)
	id, err := st.CreateDeviceToken(name, hashToken(tok))
	return id, tok, err
}

func bearerToken(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if len(h) < 7 || !strings.EqualFold(h[:7], "Bearer ") {
		return "", false
	}
	return strings.TrimSpace(h[7:]), true
}

func (a *Auth) deviceValid(tok, ip string) bool {
	if tok == "" {
		return false
	}
	id, err := a.store.DeviceTokenID(hashToken(tok))
	if err != nil {
		return false
	}
	a.usedMu.Lock()
	stale := time.Since(a.used[id]) > time.Minute
	if stale {
		a.used[id] = time.Now()
	}
	a.usedMu.Unlock()
	if stale {
		a.store.TouchDeviceToken(id, ip)
	}
	return true
}

// CurrentSessionHash 用于"改密码后注销其他会话"。
func (a *Auth) CurrentSessionHash(r *http.Request) string {
	if c, err := r.Cookie(sessionCookie); err == nil {
		return hashToken(c.Value)
	}
	return ""
}

// Login 校验用户名密码，成功后写会话 cookie。锁定时返回 ErrLocked 和剩余时间。
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, user, pass, code string) (time.Duration, error) {
	ip := ClientIP(r)
	defer a.lockIP(ip)()
	if until := a.store.LockedUntil(ip); !until.IsZero() {
		return time.Until(until), ErrLocked
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(a.opt.Username)) == 1
	passOK := a.CheckPassword(pass) // 用户名错也照样跑一遍 bcrypt，避免按耗时猜用户名
	if !userOK || !passOK {
		return a.loginFailure(ip, user, "")
	}
	secret, err := a.store.GetSetting(totpSecretKey)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return 0, err
	}
	if secret != "" {
		if code == "" {
			// 密码已经对了，这只是让登录页显示验证码框：不计失败，也不清零，免得正常登录被锁或绕过限速
			return 0, ErrNeedTOTP
		}
		step, ok := totp.Verify(secret, code, time.Now())
		if !ok {
			return a.loginFailure(ip, user, "totp_invalid", ErrBadTOTP)
		}
		claimed, err := a.store.ClaimTOTPWindow(step)
		if err != nil {
			return 0, err
		}
		if !claimed {
			return a.loginFailure(ip, user, "totp_replay", ErrBadTOTP)
		}
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

func (a *Auth) loginFailure(ip, user, detail string, cause ...error) (time.Duration, error) {
	locked, err := a.store.RecordFailure(ip, a.opt.MaxFailures, a.opt.Lockout)
	if err != nil {
		return 0, err
	}
	if detail != "" {
		detail = " " + detail
	}
	a.store.Audit(ip, "login_fail", "user="+user+detail)
	if locked {
		a.store.Audit(ip, "login_locked", fmt.Sprintf("锁定 %s", a.opt.Lockout))
		return a.opt.Lockout, ErrLocked
	}
	if len(cause) > 0 {
		return 0, cause[0]
	}
	return 0, ErrBadCredentials
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
