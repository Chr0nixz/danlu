package settings

import (
	"fmt"
	"sort"
	"strconv"
	"sync"
	"time"

	"danlu/internal/config"
	"danlu/internal/store"
)

// Group 是面板上一次一起保存的一组设置，对应一个接口。
type Group string

const (
	GroupRuntime   Group = "runtime"   // 调度与告警、代码快照、网页终端
	GroupRetention Group = "retention" // 数据保留
)

// Values 是当前生效的运行设置。
type Values struct {
	KillGrace        time.Duration // 取消后等多久强杀
	FailFast         time.Duration // 早于这个时长失败就暂停队列，0 关闭
	SilenceAlert     time.Duration // 日志静默提醒的下限，0 关闭
	TerminalLifetime time.Duration // 页面断开后终端保留多久

	DiskWarnGB         int64 // 磁盘低余量提示，0 关闭
	DiskMinGB          int64 // 磁盘保护暂停线，0 关闭
	UnknownGPUMemoryMB int   // 来历不明的显存超过多少算卡被占用
	SnapshotMaxFileMB  int
	SnapshotMaxTotalMB int

	KeepDays   int64 // 任务文件保留天数，0 不按天数清理
	MaxTotalGB int64 // 任务文件总量上限，0 不按总量清理
}

type unit string

const (
	unitSeconds unit = "s"
	unitGB      unit = "GB"
	unitMB      unit = "MB"
	unitDays    unit = "day"
)

// hardMax 是库里的值能接受的上限，防止手工改库写进去的值在换算成 time.Duration 时溢出。
func (u unit) hardMax() int64 {
	switch u {
	case unitSeconds:
		return 100 * 365 * 24 * 3600
	case unitDays:
		return 36500
	default:
		return 1<<33 - 1
	}
}

func (u unit) format(v int64) string {
	switch u {
	case unitSeconds:
		return FormatDuration(time.Duration(v) * time.Second)
	case unitDays:
		return fmt.Sprintf("%d 天", v)
	default:
		return fmt.Sprintf("%d %s", v, u)
	}
}

type spec struct {
	name    string // 接口里的名字
	key     string // settings 表里的键
	label   string // 操作记录里的说法
	group   Group
	unit    unit
	min     int64 // 面板上允许的范围；config.toml 里超出范围的旧值照样生效
	max     int64
	zeroOff bool // 0 表示关闭
	get     func(*Values) int64
	set     func(*Values, int64)
}

func secondsOf(d time.Duration) int64   { return int64(d / time.Second) }
func fromSeconds(v int64) time.Duration { return time.Duration(v) * time.Second }

var specs = []spec{
	{"fail_fast", "runtime.fail_fast", "快速失败暂停", GroupRuntime, unitSeconds, 10, 3600, true,
		func(v *Values) int64 { return secondsOf(v.FailFast) }, func(v *Values, n int64) { v.FailFast = fromSeconds(n) }},
	{"kill_grace", "runtime.kill_grace", "取消后强杀等待", GroupRuntime, unitSeconds, 1, 600, false,
		func(v *Values) int64 { return secondsOf(v.KillGrace) }, func(v *Values, n int64) { v.KillGrace = fromSeconds(n) }},
	{"unknown_gpu_memory_mb", "runtime.unknown_gpu_memory_mb", "未知显存阈值", GroupRuntime, unitMB, 64, 131072, false,
		func(v *Values) int64 { return int64(v.UnknownGPUMemoryMB) }, func(v *Values, n int64) { v.UnknownGPUMemoryMB = int(n) }},
	{"disk_min_gb", "runtime.disk_min_gb", "磁盘保护暂停线", GroupRuntime, unitGB, 1, 100000, true,
		func(v *Values) int64 { return v.DiskMinGB }, func(v *Values, n int64) { v.DiskMinGB = n }},
	{"disk_warn_gb", "runtime.disk_warn_gb", "磁盘低余量提示", GroupRuntime, unitGB, 1, 100000, true,
		func(v *Values) int64 { return v.DiskWarnGB }, func(v *Values, n int64) { v.DiskWarnGB = n }},
	{"silence_alert", "runtime.silence_alert", "日志静默提醒", GroupRuntime, unitSeconds, 60, 86400, true,
		func(v *Values) int64 { return secondsOf(v.SilenceAlert) }, func(v *Values, n int64) { v.SilenceAlert = fromSeconds(n) }},
	{"snapshot_max_file_mb", "runtime.snapshot_max_file_mb", "快照单文件上限", GroupRuntime, unitMB, 1, 1024, false,
		func(v *Values) int64 { return int64(v.SnapshotMaxFileMB) }, func(v *Values, n int64) { v.SnapshotMaxFileMB = int(n) }},
	{"snapshot_max_total_mb", "runtime.snapshot_max_total_mb", "快照总大小上限", GroupRuntime, unitMB, 1, 10240, false,
		func(v *Values) int64 { return int64(v.SnapshotMaxTotalMB) }, func(v *Values, n int64) { v.SnapshotMaxTotalMB = int(n) }},
	{"terminal_session_lifetime", "terminal.session_lifetime", "终端断开后保留", GroupRuntime, unitSeconds, 60, 86400, false,
		func(v *Values) int64 { return secondsOf(v.TerminalLifetime) }, func(v *Values, n int64) { v.TerminalLifetime = fromSeconds(n) }},
	{"keep_days", "retention.keep_days", "保留天数", GroupRetention, unitDays, 1, 36500, true,
		func(v *Values) int64 { return v.KeepDays }, func(v *Values, n int64) { v.KeepDays = n }},
	{"max_total_gb", "retention.max_total_gb", "任务文件总量上限", GroupRetention, unitGB, 1, 100000, true,
		func(v *Values) int64 { return v.MaxTotalGB }, func(v *Values, n int64) { v.MaxTotalGB = n }},
}

// keys 是所有运行设置在库里的键。按列表删，不按前缀删：retention.env_gc 是清理器自己的状态，不归这里管。
func keys() []string {
	out := make([]string, len(specs))
	for i, s := range specs {
		out[i] = s.key
	}
	return out
}

func lookup(name string) (spec, bool) {
	for _, s := range specs {
		if s.name == name {
			return s, true
		}
	}
	return spec{}, false
}

// Defaults 取 config.toml 里的值，作为库里没有时的初始值。
func Defaults(c *config.Config) Values {
	return Values{
		KillGrace:          c.Runtime.KillGrace.Duration,
		FailFast:           c.Runtime.FailFast.Duration,
		SilenceAlert:       c.Runtime.SilenceAlert.Duration,
		TerminalLifetime:   c.Terminal.SessionLifetime.Duration,
		DiskWarnGB:         c.Runtime.DiskWarnGB,
		DiskMinGB:          c.Runtime.DiskMinGB,
		UnknownGPUMemoryMB: c.Runtime.UnknownGPUMemoryMB,
		SnapshotMaxFileMB:  c.Runtime.SnapshotMaxFileMB,
		SnapshotMaxTotalMB: c.Runtime.SnapshotMaxTotalMB,
		KeepDays:           c.Retention.KeepDays,
		MaxTotalGB:         c.Retention.MaxTotalGB,
	}
}

// ApplyTo 把生效的值写进一份配置，只用于显示、体检和 danlu prune。这份配置绝不能拿去 config.Save。
func (v Values) ApplyTo(c *config.Config) {
	c.Runtime.KillGrace.Duration = v.KillGrace
	c.Runtime.FailFast.Duration = v.FailFast
	c.Runtime.SilenceAlert.Duration = v.SilenceAlert
	c.Terminal.SessionLifetime.Duration = v.TerminalLifetime
	c.Runtime.DiskWarnGB = v.DiskWarnGB
	c.Runtime.DiskMinGB = v.DiskMinGB
	c.Runtime.UnknownGPUMemoryMB = v.UnknownGPUMemoryMB
	c.Runtime.SnapshotMaxFileMB = v.SnapshotMaxFileMB
	c.Runtime.SnapshotMaxTotalMB = v.SnapshotMaxTotalMB
	c.Retention.KeepDays = v.KeepDays
	c.Retention.MaxTotalGB = v.MaxTotalGB
}

// Load 读库里的运行设置，没有的项用 def 补上；custom 标出库里有、正在生效的项。
// 库里的值坏了（只可能是手工改库）时也用 def。
func Load(st *store.Store, def Values) (Values, map[string]bool, error) {
	raw, err := st.GetSettings(keys()...)
	if err != nil {
		return def, map[string]bool{}, err
	}
	v, custom := def, map[string]bool{}
	for _, s := range specs {
		text, ok := raw[s.key]
		if !ok {
			continue
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil || n < 0 || n > s.unit.hardMax() || (n == 0 && !s.zeroOff) {
			continue
		}
		s.set(&v, n)
		custom[s.name] = true
	}
	return v, custom, nil
}

// Reset 删掉库里的运行设置，回到 config.toml 的值。danlu init --force 重新生成配置时用；面板偏好不动。
func Reset(st *store.Store) error { return st.DeleteSettings(keys()...) }

// Overridden 列出库里改过的项，如 "fail_fast=关闭"，给启动日志和 danlu info 用：
// 这些项再改 config.toml 不起作用，要说清楚。
func Overridden(v Values, custom map[string]bool) []string {
	var out []string
	for _, s := range specs {
		if custom[s.name] {
			out = append(out, s.name+"="+describe(s, s.get(&v)))
		}
	}
	return out
}

func describe(s spec, n int64) string {
	if n == 0 && s.zeroOff {
		return "关闭"
	}
	return s.unit.format(n)
}

// Item 是设置页上的一项。
type Item struct {
	Value   int64  `json:"value"`
	Default int64  `json:"default"` // config.toml 里的值，恢复后就是它
	Custom  bool   `json:"custom"`  // 面板上改过（库里有），config.toml 里的同名项不生效
	Min     int64  `json:"min"`
	Max     int64  `json:"max"`
	ZeroOff bool   `json:"zero_off"` // 0 表示关闭
	Unit    string `json:"unit"`     // s | GB | MB | day
}

// refresh 是运行中的服务多久重新读一次库：另一个进程（danlu init --force、手工改库）改的几秒内跟上。
const refresh = 2 * time.Second

// Runtime 缓存当前生效的运行设置。各个用到它们的地方每次用时调 Current()，不订阅推送：
// 这样另一个进程改的也能跟上，也不会在持有本锁时去拿调度器的锁。
//
// 持有 mu 时不要调用任何使用方（调度器、清理器等）。
type Runtime struct {
	st  *store.Store
	def Values

	mu     sync.Mutex
	cur    Values
	custom map[string]bool
	at     time.Time // cur 上次从库里读出的时刻
}

func New(st *store.Store, def Values) *Runtime {
	return &Runtime{st: st, def: def, cur: def, custom: map[string]bool{}}
}

// Reload 立即从库里读一次。serve 启动时调用：读不到就不启动，免得按 config.toml 里的旧值调度。
func (r *Runtime) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, custom, err := Load(r.st, r.def)
	if err != nil {
		return err
	}
	r.cur, r.custom, r.at = v, custom, time.Now()
	return nil
}

// Current 返回当前生效的值。最多每 refresh 读一次库；读库失败时沿用上一次的值。
func (r *Runtime) Current() Values {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	return r.cur
}

func (r *Runtime) refreshLocked() {
	if time.Since(r.at) < refresh {
		return
	}
	if v, custom, err := Load(r.st, r.def); err == nil {
		r.cur, r.custom = v, custom
	}
	r.at = time.Now()
}

// Defaults 是 config.toml 里的值。
func (r *Runtime) Defaults() Values { return r.def }

// Overridden 见包级的 Overridden。
func (r *Runtime) Overridden() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	return Overridden(r.cur, r.custom)
}

// View 返回设置页上一组设置的当前值、默认值和允许范围。
func (r *Runtime) View(g Group) map[string]Item {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	return view(g, r.cur, r.def, r.custom)
}

func view(g Group, cur, def Values, custom map[string]bool) map[string]Item {
	out := map[string]Item{}
	for _, s := range specs {
		if s.group != g {
			continue
		}
		out[s.name] = Item{Value: s.get(&cur), Default: s.get(&def), Custom: custom[s.name],
			Min: s.min, Max: s.max, ZeroOff: s.zeroOff, Unit: string(s.unit)}
	}
	return out
}

// Patch 是要改的项：值为 nil 表示恢复成 config.toml 里的值（删掉库里的这一项）。
// 时长以秒为单位，大小以 GB 或 MB 为单位，和 Item 一致。
type Patch map[string]*int64

// Update 保存面板上改的一组设置，只写和当前值不同的项，当场生效。
// 改动套在库里的最新值上，不套在缓存上。返回新的值和改动说明（写进操作记录）。
func (r *Runtime) Update(g Group, p Patch) (Values, []string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cur, custom, err := Load(r.st, r.def)
	if err != nil {
		return r.cur, nil, err
	}
	r.cur, r.custom, r.at = cur, custom, time.Now()
	c, err := r.apply(g, p, cur, custom)
	if err != nil || len(c.changes) == 0 {
		return cur, nil, err
	}
	if err := r.st.UpdateSettings(c.set, c.del); err != nil {
		return cur, nil, err
	}
	r.cur, r.custom, r.at = c.next, c.custom, time.Now()
	return c.next, c.changes, nil
}

// Preview 按和 Update 相同的规则算出保存后的值，但不写库。数据保留在保存前预览会清理什么时用。
func (r *Runtime) Preview(g Group, p Patch) (Values, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.refreshLocked()
	c, err := r.apply(g, p, r.cur, r.custom)
	return c.next, err
}

type change struct {
	next    Values
	custom  map[string]bool
	set     map[string]string
	del     []string
	changes []string
}

func (r *Runtime) apply(g Group, p Patch, cur Values, custom map[string]bool) (change, error) {
	c := change{next: cur, custom: map[string]bool{}, set: map[string]string{}}
	for k, v := range custom {
		c.custom[k] = v
	}
	names := make([]string, 0, len(p))
	for name := range p {
		names = append(names, name)
	}
	sort.Strings(names) // 错误和改动说明的顺序固定
	changed := map[string]bool{}
	for _, name := range names {
		s, ok := lookup(name)
		if !ok || s.group != g {
			return c, Invalid("未知设置项 " + name)
		}
		old := s.get(&cur)
		val := p[name]
		if val == nil {
			if !custom[name] {
				continue
			}
			def := s.get(&r.def)
			s.set(&c.next, def)
			setExact(&c.next, &r.def, name)
			c.del = append(c.del, s.key)
			delete(c.custom, name)
			changed[name] = true
			c.changes = append(c.changes, s.label+" 恢复为配置文件里的 "+describe(s, def))
			continue
		}
		n := *val
		if n == old {
			continue
		}
		if err := validate(s, n); err != nil {
			return c, err
		}
		s.set(&c.next, n)
		c.set[s.key] = strconv.FormatInt(n, 10)
		c.custom[name] = true
		changed[name] = true
		c.changes = append(c.changes, s.label+" "+describe(s, old)+" → "+describe(s, n))
	}
	// 两项相关的只在其中一项改了时检查，config.toml 里本来就不满足的不拦着改别的项
	if changed["disk_min_gb"] || changed["disk_warn_gb"] {
		if c.next.DiskMinGB > 0 && c.next.DiskWarnGB > 0 && c.next.DiskMinGB >= c.next.DiskWarnGB {
			return c, Invalid("磁盘保护暂停线需要低于低余量提示线")
		}
	}
	if changed["snapshot_max_file_mb"] || changed["snapshot_max_total_mb"] {
		if c.next.SnapshotMaxFileMB > c.next.SnapshotMaxTotalMB {
			return c, Invalid("快照单文件上限不能超过总大小上限")
		}
	}
	return c, nil
}

// setExact 恢复默认时把 config.toml 里的原值整个拷回去：时长不一定是整秒（如 kill_grace = "1500ms"）。
func setExact(dst, def *Values, name string) {
	switch name {
	case "kill_grace":
		dst.KillGrace = def.KillGrace
	case "fail_fast":
		dst.FailFast = def.FailFast
	case "silence_alert":
		dst.SilenceAlert = def.SilenceAlert
	case "terminal_session_lifetime":
		dst.TerminalLifetime = def.TerminalLifetime
	}
}

func validate(s spec, n int64) error {
	if (n == 0 && s.zeroOff) || (n >= s.min && n <= s.max) {
		return nil
	}
	var rng string
	switch s.unit {
	case unitSeconds:
		rng = s.unit.format(s.min) + " 到 " + s.unit.format(s.max)
	case unitDays:
		rng = fmt.Sprintf("%d 到 %d 天", s.min, s.max)
	default:
		rng = fmt.Sprintf("%d 到 %d %s", s.min, s.max, s.unit)
	}
	msg := fmt.Sprintf("%s需要在 %s 之间", s.label, rng)
	if s.zeroOff {
		msg += "，或设为 0 关闭"
	}
	return Invalid(msg)
}
