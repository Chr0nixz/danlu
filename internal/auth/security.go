package auth

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"danlu/internal/config"
	"danlu/internal/settings"
	"danlu/internal/store"
)

// Security 是面板「设置 → 安全设置」里能改的几项：安全入口、登录有效期和登录失败锁定。
//
// 和密码哈希一样存在数据库的 settings 表里，改完立即生效，不用重启；服务进程从不写 config.toml。
// config.toml 里的同名项只是初始值：数据库里还没有某一项时才用它。
type Security struct {
	Entrance    string
	SessionTTL  time.Duration // 登录会话和 gate cookie 的有效期
	MaxFailures int           // 同一 IP 连续输错几次后锁定
	Lockout     time.Duration // 锁定多久
}

const (
	entranceKey    = "auth.entrance"
	sessionTTLKey  = "auth.session_ttl" // 秒
	maxFailuresKey = "auth.max_failures"
	lockoutKey     = "auth.lockout" // 秒
)

var securityKeys = []string{entranceKey, sessionTTLKey, maxFailuresKey, lockoutKey}

// 面板上允许的范围。config.toml 里超出范围的旧值照样生效，只是在面板上改的时候要落在范围内。
const (
	MinSessionTTL  = 10 * time.Minute
	MaxSessionTTL  = 365 * 24 * time.Hour
	MinMaxFailures = 1
	MaxMaxFailures = 100
	MinLockout     = time.Minute
	MaxLockout     = 24 * time.Hour
)

// securityRefresh 是运行中的服务多久重新读一次库：danlu entrance --reset 在另一个进程里改，
// 几秒内就能生效。面板上改的当场生效，不用等。
const securityRefresh = 2 * time.Second

// SecurityDefaults 取 config.toml 里的值，作为数据库里没有时的初始值。
func SecurityDefaults(c *config.Config) Security {
	return Security{
		Entrance:    c.Server.Entrance,
		SessionTTL:  c.Auth.SessionTTL.Duration,
		MaxFailures: c.Auth.MaxFailures,
		Lockout:     c.Auth.Lockout.Duration,
	}
}

// LoadSecurity 读数据库里的安全设置，没有的项用 def 补上。库里的值坏了（只可能是手工改库）时也用 def。
func LoadSecurity(st *store.Store, def Security) (Security, error) {
	values, err := st.GetSettings(securityKeys...)
	if err != nil {
		return def, err
	}
	s := def
	if v, ok := values[entranceKey]; ok && config.ValidEntrance(v) {
		s.Entrance = v
	}
	if d, ok := seconds(values[sessionTTLKey]); ok {
		s.SessionTTL = d
	}
	if n, err := strconv.Atoi(values[maxFailuresKey]); err == nil && n > 0 {
		s.MaxFailures = n
	}
	if d, ok := seconds(values[lockoutKey]); ok {
		s.Lockout = d
	}
	return s, nil
}

func seconds(v string) (time.Duration, bool) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return time.Duration(n) * time.Second, true
}

// SetEntrance 换安全入口，供 danlu entrance --reset 用。正在运行的服务几秒内跟上，不用重启。
func SetEntrance(st *store.Store, entrance string) error {
	if !config.ValidEntrance(entrance) {
		return fmt.Errorf("安全入口 %q 无效", entrance)
	}
	return st.SetSetting(entranceKey, entrance)
}

// ResetSecurity 删掉数据库里的安全设置，回到 config.toml 的值。danlu init --force 重新生成配置时用。
func ResetSecurity(st *store.Store) error { return st.DeleteSettings(securityKeys...) }

// Security 返回当前生效的安全设置。最多每 securityRefresh 读一次库；读库失败时沿用上一次的值。
func (a *Auth) Security() Security {
	a.secMu.Lock()
	defer a.secMu.Unlock()
	if time.Since(a.secAt) >= securityRefresh {
		if s, err := LoadSecurity(a.store, a.defaults()); err == nil {
			a.sec = s
		}
		a.secAt = time.Now()
	}
	return a.sec
}

// Reload 立即从库里读一次。serve 启动时调用：读不到就不启动，免得拿 config.toml 里的旧入口顶上。
func (a *Auth) Reload() error {
	a.secMu.Lock()
	defer a.secMu.Unlock()
	s, err := LoadSecurity(a.store, a.defaults())
	if err != nil {
		return err
	}
	a.sec, a.secAt = s, time.Now()
	return nil
}

func (a *Auth) defaults() Security {
	return Security{Entrance: a.opt.Entrance, SessionTTL: a.opt.SessionTTL, MaxFailures: a.opt.MaxFailures, Lockout: a.opt.Lockout}
}

// SecurityPatch 是要改的项，nil 表示不改。
type SecurityPatch struct {
	Entrance    *string
	SessionTTL  *time.Duration
	MaxFailures *int
	Lockout     *time.Duration
}

// UpdateSecurity 保存面板上改的安全设置，只写和当前值不同的项（没改的项以后仍跟着 config.toml），
// 当场生效。返回新的设置和改动说明（写进操作记录，不含入口本身）。
// 改动套在库里的最新值上，不套在缓存上：danlu entrance --reset 刚改过的入口不会被改回去。
//
//   - 换了入口：旧入口和其他设备上的 gate cookie 立即失效；发起请求的浏览器拿到新的 gate cookie，不会被关在门外。
//   - 缩短了有效期：已有会话也按新时长截止，其他会话从登录时算起，当前会话从现在算起，免得保存完就被踢出去。
func (a *Auth) UpdateSecurity(w http.ResponseWriter, r *http.Request, p SecurityPatch) (Security, []string, error) {
	a.secMu.Lock()
	defer a.secMu.Unlock()
	cur, err := LoadSecurity(a.store, a.defaults())
	if err != nil {
		return a.sec, nil, err
	}
	a.sec, a.secAt = cur, time.Now()
	next := cur
	if p.Entrance != nil {
		next.Entrance = *p.Entrance
	}
	if p.SessionTTL != nil {
		next.SessionTTL = *p.SessionTTL
	}
	if p.MaxFailures != nil {
		next.MaxFailures = *p.MaxFailures
	}
	if p.Lockout != nil {
		next.Lockout = *p.Lockout
	}
	values := map[string]string{}
	var changes []string
	if next.Entrance != cur.Entrance {
		if !config.ValidEntrance(next.Entrance) {
			return cur, nil, settings.Invalid("安全入口需要 6–64 位小写字母或数字，且不能和面板自己的页面重名")
		}
		values[entranceKey] = next.Entrance
		changes = append(changes, "更换安全入口")
	}
	if next.SessionTTL != cur.SessionTTL {
		if next.SessionTTL < MinSessionTTL || next.SessionTTL > MaxSessionTTL || next.SessionTTL%time.Minute != 0 {
			return cur, nil, settings.Invalid("登录有效期需要在 10 分钟到 365 天之间，按整分钟计")
		}
		values[sessionTTLKey] = strconv.FormatInt(int64(next.SessionTTL/time.Second), 10)
		changes = append(changes, "登录有效期 "+settings.FormatDuration(cur.SessionTTL)+" → "+settings.FormatDuration(next.SessionTTL))
	}
	if next.MaxFailures != cur.MaxFailures {
		if next.MaxFailures < MinMaxFailures || next.MaxFailures > MaxMaxFailures {
			return cur, nil, settings.Invalid("失败次数上限需要在 1 到 100 次之间")
		}
		values[maxFailuresKey] = strconv.Itoa(next.MaxFailures)
		changes = append(changes, fmt.Sprintf("失败上限 %d → %d 次", cur.MaxFailures, next.MaxFailures))
	}
	if next.Lockout != cur.Lockout {
		if next.Lockout < MinLockout || next.Lockout > MaxLockout || next.Lockout%time.Minute != 0 {
			return cur, nil, settings.Invalid("锁定时长需要在 1 分钟到 24 小时之间，按整分钟计")
		}
		values[lockoutKey] = strconv.FormatInt(int64(next.Lockout/time.Second), 10)
		changes = append(changes, "锁定时长 "+settings.FormatDuration(cur.Lockout)+" → "+settings.FormatDuration(next.Lockout))
	}
	if len(values) == 0 {
		return cur, nil, nil
	}
	// 设置和收紧已有会话在一个事务里：失败时什么都没变，内存里的设置和 gate cookie 也不动
	var clamp time.Duration
	if next.SessionTTL < cur.SessionTTL {
		clamp = next.SessionTTL
	}
	if err := a.store.SaveSecurity(values, clamp, a.CurrentSessionHash(r)); err != nil {
		return cur, nil, err
	}
	a.sec, a.secAt = next, time.Now()
	if next.Entrance != cur.Entrance {
		if _, device := bearerToken(r); !device {
			a.setGate(w, next)
		}
	}
	return next, changes, nil
}
