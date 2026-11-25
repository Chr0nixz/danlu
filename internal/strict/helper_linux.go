//go:build linux

package strict

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// RunHelper 是 HelperPath 的入口，由 sudo 以 root 调用，返回退出码。
// main() 最先按程序名分流到这里：不读配置，不碰数据目录。结果是 stdout 上的一行 JSON。
func RunHelper(args []string, stdin io.Reader, stdout io.Writer, version string) int {
	enc := json.NewEncoder(stdout)
	if len(args) != 1 {
		_ = enc.Encode(StartResult{Error: "用法: " + HelperName + " start|check"})
		return 2
	}
	if os.Geteuid() != 0 {
		_ = enc.Encode(StartResult{Error: HelperName + " 要经 sudo 以 root 运行"})
		return 1
	}
	switch args[0] {
	case "check":
		res := CheckResult{Protocol: Protocol, Version: version, Systemd: systemdVersion(), Cgroup2: cgroup2()}
		if !res.Cgroup2 {
			res.Error = "不是 cgroup v2"
		}
		_ = enc.Encode(res)
		return 0
	case "start":
		uid, err := sudoUID()
		if err != nil {
			_ = enc.Encode(StartResult{Error: err.Error()})
			return 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		res, err := helperStart(ctx, uid, stdin, time.Now())
		if err != nil {
			res = StartResult{Error: err.Error()}
			if errors.Is(err, ErrProtocol) {
				res.Code = ReasonProtocol
			}
			_ = enc.Encode(res)
			return 1
		}
		_ = enc.Encode(res)
		return 0
	}
	_ = enc.Encode(StartResult{Error: "未知子命令 " + strconv.Quote(args[0])})
	return 2
}

// sudoUID 只信 sudo 设置的调用者 uid；直接以 root 运行（没有 SUDO_UID）或调用者就是 root 时拒绝。
func sudoUID() (int, error) {
	v := os.Getenv("SUDO_UID")
	uid, err := strconv.Atoi(v)
	if v == "" || err != nil || uid <= 0 {
		return 0, errors.New("只接受普通用户经 sudo 调用（没有有效的 SUDO_UID）")
	}
	return uid, nil
}

func helperStart(ctx context.Context, uid int, in io.Reader, now time.Time) (StartResult, error) {
	var req StartRequest
	dec := json.NewDecoder(io.LimitReader(in, maxRequest))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return StartResult{}, fmt.Errorf("请求无效: %w", err)
	}
	if err := req.Validate(); err != nil {
		return StartResult{}, err
	}
	if err := ownedDir(req.RunRoot, uid); err != nil {
		return StartResult{}, err
	}
	// 资源限制照抄 danlu 自己的，但 danlu_pid 是调用者给的：先读，再确认它是助手的祖先
	// （danlu → sudo → 助手）。只核对 uid 不够，同一 uid 下还可能有组或限制更宽的进程（容器、sudo -g 起的进程）。
	// 读完再查祖先链：那时它还是祖先，说明期间没退出，读到的就是它的，不是复用了 pid 号的别的进程
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", req.DanluPID))
	if err != nil {
		return StartResult{}, fmt.Errorf("读不到 danlu 进程 %d 的状态", req.DanluPID)
	}
	creds, err := ParseStatus(string(status))
	if err != nil {
		return StartResult{}, err
	}
	if creds.UID != uid {
		return StartResult{}, fmt.Errorf("进程 %d 不属于调用者", req.DanluPID)
	}
	limitsText, err := os.ReadFile(fmt.Sprintf("/proc/%d/limits", req.DanluPID))
	if err != nil {
		return StartResult{}, fmt.Errorf("读不到 danlu 进程 %d 的资源限制", req.DanluPID)
	}
	limits := LimitProps(string(limitsText))
	if len(limits) == 0 {
		// 读不出就用 systemd 系统服务的默认值，可能比这个用户本来的限制宽
		return StartResult{}, fmt.Errorf("解析不了 danlu 进程 %d 的资源限制", req.DanluPID)
	}
	readStatus := func(pid int) (string, error) {
		b, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
		return string(b), err
	}
	if !IsAncestor(os.Getpid(), req.DanluPID, readStatus) {
		return StartResult{}, fmt.Errorf("进程 %d 不是调用助手的 danlu", req.DanluPID)
	}
	devices, err := DevicePlan(LinuxHost(), req.GPUs)
	if err != nil {
		return StartResult{}, err
	}
	unit := Unit{Name: UnitName(uid, req.RunRoot, req.JobID, now), UID: uid, Devices: devices,
		Limits: limits, JobID: req.JobID, Exec: ExecLine(req)}
	if out, err := runTool(ctx, "systemd-run", unit.Args()...); err != nil {
		// systemd-run 报错时单元可能已经起来了（等 D-Bus 回答超时之类）：停掉它，免得任务脱管运行。
		// 单元名带时间戳，只会是这一次的；停一个不存在的单元也无害。ctx 可能已经到期，另给一个
		stopCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		_, _ = runTool(stopCtx, "systemctl", "stop", "--no-block", unit.Name)
		cancel()
		return StartResult{}, fmt.Errorf("systemd-run 失败: %v %s", err, clip(out))
	}
	// Type=exec：systemd-run 返回时 ExecStart 已经 exec 成功。任务立刻结束的话 MainPID 是 0，
	// danlu 照旧从 pid 文件和退出码文件收尾
	out, err := runTool(ctx, "systemctl", "show", "--property=MainPID", "--value", unit.Name)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		pid = 0
	}
	return StartResult{Unit: unit.Name, PID: pid}, nil
}

// ownedDir 确认目录属于调用者：助手只在调用者自己的数据目录下启动任务。
// 出错时不把 stat 的原始错误带回去：调用者能借它探测只有 root 看得到的路径存不存在
func ownedDir(path string, uid int) error {
	st, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("%s 不存在或不是调用者的目录", path)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !st.IsDir() || !ok || int(sys.Uid) != uid {
		return fmt.Errorf("%s 不存在或不是调用者的目录", path)
	}
	return nil
}

// runTool 只执行系统目录里的 systemd 工具，环境清空，不受调用者的 PATH 影响
func runTool(ctx context.Context, name string, args ...string) ([]byte, error) {
	var path string
	for _, dir := range []string{"/usr/bin", "/bin"} {
		if _, err := os.Stat(dir + "/" + name); err == nil {
			path = dir + "/" + name
			break
		}
	}
	if path == "" {
		return nil, fmt.Errorf("找不到 %s", name)
	}
	return runPath(ctx, path, args...)
}

// runPath 执行绝对路径上的程序：环境清空，标准输出和错误合在一起返回
func runPath(ctx context.Context, path string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}
	cmd.Stdin = nil
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.Bytes(), err
}

// LinuxHost 读真实的 /proc、/sys、/dev。
func LinuxHost() Host {
	return Host{Proc: "/proc", Sys: "/sys", Dev: "/dev", Major: func(path string) (uint32, bool) {
		var st syscall.Stat_t
		if err := syscall.Stat(path, &st); err != nil || st.Mode&syscall.S_IFMT != syscall.S_IFCHR {
			return 0, false
		}
		return devMajor(uint64(st.Rdev)), true
	}}
}

// devMajor 同 unix.Major：Linux 的 dev_t 把主设备号拆在两段里
func devMajor(dev uint64) uint32 {
	return uint32((dev>>8)&0xfff) | uint32((dev>>32)&^0xfff)
}

func systemdVersion() int {
	out, err := runTool(context.Background(), "systemctl", "--version")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(out))
	if len(f) < 2 || f[0] != "systemd" {
		return 0
	}
	v, _ := strconv.Atoi(f[1])
	return v
}

func cgroup2() bool {
	var st syscall.Statfs_t
	return syscall.Statfs("/sys/fs/cgroup", &st) == nil && st.Type == 0x63677270
}
