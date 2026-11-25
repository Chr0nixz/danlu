package strict

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBusKey(t *testing.T) {
	for in, want := range map[string]string{
		"00000000:3B:00.0":   "0000:3b:00.0",
		"0000:3b:00.0":       "0000:3b:00.0",
		" 00000001:08:1f.7 ": "0001:08:1f.7",
	} {
		if got, ok := BusKey(in); !ok || got != want {
			t.Errorf("BusKey(%q) = %q %v，应为 %q", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "3b:00.0", "0000:3b:00.8", "0000:3b:00.0/../x", "000100000:3b:00.0"} {
		if _, ok := BusKey(bad); ok {
			t.Errorf("BusKey(%q) 应判无效", bad)
		}
	}
}

func validRequest() StartRequest {
	return StartRequest{V: Protocol, JobID: 82, RunRoot: "/home/u/.danlu/run", Exec: "/home/u/bin/danlu",
		Log: "/home/u/.danlu/logs/82.log", EnvFile: "/home/u/.danlu/run/82/env", DanluPID: 1234,
		GPUs: []string{"00000000:1F:00.0", "0000:20:00.0"}}
}

func TestValidate(t *testing.T) {
	r := validRequest()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(r.GPUs, []string{"0000:1f:00.0", "0000:20:00.0"}) {
		t.Fatalf("总线号应规整: %v", r.GPUs)
	}
	if r.RunDir() != "/home/u/.danlu/run/82" {
		t.Fatal(r.RunDir())
	}
	for name, mutate := range map[string]func(*StartRequest){
		"协议":     func(r *StartRequest) { r.V = Protocol - 1 },
		"编号":     func(r *StartRequest) { r.JobID = 0 },
		"相对路径":   func(r *StartRequest) { r.Exec = "bin/danlu" },
		"不规整路径":  func(r *StartRequest) { r.RunRoot = "/home/u/../root/run" },
		"pid":    func(r *StartRequest) { r.DanluPID = 1 },
		"总线号无效":  func(r *StartRequest) { r.GPUs = []string{"3b:00.0"} },
		"总线号重复":  func(r *StartRequest) { r.GPUs = []string{"0000:1f:00.0", "00000000:1F:00.0"} },
		"空总线号列表": nil,
	} {
		r := validRequest()
		if mutate == nil {
			r.GPUs = nil
			if err := r.Validate(); err != nil {
				t.Errorf("不用卡的任务也要能启动: %v", err)
			}
			continue
		}
		mutate(&r)
		if err := r.Validate(); err == nil {
			t.Errorf("%s：应校验失败", name)
		}
	}
}

func TestUnitName(t *testing.T) {
	now := time.Unix(1790000000, 0)
	a := UnitName(1001, "/home/u/.danlu/run", 82, now)
	if !strings.HasPrefix(a, "danlu-job-1001-") || !strings.HasSuffix(a, "-82-1790000000.service") {
		t.Fatalf("单元名格式不对: %s", a)
	}
	if b := UnitName(1001, "/home/u/other/run", 82, now); b == a {
		t.Fatal("不同数据目录的同号任务不能撞名")
	}
}

// fakeHost 模仿 hsy：两张卡，/dev/nvidiaN 和 DRM 节点的主设备号分别是 195、226，另有一个和卡共用 195 的驱动名
func fakeHost(t *testing.T) Host {
	t.Helper()
	root := t.TempDir()
	h := Host{Proc: filepath.Join(root, "proc"), Sys: filepath.Join(root, "sys"), Dev: filepath.Join(root, "dev")}
	write := func(path, text string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(h.Proc, "devices"), `Character devices:
  1 mem
136 pts
195 nvidia-frontend
195 nvidia
226 drm
234 nvidia-uvm
509 nvidia-caps

Block devices:
  7 loop
  8 sd
259 blkext
`)
	majors := map[string]uint32{}
	for i, bus := range []string{"0000:08:00.0", "0000:09:00.0"} {
		minor := []string{"0", "3"}[i] // 次号和编号不一定一样
		write(filepath.Join(h.Proc, "driver/nvidia/gpus", bus, "information"), "Model: X\nDevice Minor: \t "+minor+"\n")
		write(filepath.Join(h.Dev, "nvidia"+minor), "")
		majors[filepath.Join(h.Dev, "nvidia"+minor)] = 195
		pci := filepath.Join(root, "pci", bus)
		if err := os.MkdirAll(pci, 0o755); err != nil {
			t.Fatal(err)
		}
		for _, node := range []string{"card" + string(rune('1'+i)), "renderD12" + string(rune('8'+i)), "card" + string(rune('1'+i)) + "-DP-1"} {
			dir := filepath.Join(h.Sys, "class/drm", node)
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(pci, filepath.Join(dir, "device")); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(node, "-") {
				write(filepath.Join(h.Dev, "dri", node), "")
				majors[filepath.Join(h.Dev, "dri", node)] = 226
			}
		}
	}
	for _, name := range []string{"nvidiactl", "nvidia-uvm", "nvidia-modeset"} {
		write(filepath.Join(h.Dev, name), "")
	}
	h.Major = func(path string) (uint32, bool) {
		m, ok := majors[path]
		return m, ok
	}
	return h
}

func TestDevicePlan(t *testing.T) {
	h := fakeHost(t)
	got, err := DevicePlan(h, []string{"0000:09:00.0"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"/dev/nvidiactl rw", "/dev/nvidia-uvm rw", "/dev/nvidia-modeset rw",
		"/dev/nvidia3 rw", "/dev/dri/card2 rw", "/dev/dri/renderD129 rw",
		"char-mem rwm", "char-nvidia-caps rwm", "char-nvidia-uvm rwm", "char-pts rwm",
		"block-blkext rwm", "block-loop rwm", "block-sd rwm",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("放行清单不对:\n got %q\nwant %q", got, want)
	}
	for _, d := range got {
		if strings.Contains(d, "nvidia0") || strings.Contains(d, "card1") || strings.Contains(d, "char-drm") || strings.Contains(d, "char-nvidia ") {
			t.Fatalf("没分到的卡和 GPU 主设备号不能放行: %s", d)
		}
	}

	none, err := DevicePlan(h, nil)
	if err != nil || slices.ContainsFunc(none, func(d string) bool {
		return strings.HasPrefix(d, "/dev/nvidia0") || strings.HasPrefix(d, "/dev/nvidia3")
	}) {
		t.Fatalf("不用卡的任务一张卡也不放行: %q %v", none, err)
	}
	if _, err := DevicePlan(h, []string{"0000:aa:00.0"}); err == nil {
		t.Fatal("驱动里没有的卡应报错")
	}
}

func TestLimitProps(t *testing.T) {
	text := `Limit                     Soft Limit           Hard Limit           Units
Max cpu time              unlimited            unlimited            seconds
Max file size             unlimited            unlimited            bytes
Max stack size            8388608              unlimited            bytes
Max core file size        0                    unlimited            bytes
Max processes             1031417              1031417              processes
Max open files            1024                 524288               files
Max locked memory         8388608              8388608              bytes
Max realtime timeout      unlimited            unlimited            us
`
	want := []string{"LimitCORE=0:infinity", "LimitFSIZE=infinity:infinity", "LimitMEMLOCK=8388608:8388608",
		"LimitNOFILE=1024:524288", "LimitNPROC=1031417:1031417", "LimitSTACK=8388608:infinity"}
	if got := LimitProps(text); !slices.Equal(got, want) {
		t.Fatalf("资源限制翻译不对:\n got %q\nwant %q", got, want)
	}
}

func TestParseStatus(t *testing.T) {
	c, err := ParseStatus("Name:\tdanlu\nPPid:\t812\nUid:\t1001\t1001\t1001\t1001\nGid:\t1002\t1002\t1002\t1002\nGroups:\t27 44 1002 \n")
	if err != nil || c.UID != 1001 || c.GID != 1002 || c.PPID != 812 || !slices.Equal(c.Groups, []int{27, 44, 1002}) {
		t.Fatalf("%+v %v", c, err)
	}
	if _, err := ParseStatus("Name:\tx\n"); err == nil {
		t.Fatal("没有 Uid/Gid 应报错")
	}
}

func TestUnitArgs(t *testing.T) {
	r := validRequest()
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
	u := Unit{Name: "danlu-job-1001-abcd-82-1.service", UID: 1001,
		Devices: []string{"/dev/nvidia3 rw", "char-pts rwm"}, Limits: []string{"LimitNOFILE=1024:524288"}, JobID: 82, Exec: ExecLine(r)}
	args := u.Args()
	for _, want := range []string{"--uid=1001", "--slice=user-1001.slice", "--property=DevicePolicy=closed",
		"--property=DeviceAllow=/dev/nvidia3 rw", "--property=LimitNOFILE=1024:524288",
		"--property=OOMPolicy=continue", "--property=KillMode=process"} {
		if !slices.Contains(args, want) {
			t.Errorf("缺少参数 %q", want)
		}
	}
	// 组由 systemd 按用户库取，调用者给不了
	for _, a := range args {
		if strings.HasPrefix(a, "--gid") || strings.Contains(a, "Group") {
			t.Errorf("不应带组参数: %q", a)
		}
	}
	sep := slices.Index(args, "--")
	wantExec := []string{"/home/u/bin/danlu", "job-exec", "--env", "/home/u/.danlu/run/82/env", "--log", "/home/u/.danlu/logs/82.log",
		"--dir", "/home/u/.danlu/run/82", "--", "/bin/bash", "/home/u/.danlu/run/82/run.sh"}
	if sep < 0 || !slices.Equal(args[sep+1:], wantExec) {
		t.Fatalf("ExecStart 不对: %q", args[sep+1:])
	}
}

// danlu_pid 必须是助手的祖先：同一 uid 下的别的进程（比如以 gid 0 运行的容器进程）不行
func TestIsAncestor(t *testing.T) {
	// 1 ← 700(systemd --user) ← 800(danlu) ← 900(sudo) ← 950(助手)；600 是同一 uid 的容器进程
	parent := map[int]int{950: 900, 900: 800, 800: 700, 700: 1, 600: 1}
	status := func(pid int) (string, error) {
		pp, ok := parent[pid]
		if !ok {
			return "", os.ErrNotExist
		}
		return fmt.Sprintf("Name:\tx\nPPid:\t%d\nUid:\t1001\t1001\t1001\t1001\nGid:\t1001\t1001\t1001\t1001\n", pp), nil
	}
	for pid, want := range map[int]bool{900: true, 800: true, 700: true, 600: false, 950: false, 1: false, 4242: false} {
		if got := IsAncestor(950, pid, status); got != want {
			t.Errorf("IsAncestor(950, %d) = %v，期望 %v", pid, got, want)
		}
	}
	// 祖先链读不下去（进程已经退出）时不认
	delete(parent, 900)
	if IsAncestor(950, 800, status) {
		t.Error("祖先链断了还认")
	}
	// 成环的假数据不会死循环
	loop := map[int]int{10: 11, 11: 10}
	if IsAncestor(10, 99, func(pid int) (string, error) {
		return fmt.Sprintf("PPid:\t%d\nUid:\t1\t1\t1\t1\nGid:\t1\t1\t1\t1\n", loop[pid]), nil
	}) {
		t.Error("成环时不该认")
	}
}

// GitHub runner 这类没有 NVIDIA 驱动的机器：不用卡的严格任务照样能跑，要卡就报错。
// 驱动在、但 /dev/nvidiaN 还没建出来时拿不到要排除的主设备号，不用卡也报错（否则会放行整个 nvidia 主设备号）
func TestDevicePlanWithoutNVIDIA(t *testing.T) {
	root := t.TempDir()
	h := Host{Proc: filepath.Join(root, "proc"), Sys: filepath.Join(root, "sys"), Dev: filepath.Join(root, "dev"),
		Major: func(string) (uint32, bool) { return 0, false }}
	for _, dir := range []string{h.Proc, h.Sys, h.Dev} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(h.Proc, "devices"), []byte("Character devices:\n  1 mem\n136 pts\n\nBlock devices:\n  8 sd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DevicePlan(h, nil)
	if want := []string{"char-mem rwm", "char-pts rwm", "block-sd rwm"}; err != nil || !slices.Equal(got, want) {
		t.Fatalf("没有 NVIDIA 驱动时不用卡的任务应放行普通设备: %q %v", got, err)
	}
	if _, err := DevicePlan(h, []string{"0000:08:00.0"}); err == nil {
		t.Fatal("没有 NVIDIA 驱动时要卡应报错")
	}
	if err := os.MkdirAll(filepath.Join(h.Proc, "driver", "nvidia", "gpus"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := DevicePlan(h, nil); err == nil {
		t.Fatal("驱动在但没有 /dev/nvidiaN 时不用卡也应报错")
	}
}
