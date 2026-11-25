// Package strict 实现严格限卡：每个任务作为一个临时 systemd 服务运行，用 cgroup 设备控制只放行分到的卡。
//
// 改设备规则要 root，所以拆成两半：
//
//   - 助手（helper_linux.go）：root 拥有的 danlu 副本 HelperPath，只能经 sudoers 以 start、check 两个子命令调用。
//     它只会"收紧"：进程永远以调用者身份（组由 systemd 按用户库取）、在调用者的 user-<uid>.slice 里、
//     照抄调用者自己进程（必须是助手的祖先）的资源限制运行，
//     调用者能选的只有放行哪些卡（只会减少设备权限）和执行什么（本来就能以自己的身份执行）。
//   - 客户端（client_linux.go、monitor.go）：danlu 以普通用户身份用 sudo -n 调助手，缓存可用状态。
//
// 2026-11-22 在 hsy 上探测过（scripts/s0/strict-probe.sh）：只放行部分卡以后，CUDA、NVML、NVIDIA EGL
// 都只看得到放行的卡，并从 0 重新编号，所以严格任务的 CUDA_VISIBLE_DEVICES 写 0..k-1。
package strict

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	HelperName = "danlu-gpu-helper"
	HelperPath = "/usr/local/libexec/" + HelperName
	// Protocol 是 danlu 和助手之间的约定。助手是 root 拥有的副本，danlu 自己升级换不掉它，
	// 所以约定要小而稳；变了就让用户重新运行 sudo danlu strict install。
	// 2：不再照抄 danlu_pid 的组；danlu_pid 必须是助手的祖先，读不到它的资源限制就拒绝（11-23 审阅 P1-1）
	Protocol   = 2
	maxRequest = 1 << 20
	// minSystemd 是严格单元要的最低 systemd 版本：OOMPolicy= 从 243 开始才有
	minSystemd = 243
)

// StartRequest 是 danlu 交给助手的启动请求（stdin 上的 JSON）。
type StartRequest struct {
	V        int      `json:"v"`
	JobID    int64    `json:"job_id"`
	RunRoot  string   `json:"run_root"`  // <数据目录>/run，任务目录是 RunRoot/<id>
	Exec     string   `json:"exec"`      // danlu 程序，服务里以用户身份执行 job-exec
	Log      string   `json:"log"`       // 任务日志，job-exec 以追加方式打开
	EnvFile  string   `json:"env_file"`  // NUL 分隔的完整环境
	DanluPID int      `json:"danlu_pid"` // 照抄它的资源限制；必须是助手的祖先（调用 sudo 的 danlu）
	GPUs     []string `json:"gpus"`      // 放行的卡，PCI 总线号
}

// StartResult 是助手的回答。出错时只有 Error，协议不一致时 Code 是 "protocol"。
type StartResult struct {
	Unit  string `json:"unit,omitempty"`
	PID   int    `json:"pid,omitempty"`
	Error string `json:"error,omitempty"`
	Code  string `json:"code,omitempty"`
}

// ErrProtocol 表示 danlu 和助手的协议版本不一致。
var ErrProtocol = errors.New("协议版本不一致")

// CheckResult 是 check 的回答，danlu 据此判断助手能不能用。
type CheckResult struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	Systemd  int    `json:"systemd"`
	Cgroup2  bool   `json:"cgroup2"`
	Error    string `json:"error,omitempty"`
}

var busRE = regexp.MustCompile(`^([0-9a-f]{4}|[0-9a-f]{8}):([0-9a-f]{2}):([0-9a-f]{2})\.([0-7])$`)

// BusKey 把 PCI 总线号规整成 "0000:3b:00.0"：nvidia-smi 写 8 位域号，/proc 和 /sys 写 4 位。
func BusKey(bus string) (string, bool) {
	m := busRE.FindStringSubmatch(strings.ToLower(strings.TrimSpace(bus)))
	if m == nil {
		return "", false
	}
	domain, err := strconv.ParseUint(m[1], 16, 32)
	if err != nil || domain > 0xffff {
		return "", false
	}
	return fmt.Sprintf("%04x:%s:%s.%s", domain, m[2], m[3], m[4]), true
}

// Validate 做不依赖主机的检查，并把总线号规整、去重。
func (r *StartRequest) Validate() error {
	if r.V != Protocol {
		return fmt.Errorf("%w（danlu %d，助手 %d）：在服务器上重新运行 sudo danlu strict install", ErrProtocol, r.V, Protocol)
	}
	if r.JobID <= 0 {
		return errors.New("任务编号无效")
	}
	for _, p := range [][2]string{{"run_root", r.RunRoot}, {"exec", r.Exec}, {"log", r.Log}, {"env_file", r.EnvFile}} {
		if !filepath.IsAbs(p[1]) || filepath.Clean(p[1]) != p[1] {
			return fmt.Errorf("%s 必须是规整的绝对路径: %q", p[0], p[1])
		}
	}
	if r.DanluPID <= 1 {
		return errors.New("danlu_pid 无效")
	}
	if len(r.GPUs) > 64 {
		return errors.New("卡太多")
	}
	keys := make([]string, 0, len(r.GPUs))
	for _, b := range r.GPUs {
		k, ok := BusKey(b)
		if !ok {
			return fmt.Errorf("PCI 总线号无效: %q", b)
		}
		if slices.Contains(keys, k) {
			return fmt.Errorf("PCI 总线号重复: %q", b)
		}
		keys = append(keys, k)
	}
	r.GPUs = keys
	return nil
}

// RunDir 是任务目录，和 runner.RunDir 一致。
func (r *StartRequest) RunDir() string {
	return filepath.Join(r.RunRoot, strconv.FormatInt(r.JobID, 10))
}

// UnitName 带上 uid 和数据目录的哈希：同一用户的两个 danlu 实例任务编号会重复；
// 带上时间戳：同一任务放回队列后再启动，不会撞上还没回收的旧单元。
func UnitName(uid int, runRoot string, jobID int64, now time.Time) string {
	sum := sha256.Sum256([]byte(runRoot))
	return fmt.Sprintf("danlu-job-%d-%x-%d-%d.service", uid, sum[:4], jobID, now.Unix())
}

// Host 是助手读主机信息的入口，单测里换成假目录。
type Host struct {
	Proc, Sys, Dev string
	// Major 返回字符设备节点的主设备号；不是字符设备或读不到时 ok 为 false
	Major func(path string) (major uint32, ok bool)
}

var (
	cardNodeRE = regexp.MustCompile(`^nvidia[0-9]+$`)
	drmNodeRE  = regexp.MustCompile(`^(card|renderD)[0-9]+$`)
	// 所有卡共用的驱动节点，和 /dev/nvidiaN 同一个主设备号，只能按路径放行
	sharedNodes = []string{"nvidiactl", "nvidia-uvm", "nvidia-uvm-tools", "nvidia-modeset"}
)

// DevicePlan 生成 DeviceAllow 列表：除 GPU 以外的设备驱动全部放行（设备限制只用来隔离 GPU，不顺带做沙箱），
// GPU 只放行驱动公共节点和分到的卡的 /dev/nvidiaN、/dev/dri 节点。
// 要排除的主设备号从 /dev/nvidia[0-9]* 和 /dev/dri/* 节点上取，不按驱动名字猜。
//
// 主机上根本没有 NVIDIA 驱动（没有 /proc/driver/nvidia）时，不用卡的任务照样能跑：没有要藏的卡，
// 放行清单按单元启动那一刻的 /proc/devices 解析，之后才加载的驱动的主设备号不在里面，仍然被拦。
// 驱动在、却找不到 /dev/nvidiaN 节点时拿不到要排除的主设备号，一律报错。
func DevicePlan(h Host, buses []string) ([]string, error) {
	excluded := map[uint32]bool{}
	nodes, _ := os.ReadDir(h.Dev)
	for _, e := range nodes {
		if cardNodeRE.MatchString(e.Name()) {
			if major, ok := h.Major(filepath.Join(h.Dev, e.Name())); ok {
				excluded[major] = true
			}
		}
	}
	dri, _ := os.ReadDir(filepath.Join(h.Dev, "dri"))
	for _, e := range dri {
		if drmNodeRE.MatchString(e.Name()) {
			if major, ok := h.Major(filepath.Join(h.Dev, "dri", e.Name())); ok {
				excluded[major] = true
			}
		}
	}
	if len(excluded) == 0 {
		if _, err := os.Stat(filepath.Join(h.Proc, "driver", "nvidia")); len(buses) > 0 || !errors.Is(err, os.ErrNotExist) {
			return nil, errors.New("找不到 /dev/nvidiaN 设备节点")
		}
	}

	var out []string
	for _, name := range sharedNodes {
		if _, err := os.Stat(filepath.Join(h.Dev, name)); err == nil {
			out = append(out, "/dev/"+name+" rw")
		}
	}
	var gpus []os.DirEntry
	if len(buses) > 0 {
		var err error
		if gpus, err = os.ReadDir(filepath.Join(h.Proc, "driver", "nvidia", "gpus")); err != nil {
			return nil, fmt.Errorf("读不到 NVIDIA 驱动的卡列表: %w", err)
		}
	}
	drm, _ := os.ReadDir(filepath.Join(h.Sys, "class", "drm"))
	for _, bus := range buses {
		minor := ""
		for _, e := range gpus {
			if k, ok := BusKey(e.Name()); ok && k == bus {
				var err error
				if minor, err = deviceMinor(filepath.Join(h.Proc, "driver", "nvidia", "gpus", e.Name(), "information")); err != nil {
					return nil, err
				}
				break
			}
		}
		if minor == "" {
			return nil, fmt.Errorf("驱动里没有总线号为 %s 的卡", bus)
		}
		if _, err := os.Stat(filepath.Join(h.Dev, "nvidia"+minor)); err != nil {
			return nil, fmt.Errorf("卡 %s 没有设备节点 /dev/nvidia%s", bus, minor)
		}
		out = append(out, "/dev/nvidia"+minor+" rw")
		for _, e := range drm {
			if !drmNodeRE.MatchString(e.Name()) {
				continue
			}
			target, err := filepath.EvalSymlinks(filepath.Join(h.Sys, "class", "drm", e.Name(), "device"))
			if err != nil {
				continue
			}
			if k, ok := BusKey(filepath.Base(target)); ok && k == bus {
				out = append(out, "/dev/dri/"+e.Name()+" rw")
			}
		}
	}

	devices, err := os.ReadFile(filepath.Join(h.Proc, "devices"))
	if err != nil {
		return nil, fmt.Errorf("读不到 /proc/devices: %w", err)
	}
	chars, blocks := parseDevices(string(devices))
	for _, name := range sortedKeys(chars) {
		if !slices.ContainsFunc(chars[name], func(m uint32) bool { return excluded[m] }) {
			out = append(out, "char-"+name+" rwm")
		}
	}
	for _, name := range sortedKeys(blocks) {
		out = append(out, "block-"+name+" rwm")
	}
	return out, nil
}

func deviceMinor(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	for line := range strings.SplitSeq(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "Device Minor:"); ok {
			v = strings.TrimSpace(v)
			if _, err := strconv.Atoi(v); err == nil {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("%s 里没有 Device Minor", path)
}

// parseDevices 解析 /proc/devices：驱动名 → 主设备号（一个名字可能占几个主设备号）。
func parseDevices(text string) (chars, blocks map[string][]uint32) {
	chars, blocks = map[string][]uint32{}, map[string][]uint32{}
	var cur map[string][]uint32
	for line := range strings.SplitSeq(text, "\n") {
		switch strings.TrimSpace(line) {
		case "Character devices:":
			cur = chars
			continue
		case "Block devices:":
			cur = blocks
			continue
		}
		f := strings.Fields(line)
		if cur == nil || len(f) != 2 {
			continue
		}
		major, err := strconv.ParseUint(f[0], 10, 32)
		if err != nil || strings.ContainsAny(f[1], " \t") {
			continue
		}
		cur[f[1]] = append(cur[f[1]], uint32(major))
	}
	return chars, blocks
}

func sortedKeys(m map[string][]uint32) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// 只照抄常用的几项，单位和 systemd 的 LimitX= 一致（字节、个数）。
// CPU、实时、nice 这类留给 systemd 的默认值（本来也基本是 unlimited）。
var limitProps = map[string]string{
	"Max open files":      "LimitNOFILE",
	"Max locked memory":   "LimitMEMLOCK",
	"Max processes":       "LimitNPROC",
	"Max stack size":      "LimitSTACK",
	"Max core file size":  "LimitCORE",
	"Max address space":   "LimitAS",
	"Max data size":       "LimitDATA",
	"Max file size":       "LimitFSIZE",
	"Max msgqueue size":   "LimitMSGQUEUE",
	"Max pending signals": "LimitSIGPENDING",
}

var limitRE = regexp.MustCompile(`^(Max [a-z ]+?)\s{2,}(\S+)\s+(\S+)`)

// LimitProps 把 /proc/<pid>/limits 翻成 systemd 的 LimitX=软:硬。
// 照抄 danlu 自己的限制：任务拿到的和不隔离时一样，也不会超过这个用户本来就有的。
func LimitProps(text string) []string {
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		m := limitRE.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		prop, ok := limitProps[m[1]]
		if !ok {
			continue
		}
		soft, hard := limitValue(m[2]), limitValue(m[3])
		if soft == "" || hard == "" {
			continue
		}
		out = append(out, prop+"="+soft+":"+hard)
	}
	slices.Sort(out)
	return out
}

func limitValue(v string) string {
	if v == "unlimited" {
		return "infinity"
	}
	if _, err := strconv.ParseUint(v, 10, 64); err != nil {
		return ""
	}
	return v
}

// Creds 是进程的实际 uid、gid、附加组和父进程，从 /proc/<pid>/status 读。
// 助手只用 UID 核对身份、用 PPID 走祖先链；组不照抄，由 systemd 按 User= 从用户库取。
type Creds struct {
	UID, GID int
	Groups   []int
	PPID     int
}

func ParseStatus(text string) (Creds, error) {
	var c Creds
	var haveUID, haveGID bool
	for line := range strings.SplitSeq(text, "\n") {
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(val)
		switch key {
		case "Uid", "Gid":
			if len(f) == 0 {
				continue
			}
			n, err := strconv.Atoi(f[0])
			if err != nil {
				return c, fmt.Errorf("进程状态里的 %s 无效", key)
			}
			if key == "Uid" {
				c.UID, haveUID = n, true
			} else {
				c.GID, haveGID = n, true
			}
		case "PPid":
			if len(f) == 0 {
				continue
			}
			n, err := strconv.Atoi(f[0])
			if err != nil {
				return c, errors.New("进程状态里的 PPid 无效")
			}
			c.PPID = n
		case "Groups":
			for _, s := range f {
				n, err := strconv.Atoi(s)
				if err != nil {
					return c, errors.New("进程状态里的 Groups 无效")
				}
				c.Groups = append(c.Groups, n)
			}
		}
	}
	if !haveUID || !haveGID {
		return c, errors.New("进程状态里没有 Uid/Gid")
	}
	return c, nil
}

// IsAncestor 判断 pid 是不是 self 的祖先（父进程、祖父进程……），最多往上找 maxAncestors 层。
// status 读 /proc/<pid>/status 的内容。助手用它确认 danlu_pid 就是经 sudo 调它的那个 danlu，
// 而不是同一 uid 下别的进程（比如没开 userns-remap、以 gid 0 运行的容器进程）。
// 调用方要在读完那个进程的信息之后再查：那时它还是祖先，说明期间没退出，pid 也就没被复用。
func IsAncestor(self, pid int, status func(pid int) (string, error)) bool {
	cur := self
	for range maxAncestors {
		text, err := status(cur)
		if err != nil {
			return false
		}
		c, err := ParseStatus(text)
		if err != nil || c.PPID <= 1 {
			return false
		}
		if c.PPID == pid {
			return true
		}
		cur = c.PPID
	}
	return false
}

const maxAncestors = 16

// Unit 是助手交给 systemd-run 的单元。
type Unit struct {
	Name    string
	UID     int
	Devices []string
	Limits  []string
	JobID   int64
	Exec    []string
}

// Args 是 systemd-run 的参数。只有这里列出的属性，调用者影响不了别的。
//   - OOMPolicy=continue：内核 OOM 杀掉一个进程时不要连带停掉整个单元，和 danlu 自己的服务单元一致；
//   - KillMode=process：包装脚本退出后由 danlu 照旧清理残留进程并写备注；
//   - --collect：结束或失败后自动回收单元。
func (u Unit) Args() []string {
	args := []string{
		"--quiet", "--no-ask-password", "--collect",
		"--unit=" + u.Name,
		"--description=" + fmt.Sprintf("danlu 任务 #%d（严格限卡）", u.JobID),
		// 只给 uid：不带 Group=、SupplementaryGroups= 时，systemd 按用户库取主组和附加组（initgroups），
		// 调用者影响不了
		"--uid=" + strconv.Itoa(u.UID),
		"--slice=user-" + strconv.Itoa(u.UID) + ".slice",
		"--property=Type=exec",
		"--property=KillMode=process",
		"--property=OOMPolicy=continue",
		"--property=DevicePolicy=closed",
	}
	for _, d := range u.Devices {
		args = append(args, "--property=DeviceAllow="+d)
	}
	for _, l := range u.Limits {
		args = append(args, "--property="+l)
	}
	args = append(args, "--")
	return append(args, u.Exec...)
}

// ExecLine 是服务的 ExecStart：以用户身份执行 danlu job-exec，换上完整环境、接好日志后 exec 包装脚本。
func ExecLine(r StartRequest) []string {
	dir := r.RunDir()
	return []string{r.Exec, "job-exec", "--env", r.EnvFile, "--log", r.Log, "--dir", dir, "--", "/bin/bash", filepath.Join(dir, "run.sh")}
}
