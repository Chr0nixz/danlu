package system

import (
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// 这个文件放资源采集里与平台无关的纯逻辑（解析、选择、抽样），不带 //go:build linux，
// 这样它们的单元测试在 Windows/macOS 开发机上也能跑。/proc、/sys 的读取仍在 resources_linux.go。

// corePercents 按核计算使用率。核数变化（CPU 热插拔）时按索引存的基准会错位，
// 这一帧全部重新采样（-1）而不是算出错值。
func corePercents(cur []cpuTimes, prev map[int]cpuTimes, elapsed float64) []int {
	out := make([]int, len(cur))
	sameCount := len(cur) == len(prev)
	for i, value := range cur {
		out[i] = -1
		if elapsed <= 0 || !sameCount {
			continue
		}
		old, ok := prev[i]
		if !ok || value.total <= old.total || value.idle < old.idle {
			continue
		}
		total := value.total - old.total
		if total == 0 {
			continue
		}
		idle := value.idle - old.idle
		out[i] = max(0, min(100, int(math.Round(float64(total-idle)/float64(total)*100))))
	}
	return out
}

// selectDiskIO 只选物理整盘：/sys/block/<name>/device 存在的设备（sda、nvme0n1、vda）。
// md RAID 和 dm（LVM、dm-crypt）的读写最终都落在物理盘上，而它们的成员常常是分区
// （Ubuntu 默认把 LVM 装在 nvme0n1p3 上），按 slaves 去重时整盘 nvme0n1 去不掉，
// 总量会算成两倍；物理盘的 busy 也才是判断 I/O 瓶颈的口径。分区不在 /sys/block 下，自然不选。
// 一块物理盘都认不出时退回整盘和阵列（按 slaves 去掉成员）；连 /sys/block 都没有时
// 退回所有非噪声设备，按名字去掉分区。
func selectDiskIO(all []diskCounter, sysBlock string) []diskCounter {
	var physical, whole []diskCounter
	for _, d := range all {
		if excludedDisk(d.Name) {
			continue
		}
		if _, err := os.Stat(filepath.Join(sysBlock, d.Name)); err != nil {
			continue
		}
		whole = append(whole, d)
		if _, err := os.Stat(filepath.Join(sysBlock, d.Name, "device")); err == nil {
			physical = append(physical, d)
		}
	}
	if len(physical) > 0 {
		return physical
	}
	if len(whole) > 0 {
		slaves := map[string]bool{}
		for _, d := range whole {
			ents, _ := os.ReadDir(filepath.Join(sysBlock, d.Name, "slaves"))
			for _, e := range ents {
				slaves[e.Name()] = true
			}
		}
		out := make([]diskCounter, 0, len(whole))
		for _, d := range whole {
			if !slaves[d.Name] {
				out = append(out, d)
			}
		}
		return out
	}
	out := make([]diskCounter, 0, len(all))
	for _, d := range all {
		if !excludedDisk(d.Name) && !partitionRe.MatchString(d.Name) {
			out = append(out, d)
		}
	}
	return out
}

// 常见的分区命名：sda1、vdb2、xvda1，nvme0n1p3、mmcblk0p1。只在读不到 /sys/block 时用。
var partitionRe = regexp.MustCompile(`^(?:(?:sd|vd|xvd|hd)[a-z]+[0-9]+|(?:nvme[0-9]+n[0-9]+|mmcblk[0-9]+)p[0-9]+)$`)

// excludedDisk 只排除噪声设备（回环、内存盘、光驱等）。
func excludedDisk(name string) bool {
	for _, prefix := range []string{"loop", "ram", "zram", "nbd", "sr", "fd"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// amdEnergySocketInputs 只挑插座能量计数器。amd_energy 把每核能量（EcoreX，前 nr_cpus 个）
// 和每插座能量（EsocketX，最后 nr_socks 个）都暴露成 energy*_input，插座能量已经包含所有核，
// 全部相加会把 CPU 功耗算成约两倍。按 label 筛选 Esocket*；读不到 label 时宁可不出数。
// 这些属性为修 CVE-2020-12912 仅 owner/group 可读，普通用户读不到时 sample() 自然降级为 —。
func amdEnergySocketInputs(dir string) []string {
	inputs, _ := filepath.Glob(filepath.Join(dir, "energy*_input"))
	var out []string
	for _, input := range inputs {
		label, err := os.ReadFile(strings.TrimSuffix(input, "_input") + "_label")
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(label)), "Esocket") {
			out = append(out, input)
		}
	}
	return out
}

// virtualNet 判断网卡是不是虚拟的：物理网卡在 /sys/class/net/<name>/device 下挂着实际设备，
// bridge（br0、vmbr0）、VLAN（eth0.100）、bond、tailscale/WireGuard/ZeroTier 隧道和容器网卡都没有。
// 它们的流量最终都要经过某块物理网卡，计入总量会重复。
func virtualNet(sysNet, name string) bool {
	if name == "lo" {
		return true
	}
	_, err := os.Stat(filepath.Join(sysNet, name, "device"))
	return err != nil
}

// knownVirtualNet 是按名字认出的容器和隧道网卡，只在一块物理网卡都没有时（容器、部分虚拟机）用来筛选。
func knownVirtualNet(name string) bool {
	for _, prefix := range []string{"docker", "veth", "br-", "virbr", "vmbr", "bond", "tun", "tap", "wg", "tailscale", "zt", "cni", "flannel", "kube", "lxc", "vmnet", "vboxnet", "p2p"} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

// netTotals 汇总收发速率：有物理网卡时只加物理网卡；没有时去掉认得出的容器/隧道网卡再加，
// 还是一块都不剩就全部相加（容器里只有 veth 的 eth0）。
func netTotals(entries []NetIface) (rx, tx uint64) {
	pick := func(keep func(NetIface) bool) bool {
		found := false
		for _, e := range entries {
			if keep(e) {
				rx += e.RxBps
				tx += e.TxBps
				found = true
			}
		}
		return found
	}
	if pick(func(e NetIface) bool { return !e.Virtual }) {
		return rx, tx
	}
	if pick(func(e NetIface) bool { return !knownVirtualNet(e.Name) }) {
		return rx, tx
	}
	pick(func(NetIface) bool { return true })
	return rx, tx
}

// discoverCPUTemps 找出每颗 CPU 的封装温度传感器：coretemp 每个插座一个 hwmon（"Package id N"），
// k10temp/zenpower 优先 Tdie（部分锐龙/线程撕裂者的 Tctl 带固定偏移）。多路服务器返回多个，
// 调用方取最高的一个。
func discoverCPUTemps(hwmonRoot string) []string {
	dirs, _ := filepath.Glob(filepath.Join(hwmonRoot, "hwmon*"))
	var out []string
	for _, dir := range dirs {
		raw, err := os.ReadFile(filepath.Join(dir, "name"))
		if err != nil {
			continue
		}
		var prefer []string
		switch strings.TrimSpace(string(raw)) {
		case "coretemp":
			prefer = []string{"Package id"}
		case "k10temp", "zenpower":
			prefer = []string{"Tdie", "Tctl"}
		default:
			continue
		}
		inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		labels := make(map[string]string, len(inputs))
		for _, input := range inputs {
			if label, err := os.ReadFile(strings.TrimSuffix(input, "_input") + "_label"); err == nil {
				labels[input] = strings.TrimSpace(string(label))
			}
		}
		chosen := ""
		for _, want := range prefer {
			for _, input := range inputs {
				if strings.HasPrefix(labels[input], want) {
					chosen = input
					break
				}
			}
			if chosen != "" {
				break
			}
		}
		// 有些内核版本没有 temp*_label，或者标签不同；已知驱动至少给第一个输入，
		// 总比整个温度字段显示为 — 好。
		if chosen == "" && len(inputs) > 0 {
			chosen = inputs[0]
		}
		if chosen != "" {
			out = append(out, chosen)
		}
	}
	return out
}

// powerReadError 把读能量计数器的错误写成面板上能看懂的原因。2020 年修 Platypus 侧信道
// （CVE-2020-8694）之后，内核把 RAPL 的 energy_uj 改成只有 root 能读，amd_energy 同理；
// danlu 以普通用户的服务运行，所以这几乎总是权限问题。
func powerReadError(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "没有权限：内核只允许 root 读 CPU 能量计数器"
	}
	return "读取 CPU 能量计数器失败: " + err.Error()
}
