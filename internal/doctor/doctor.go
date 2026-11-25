// Package doctor 提供命令行和网页共用的服务器体检。
package doctor

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"danlu/internal/config"
	"danlu/internal/execx"
	"danlu/internal/gpu"
	"danlu/internal/strict"
	"danlu/internal/system"
	"danlu/internal/tlsutil"
)

type Check struct {
	ID     string `json:"id"`
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

type Report struct {
	Checks []Check `json:"checks"`
}

// strictCheck 只在装了严格限卡助手时出现：经 sudo -n 查一次它能不能用（会在 auth.log 留一行）。
// 不知道面板上开没开，所以查不过只算 warn
func strictCheck(ctx context.Context) (Check, bool) {
	if !strict.Installed() {
		return Check{}, false
	}
	st := strict.Check(ctx)
	if st.OK {
		return Check{"strict", "ok", "严格限卡助手", "可用，版本 " + st.HelperVersion, ""}, true
	}
	return Check{"strict", "warn", "严格限卡助手", st.Reason + "：" + st.Detail, "在服务器上重新运行 sudo danlu strict install"}, true
}

// GPUCache 是服务里一直在采的 GPU 数据（*gpu.Monitor）。网页体检传它，GPU 一项读缓存、不另起 nvidia-smi：
// 驱动卡住时 nvidia-smi 杀不掉，每点一次体检就多一个卡死的进程和一个等它的 goroutine。
type GPUCache interface {
	Latest() ([]gpu.GPU, time.Time, error)
	MachineLatest() (gpu.MachineInfo, time.Time, error)
}

// Run 做一次完整体检（命令行用：自己跑 nvidia-smi）。
func Run(ctx context.Context, cfg *config.Config, paths config.Paths, version string, apiVersion int) Report {
	return RunWith(ctx, cfg, paths, version, apiVersion, nil)
}

// RunWith 和 Run 一样，gpus 不为 nil 时 GPU 一项读服务的采样缓存。
func RunWith(ctx context.Context, cfg *config.Config, paths config.Paths, version string, apiVersion int, gpus GPUCache) Report {
	ctx, cancel := context.WithTimeout(ctx, 24*time.Second)
	defer cancel()
	one := func(f func(context.Context) Check) func(context.Context) []Check {
		return func(ctx context.Context) []Check { return []Check{f(ctx)} }
	}
	items := []item{
		{"gpu", "NVIDIA 驱动与 GPU", "检查 NVIDIA 驱动和 nvidia-smi", one(func(ctx context.Context) Check {
			if gpus != nil {
				return gpuCacheCheck(gpus)
			}
			return gpuCheck(ctx, cfg.Runtime.UnknownGPUMemoryMB)
		})},
		{"conda", "conda 环境", "检查 conda 命令和服务运行环境", one(func(ctx context.Context) Check { return condaCheck(ctx, cfg.Runtime.CondaBase) })},
		{"platform", "systemd 与系统服务", "检查 systemctl 是否卡住", func(ctx context.Context) []Check { return platformChecks(ctx, paths) }},
		{"cli_socket", "命令行入口", "运行 danlu restart", func(context.Context) []Check {
			if c, ok := localSocketCheck(paths); ok {
				return []Check{c}
			}
			return nil
		}},
		{"data_disk", "数据目录磁盘", "检查数据目录所在的文件系统是否卡住", one(func(context.Context) Check {
			return diskCheck("data_disk", "数据目录磁盘", paths.Home, cfg.Runtime.DiskWarnGB, cfg.Runtime.DiskMinGB)
		})},
		{"output_disk", "输出目录磁盘", "检查输出目录所在的文件系统（网络存储）是否卡住", one(func(context.Context) Check {
			outputRoot, err := config.ResolvePath(cfg.Runtime.OutputRoot)
			if err != nil {
				return Check{"output_disk", "fail", "输出目录磁盘", err.Error(), "修正 runtime.output_root 配置"}
			}
			return diskCheck("output_disk", "输出目录磁盘", outputRoot, cfg.Runtime.DiskWarnGB, cfg.Runtime.DiskMinGB)
		})},
		{"server_cert", "服务器证书", "检查数据目录所在的文件系统是否卡住", func(context.Context) []Check { return certificateChecks(cfg, paths) }},
		{"port", "服务端口", "", one(func(context.Context) Check { return portCheck(cfg) })},
		{"strict", "严格限卡助手", "在服务器上重新运行 sudo danlu strict install", func(ctx context.Context) []Check {
			if c, ok := strictCheck(ctx); ok {
				return []Check{c}
			}
			return nil
		}},
	}
	checks := runItems(ctx, items, itemLimit)
	versionLevel, versionFix := "ok", ""
	if !releaseVersion(version) {
		versionLevel, versionFix = "warn", "使用带正式版本 tag、工作区干净时的构建"
	}
	checks = append(checks, Check{
		ID: "version", Level: versionLevel, Title: "danlu 版本",
		Detail: fmt.Sprintf("%s；API %d", valueOrNA(version), apiVersion), Fix: versionFix,
	})
	return Report{Checks: checks}
}

// item 是一项（或一组）检查；超时时用 id、title、fix 报一条"没有返回"。
type item struct {
	id, title, fix string
	run            func(context.Context) []Check
}

// itemLimit 是每项检查最多等多久。各项并行跑：nvidia-smi 卡在驱动里、statfs 卡在失联的网络存储上时
// 都杀不掉，等它的话体检永远不返回（网页上每点一次就多挂一个请求）
var itemLimit = 15 * time.Second

// runItems 并行跑各项检查，按原顺序收结果。超过 limit（或 ctx 结束）还没返回的报 fail，不再等它。
func runItems(ctx context.Context, items []item, limit time.Duration) []Check {
	results := make([]chan []Check, len(items))
	for i, it := range items {
		ch := make(chan []Check, 1)
		results[i] = ch
		go func() { ch <- it.run(ctx) }()
	}
	timer := time.NewTimer(limit)
	defer timer.Stop()
	expired := false
	checks := make([]Check, 0, 20)
	for i, ch := range results {
		var got []Check
		done := false
		if !expired {
			select {
			case got = <-ch:
				done = true
			case <-timer.C:
				expired = true
			case <-ctx.Done():
				expired = true
			}
		}
		if !done {
			select {
			case got = <-ch: // 已经超时：这时做完了的照收
				done = true
			default:
			}
		}
		if !done {
			it := items[i]
			checks = append(checks, Check{it.id, "fail", it.title,
				fmt.Sprintf("超过 %d 秒没有返回，可能卡在驱动或失联的存储上", int(limit.Seconds())), it.fix})
			continue
		}
		checks = append(checks, got...)
	}
	return checks
}

// gpuCacheCheck 读服务的 GPU 采样缓存，不另起 nvidia-smi。
func gpuCacheCheck(c GPUCache) Check {
	const id, title, fix = "gpu", "NVIDIA 驱动与 GPU", "检查 NVIDIA 驱动和 nvidia-smi"
	cards, at, err := c.Latest()
	if err != nil {
		return Check{id, "fail", title, err.Error(), fix}
	}
	if at.IsZero() {
		return Check{id, "warn", title, "服务刚启动，还没有采到 GPU 数据", "稍后再体检一次"}
	}
	info, _, err := c.MachineLatest()
	detail := fmt.Sprintf("驱动 %s；CUDA %s；%d 张 GPU", valueOrNA(info.DriverVersion), valueOrNA(info.CUDAVersion), len(cards))
	if err != nil && !errors.Is(err, gpu.ErrMachineInfoNotReady) {
		detail += "；读驱动版本失败：" + err.Error()
	}
	return Check{ID: id, Level: "ok", Title: title, Detail: detail}
}

// releaseVersion 判断是否为正式版本：开发构建以 dev 开头，带未提交改动的构建以 -dirty 结尾。
func releaseVersion(version string) bool {
	return version != "" && version != "dev" && !strings.HasPrefix(version, "dev-") && !strings.HasSuffix(version, "-dirty")
}

func oomPolicyCheck(got string) Check {
	return propertyCheck("service_oom_policy", "训练 OOM 不重启服务", got, "continue", "运行 danlu install-service，确认 OOMPolicy=continue")
}

func propertyCheck(id, title, got, want, fix string) Check {
	if got == want {
		return Check{id, "ok", title, got, ""}
	}
	return Check{id, "fail", title, fmt.Sprintf("期望 %s，实际 %s", want, valueOrNA(got)), fix}
}

func gpuCheck(ctx context.Context, unknownMemoryThresholdMB ...int) Check {
	src := gpu.NewSource("", unknownMemoryThresholdMB...)
	info := gpu.MachineInfo{}
	if machine, ok := src.(gpu.MachineSource); ok {
		var err error
		info, err = machine.Machine(ctx)
		if err != nil {
			return Check{"gpu", "fail", "NVIDIA 驱动与 GPU", err.Error(), "检查 NVIDIA 驱动和 nvidia-smi"}
		}
	}
	cards, err := src.Query(ctx)
	if err != nil {
		return Check{"gpu", "fail", "NVIDIA 驱动与 GPU", err.Error(), "检查 NVIDIA 驱动和 nvidia-smi"}
	}
	return Check{ID: "gpu", Level: "ok", Title: "NVIDIA 驱动与 GPU",
		Detail: fmt.Sprintf("驱动 %s；CUDA %s；%d 张 GPU", valueOrNA(info.DriverVersion), valueOrNA(info.CUDAVersion), len(cards))}
}

func condaCheck(ctx context.Context, configured string) Check {
	base := configured
	if base == "" {
		base = config.DetectConda()
	}
	if base == "" {
		return Check{"conda", "warn", "conda 环境", "没有检测到 conda 安装", "如使用 conda，请在 runtime.conda_base 配置安装目录"}
	}
	abs, err := filepath.Abs(base)
	if err != nil {
		return Check{"conda", "fail", "conda 环境", err.Error(), "修正 runtime.conda_base 配置"}
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		return Check{"conda", "fail", "conda 环境", "安装目录不存在: " + abs, "修正 runtime.conda_base 配置"}
	}
	exe := ""
	for _, candidate := range []string{filepath.Join(abs, "bin", "conda"), filepath.Join(abs, "condabin", "conda")} {
		if fi, err := os.Stat(candidate); err == nil && !fi.IsDir() {
			exe = candidate
			break
		}
	}
	if exe == "" {
		exe, _ = exec.LookPath("conda")
	}
	if exe == "" {
		return Check{"conda", "warn", "conda 环境", "安装目录存在，但找不到 conda 命令: " + abs, "检查 conda 安装是否完整，或将 conda 放入服务 PATH"}
	}
	out, err := output(ctx, exe, "env", "list", "--json")
	if err != nil {
		return Check{"conda", "warn", "conda 环境", "读取环境列表失败: " + err.Error(), "检查 conda 命令和服务运行环境"}
	}
	var list struct {
		Envs []string `json:"envs"`
	}
	if err := json.Unmarshal(out, &list); err != nil {
		return Check{"conda", "warn", "conda 环境", "无法解析 conda 环境列表", "检查 conda env list --json 的输出"}
	}
	return Check{ID: "conda", Level: "ok", Title: "conda 环境", Detail: fmt.Sprintf("%s；%d 个环境", abs, len(list.Envs))}
}

func diskCheck(id, title, path string, warnGB, minGB int64) Check {
	total, available, err := system.DiskSpace(path)
	if err != nil {
		return Check{id, "fail", title, fmt.Sprintf("%s：%v", path, err), "检查目录是否存在及文件系统是否可访问"}
	}
	level := "ok"
	fix := ""
	if minGB > 0 && available < uint64(minGB)<<30 {
		level, fix = "fail", "先释放磁盘空间；低于保护阈值时队列会暂停"
	} else if warnGB > 0 && available < uint64(warnGB)<<30 {
		level, fix = "warn", "清理不需要的文件，或将输出目录迁到空间更充足的分区"
	}
	detail := fmt.Sprintf("%s：剩余 %s / 共 %s", path, humanBytes(available), humanBytes(total))
	return Check{id, level, title, detail, fix}
}

func certificateChecks(cfg *config.Config, paths config.Paths) []Check {
	if cfg.TLS.Mode == config.TLSOff {
		return []Check{{"server_cert", "warn", "HTTPS 证书", "HTTPS 未启用", "仅用于本机测试时保留；公网访问应启用 HTTPS"}}
	}
	serverCert := cfg.TLS.CertFile
	caCert := ""
	if cfg.TLS.Mode == config.TLSLocalCA {
		files := tlsutil.FilesIn(paths.TLS)
		serverCert, caCert = files.Cert, files.CACert
	}
	checks := []Check{certExpiryCheck("server_cert", "服务器证书", serverCert)}
	if cfg.TLS.Mode == config.TLSLocalCA {
		checks = append(checks, certExpiryCheck("ca_cert", "本地 CA", caCert))
		cert, err := readCertificate(serverCert)
		if err != nil {
			checks = append(checks, Check{"cert_ip", "fail", "证书 IP", "无法读取服务器证书: " + err.Error(), "运行 danlu cert --regen 后重新安装 CA"})
		} else if ip := net.ParseIP(cfg.Server.PublicIP); ip == nil || !containsIP(cert.IPAddresses, ip) {
			checks = append(checks, Check{"cert_ip", "fail", "证书 IP", "服务器证书不包含 public_ip " + cfg.Server.PublicIP, "运行 danlu cert --regen 后重新安装 CA"})
		} else {
			checks = append(checks, Check{"cert_ip", "ok", "证书 IP", "包含 " + cfg.Server.PublicIP, ""})
		}
	}
	return checks
}

func certExpiryCheck(id, title, path string) Check {
	cert, err := readCertificate(path)
	if err != nil {
		return Check{id, "fail", title, "读取证书失败: " + err.Error(), "检查证书路径和 PEM 文件"}
	}
	remaining := time.Until(cert.NotAfter)
	detail := fmt.Sprintf("到期时间 %s（剩余 %d 天）", cert.NotAfter.Format(time.RFC3339), int(remaining.Hours()/24))
	if remaining <= 0 {
		return Check{id, "fail", title, detail, "续期或重新生成证书"}
	}
	if remaining < 30*24*time.Hour {
		return Check{id, "warn", title, detail, "证书将在 30 天内到期，请提前续期"}
	}
	return Check{id, "ok", title, detail, ""}
}

func readCertificate(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(b)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("PEM 中没有证书")
	}
	return x509.ParseCertificate(block.Bytes)
}

func containsIP(ips []net.IP, want net.IP) bool {
	for _, ip := range ips {
		if ip.Equal(want) {
			return true
		}
	}
	return false
}

func portCheck(cfg *config.Config) Check {
	host := cfg.Server.Bind
	if host == "" || host == "0.0.0.0" {
		host = "127.0.0.1"
	} else if host == "::" {
		host = "::1"
	}
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, fmt.Sprint(cfg.Server.Port)), 2*time.Second)
	if err != nil {
		return Check{"port", "fail", "服务端口", fmt.Sprintf("%s:%d 未监听", host, cfg.Server.Port), "检查 systemd 服务状态和 server.port 配置"}
	}
	_ = conn.Close()
	return Check{"port", "ok", "服务端口", fmt.Sprintf("%s:%d 正在监听", host, cfg.Server.Port), ""}
}

func output(parent context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	return execx.Output(execx.Command(ctx, name, args...))
}

func valueOrNA(s string) string {
	if strings.TrimSpace(s) == "" {
		return "未知"
	}
	return s
}

func parseProperties(s string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(s, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			props[key] = value
		}
	}
	return props
}

func humanBytes(n uint64) string {
	const unit = uint64(1 << 30)
	if n >= unit {
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(unit))
	}
	return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
}
