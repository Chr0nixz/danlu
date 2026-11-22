package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"sync"

	"danlu/internal/store"
)

// 面板偏好：新建任务的默认值、列表每页条数、日志默认换行、资源页告警阈值。
// 存在库里（键 ui.<名字>，值是 JSON），手机和电脑共用；主题、字号这类跟屏幕有关的只存浏览器。
// 只有 GET /api/settings 读它，不用缓存。

type prefKind int

const (
	prefBool prefKind = iota
	prefInt
	prefChoice // 固定几个取值（字符串或整数）
)

type prefSpec struct {
	name    string
	label   string
	kind    prefKind
	min     int64 // prefInt
	max     int64
	choices []any // prefChoice：string 或 int64
	def     any   // bool、int64 或 string
	suffix  string
}

var prefSpecs = []prefSpec{
	{name: "new_job_gpus", label: "新建任务默认 GPU 数", kind: prefInt, min: 0, max: 16, def: int64(1), suffix: " 张"},
	{name: "new_job_bashrc", label: "新建任务默认继承 .bashrc", kind: prefBool, def: false},
	{name: "new_job_notify_done", label: "新建任务默认结束提醒", kind: prefBool, def: false},
	{name: "new_job_conda", label: "新建任务默认 conda 环境", kind: prefChoice, choices: []any{"auto", "none"}, def: "auto"},
	{name: "jobs_page_size", label: "任务列表每页条数", kind: prefChoice, choices: []any{int64(20), int64(50), int64(100)}, def: int64(20)},
	{name: "log_wrap", label: "日志默认自动换行", kind: prefBool, def: true},
	{name: "warn_cpu_percent", label: "CPU 告警阈值", kind: prefInt, min: 50, max: 100, def: int64(90), suffix: "%"},
	{name: "warn_mem_percent", label: "内存告警阈值", kind: prefInt, min: 50, max: 100, def: int64(90), suffix: "%"},
	{name: "warn_swap_percent", label: "交换区告警阈值", kind: prefInt, min: 10, max: 100, def: int64(50), suffix: "%"},
	{name: "warn_gpu_temp_c", label: "GPU 温度告警阈值", kind: prefInt, min: 60, max: 100, def: int64(83), suffix: "°C"},
	// 推送语言是服务端行为（手机收到什么语言），与面板显示语言（各设备本地）解耦
	{name: "notify_lang", label: "推送语言", kind: prefChoice, choices: []any{"zh-CN", "en"}, def: "zh-CN"},
}

const prefPrefix = "ui."

func prefKeys() []string {
	out := make([]string, len(prefSpecs))
	for i, p := range prefSpecs {
		out[i] = prefPrefix + p.name
	}
	return out
}

func lookupPref(name string) (prefSpec, bool) {
	for _, p := range prefSpecs {
		if p.name == name {
			return p, true
		}
	}
	return prefSpec{}, false
}

// parse 解析并校验一个值。坏值返回 InputError。
func (p prefSpec) parse(raw json.RawMessage) (any, error) {
	// 每项一个码，整句文案在 core 目录 error.pref_invalid_<name>（zh 与 label+rangeText 逐字一致）；
	// 改 prefSpecs 的范围或标签时同步改目录。
	bad := func() (any, error) {
		return nil, InvalidC("pref_invalid_"+p.name, p.label+p.rangeText(), nil)
	}
	switch p.kind {
	case prefBool:
		var b bool
		if err := json.Unmarshal(raw, &b); err != nil {
			return bad()
		}
		return b, nil
	case prefInt:
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil || n < p.min || n > p.max {
			return bad()
		}
		return n, nil
	default:
		if _, isStr := p.choices[0].(string); isStr {
			var s string
			if err := json.Unmarshal(raw, &s); err != nil {
				return bad()
			}
			for _, c := range p.choices {
				if c == s {
					return s, nil
				}
			}
			return bad()
		}
		var n int64
		if err := json.Unmarshal(raw, &n); err != nil {
			return bad()
		}
		for _, c := range p.choices {
			if c == n {
				return n, nil
			}
		}
		return bad()
	}
}

func (p prefSpec) rangeText() string {
	switch p.kind {
	case prefBool:
		return "需要是 true 或 false"
	case prefInt:
		return fmt.Sprintf("需要是 %d 到 %d 之间的整数", p.min, p.max)
	default:
		parts := make([]string, len(p.choices))
		for i, c := range p.choices {
			parts[i] = fmt.Sprint(c)
		}
		return fmt.Sprintf("只能是 %v 之一", parts)
	}
}

func (p prefSpec) format(v any) string {
	switch x := v.(type) {
	case bool:
		if x {
			return "开"
		}
		return "关"
	case int64:
		return fmt.Sprintf("%d%s", x, p.suffix)
	default:
		return fmt.Sprint(x)
	}
}

// PrefDefaults 是没改过时的面板偏好。
func PrefDefaults() map[string]any {
	out := make(map[string]any, len(prefSpecs))
	for _, p := range prefSpecs {
		out[p.name] = p.def
	}
	return out
}

// LoadPrefs 返回完整的、当前生效的面板偏好。库里的坏值按默认值算。
func LoadPrefs(st *store.Store) (map[string]any, error) {
	raw, err := st.GetSettings(prefKeys()...)
	if err != nil {
		return nil, err
	}
	out := PrefDefaults()
	for _, p := range prefSpecs {
		text, ok := raw[prefPrefix+p.name]
		if !ok {
			continue
		}
		if v, err := p.parse(json.RawMessage(text)); err == nil {
			out[p.name] = v
		}
	}
	return out, nil
}

// prefsMu 让两次保存一个接一个处理，免得读改写交错。
var prefsMu sync.Mutex

// UpdatePrefs 保存面板偏好里改了的项；值为 null 表示恢复默认（删掉库里的这一项）。
// 返回保存后的完整偏好和改动说明。
func UpdatePrefs(st *store.Store, patch map[string]json.RawMessage) (map[string]any, []string, error) {
	prefsMu.Lock()
	defer prefsMu.Unlock()
	cur, err := LoadPrefs(st)
	if err != nil {
		return nil, nil, err
	}
	stored, err := st.GetSettings(prefKeys()...)
	if err != nil {
		return nil, nil, err
	}
	names := make([]string, 0, len(patch))
	for name := range patch {
		names = append(names, name)
	}
	sort.Strings(names)
	next := make(map[string]any, len(cur))
	for k, v := range cur {
		next[k] = v
	}
	set := map[string]string{}
	var del, changes []string
	for _, name := range names {
		p, ok := lookupPref(name)
		if !ok {
			return cur, nil, InvalidC("pref_unknown", "未知偏好项 "+name, map[string]any{"name": name})
		}
		key := prefPrefix + name
		raw := bytes.TrimSpace(patch[name])
		if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
			if _, ok := stored[key]; !ok {
				continue
			}
			del = append(del, key)
			next[name] = p.def
			if cur[name] != p.def {
				changes = append(changes, p.label+" 恢复默认 "+p.format(p.def))
			}
			continue
		}
		v, err := p.parse(raw)
		if err != nil {
			return cur, nil, err
		}
		if v == cur[name] {
			continue
		}
		b, _ := json.Marshal(v)
		set[key] = string(b)
		next[name] = v
		changes = append(changes, p.label+" "+p.format(cur[name])+" → "+p.format(v))
	}
	if len(set) == 0 && len(del) == 0 {
		return cur, nil, nil
	}
	if err := st.UpdateSettings(set, del); err != nil {
		return cur, nil, err
	}
	return next, changes, nil
}
