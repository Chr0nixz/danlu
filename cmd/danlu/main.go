// danlu：GPU 服务器训练任务管理面板。
package main

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"danlu/internal/api"
	"danlu/internal/auth"
	"danlu/internal/config"
	"danlu/internal/gpu"
	"danlu/internal/notify"
	"danlu/internal/qrutil"
	"danlu/internal/retention"
	"danlu/internal/runner"
	"danlu/internal/scheduler"
	"danlu/internal/snapshot"
	"danlu/internal/store"
	"danlu/internal/system"
	"danlu/internal/terminal"
	"danlu/internal/tlsutil"
	"danlu/internal/totp"
	"danlu/internal/webui"

	"golang.org/x/term"
)

var version = "dev" // 构建时通过 -ldflags "-X main.version=..." 注入

const usage = `danlu — GPU 服务器训练任务管理面板

用法: danlu <命令> [参数]

  init              初始化：生成端口、安全入口、密码和证书；--no-qr 不打印二维码
  serve             启动服务
  info              显示访问地址等信息；--json 输出给桌面客户端读取；--no-qr 不打印二维码
  token             设备令牌（桌面客户端用）：create --name 名字 | list | revoke <id>
  passwd            修改登录密码
  totp              两步验证：enable | disable
  entrance          查看安全入口；--reset 重新生成
  port <端口>       修改端口
  cert --regen      重新生成本地 CA 和证书（公网 IP 变化后使用）
  ca                把本地 CA 证书打印到标准输出，首次接入时用：ssh <服务器> danlu ca > danlu-ca.crt
  install-service   安装为 systemd 用户服务（开机自启、退出 SSH 后继续运行）
  restart           重启服务（正在跑的训练不受影响）
  logs [-n 行数]    查看服务日志（journalctl）
  prune             按保留策略清理任务文件；--dry-run 预览，--older-than 72h 指定时长
  notify            手机推送：setup 配置订阅 | test 发一条测试
  trace <路径>      从 checkpoint 路径反查任务
  doctor            检查服务器状态；--json 输出结构化结果
  update            用当前新程序更新正在运行的 danlu，自动定位并重启服务
  replace <路径>    升级用：用当前这个程序替换 <路径> 上的 danlu，然后重启服务
  version           显示版本

数据目录默认是 ~/.danlu，可以用环境变量 DANLU_HOME 指定。
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	args := os.Args[2:]
	var err error
	switch os.Args[1] {
	case "init":
		err = cmdInit(args)
	case "serve":
		err = cmdServe(args)
	case "info":
		err = cmdInfo(args)
	case "token":
		err = cmdToken(args)
	case "passwd":
		err = cmdPasswd(args)
	case "totp":
		err = cmdTOTP(args)
	case "entrance":
		err = cmdEntrance(args)
	case "port":
		err = cmdPort(args)
	case "cert":
		err = cmdCert(args)
	case "ca":
		err = cmdCA(args)
	case "restart":
		err = cmdRestart()
	case "logs":
		err = cmdLogs(args)
	case "prune":
		err = cmdPrune(args)
	case "notify":
		err = cmdNotify(args)
	case "trace":
		err = cmdTrace(args)
	case "doctor":
		err = cmdDoctor(args)
	case "update":
		err = cmdUpdate(args)
	case "replace":
		err = cmdReplace(args)
	case "install-service":
		err = cmdInstallService()
	case "version", "--version", "-v":
		fmt.Println("danlu", version)
	case "help", "--help", "-h":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "未知命令 %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "错误:", err)
		os.Exit(1)
	}
}

func paths() config.Paths { return config.PathsFor(config.Home()) }

// ---------- 交互输入 ----------

var stdin = bufio.NewReader(os.Stdin)

func ask(label, def string) string {
	if def != "" {
		fmt.Printf("  %s [%s]: ", label, def)
	} else {
		fmt.Printf("  %s: ", label)
	}
	line, _ := stdin.ReadString('\n')
	if line = strings.TrimSpace(line); line == "" {
		return def
	}
	return line
}

func askPassword(label string) string {
	fmt.Printf("  %s: ", label)
	if term.IsTerminal(int(os.Stdin.Fd())) {
		b, _ := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		return strings.TrimSpace(string(b))
	}
	line, _ := stdin.ReadString('\n')
	return strings.TrimSpace(line)
}

// ---------- init ----------

func cmdInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	port := fs.Int("port", 0, "端口（默认随机）")
	ip := fs.String("public-ip", "", "公网 IP（默认自动检测）")
	userName := fs.String("user", "", "用户名（默认 admin）")
	password := fs.String("password", "", "密码（默认随机生成）")
	tlsMode := fs.String("tls", config.TLSLocalCA, "local-ca | off")
	conda := fs.String("conda", "", "conda 安装目录（默认自动检测）")
	yes := fs.Bool("yes", false, "不提问，全部使用默认值")
	force := fs.Bool("force", false, "覆盖已有配置")
	noQR := fs.Bool("no-qr", false, "不打印访问二维码")
	fs.Parse(args)

	p := paths()
	if config.Exists(p) && !*force {
		return fmt.Errorf("已经初始化过了（%s）。查看访问地址用 danlu info；要重新初始化请加 --force", p.Config)
	}
	c := config.Default()
	c.Server.Port = *port
	if c.Server.Port == 0 {
		c.Server.Port = config.RandomPort()
	}
	c.Server.PublicIP = *ip
	if c.Server.PublicIP == "" {
		c.Server.PublicIP = config.DetectIP()
	}
	if *userName != "" {
		c.Auth.Username = *userName
	}
	c.Runtime.CondaBase = *conda
	if c.Runtime.CondaBase == "" {
		c.Runtime.CondaBase = config.DetectConda()
	}
	c.TLS.Mode = *tlsMode
	pw := *password

	if !*yes {
		fmt.Println("初始化 danlu（直接回车使用括号里的默认值）")
		c.Server.Port, _ = strconv.Atoi(ask("面板端口", strconv.Itoa(c.Server.Port)))
		c.Server.PublicIP = ask("公网 IP", c.Server.PublicIP)
		c.Auth.Username = ask("用户名", c.Auth.Username)
		if pw == "" {
			pw = askPassword("密码（回车自动生成）")
		}
		c.Runtime.CondaBase = ask("conda 安装目录（没有就留空）", c.Runtime.CondaBase)
	}
	if net.ParseIP(c.Server.PublicIP) == nil {
		return fmt.Errorf("公网 IP %q 无效", c.Server.PublicIP)
	}
	generated := false
	if pw == "" {
		pw, generated = config.RandomPassword(), true
	} else if len(pw) < 8 {
		return errors.New("密码至少 8 位")
	}
	c.Server.Entrance = config.RandomEntrance()
	if err := c.Validate(); err != nil {
		return err
	}
	if err := p.MkdirAll(); err != nil {
		return err
	}
	if _, err := config.EnsureSecret(p); err != nil {
		return err
	}
	if c.TLS.Mode == config.TLSLocalCA {
		if err := tlsutil.Generate(p.TLS, tlsutil.IPs(c.Server.PublicIP)); err != nil {
			return fmt.Errorf("生成证书失败: %w", err)
		}
	}
	if err := setPassword(p, pw); err != nil {
		return err
	}
	if err := config.Save(p, c); err != nil {
		return err
	}

	fmt.Println()
	fmt.Println("  ✔ 已写入配置", p.Config)
	if c.TLS.Mode == config.TLSLocalCA {
		fmt.Printf("  ✔ 已生成本地 CA 和服务器证书（只对 %s 有效）\n", c.Server.PublicIP)
	}
	fmt.Println("  ──────────────────────────────────────────")
	fmt.Println("  访问地址 ", c.URL())
	fmt.Println("  用户名   ", c.Auth.Username)
	if generated {
		fmt.Println("  密码     ", pw, "（只显示这一次）")
	}
	fmt.Printf("  ⚠ 请在防火墙 / 云安全组放行 TCP %d\n", c.Server.Port)
	if c.TLS.Mode == config.TLSOff {
		fmt.Println("  ⚠ 没有启用 HTTPS，密码会以明文传输，只能用于本地测试")
	}
	fmt.Println("  ──────────────────────────────────────────")
	fmt.Println("  下一步：danlu install-service（或直接 danlu serve 先试试）")
	if c.TLS.Mode == config.TLSLocalCA {
		fmt.Println("  打开面板前，先在自己的电脑上取回并安装 CA：ssh <这台服务器> danlu ca > danlu-ca.crt")
		fmt.Println("  浏览器提示证书不受信任时不要继续登录")
	}
	if !*noQR {
		return printAccessQR(c.URL())
	}
	return nil
}

// ---------- serve ----------

func cmdServe(args []string) error {
	p := paths()
	c, err := config.Load(p)
	if err != nil {
		return err
	}
	if err := p.MkdirAll(); err != nil {
		return err
	}
	// 先锁数据目录再开库：迁移、接管任务和调度都只能由一个实例做
	unlock, err := lockHome(p.Home)
	if err != nil {
		return err
	}
	defer unlock()
	secret, err := config.EnsureSecret(p)
	if err != nil {
		return err
	}
	st, err := store.Open(p.DB)
	if err != nil {
		return err
	}
	defer st.Close()
	if imported, err := auth.ImportPasswordHash(st, c.Auth.PasswordHash); err != nil {
		return err
	} else if imported {
		log.Printf("已把 config.toml 里的密码哈希导入数据库，以后用 danlu passwd 修改密码")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	hostname, _ := os.Hostname()
	notifier := notify.New(st, c.Notify, hostname, c.BaseURL())
	if err := notifier.Initialize(); err != nil {
		log.Printf("初始化手机推送失败，将在后台重试: %v", err)
	}
	notifyDone := make(chan struct{})
	go func() { notifier.Run(ctx); close(notifyDone) }()
	defer func() { stop(); <-notifyDone }()
	outputRoot, err := config.ResolvePath(c.Runtime.OutputRoot)
	if err != nil {
		return err
	}

	mon := gpu.NewMonitor(gpu.NewSource())
	go mon.Run(ctx)
	host := system.NewMonitor(system.NewSource())
	go host.Run(ctx)
	disks := system.NewDiskMonitor(p.Home, outputRoot, func() ([]string, error) { return st.RecentWorkdirs(10) }, uint64(c.Runtime.DiskWarnGB)<<30)
	go disks.Run(ctx)

	run := &runner.Runner{RunRoot: p.Run, LogRoot: p.Logs, EnvRoot: p.Envs, CondaBase: c.Runtime.CondaBase}
	terminals := terminal.NewManager()
	defer terminals.Close()
	mgr := scheduler.New(st, mon, run, scheduler.Options{
		SnapshotDir: p.Snapshots,
		RunRoot:     p.Run,
		OutputRoot:  outputRoot,
		Snapshot: snapshot.Options{
			MaxFile:  int64(c.Runtime.SnapshotMaxFileMB) << 20,
			MaxTotal: int64(c.Runtime.SnapshotMaxTotalMB) << 20,
		},
		SnapshotTimeout: 10 * time.Second,
		KillGrace:       c.Runtime.KillGrace.Duration,
		FailFast:        c.Runtime.FailFast.Duration,
		Disks:           disks,
		DiskMinBytes:    uint64(c.Runtime.DiskMinGB) << 30,
		ManifestTimeout: 10 * time.Second,
	})
	// 先占住端口再接管任务：端口被占用时直接退出，不碰任何任务
	ln, err := net.Listen("tcp", net.JoinHostPort(c.Server.Bind, strconv.Itoa(c.Server.Port)))
	if err != nil {
		return err
	}
	defer ln.Close()
	mgr.Recover()
	// Run 退出前会等正在启动的任务都落库。数据库要在它之后关：defer 按相反的顺序执行
	mgrDone := make(chan struct{})
	go func() { mgr.Run(ctx); close(mgrDone) }()
	defer func() { stop(); <-mgrDone }()
	pruner := &retention.Pruner{Store: st, Paths: p, OutputRoot: outputRoot, Options: retention.Options{
		KeepDays: c.Retention.KeepDays, MaxBytes: c.Retention.MaxTotalGB << 30,
	}}
	pruneDone := make(chan struct{})
	go func() { pruner.Run(ctx); close(pruneDone) }()
	defer func() { stop(); <-pruneDone }()

	au := auth.New(st, auth.Options{
		Entrance: c.Server.Entrance, Secret: secret, Secure: c.TLS.Mode != config.TLSOff,
		Username: c.Auth.Username, SessionTTL: c.Auth.SessionTTL.Duration,
		MaxFailures: c.Auth.MaxFailures, Lockout: c.Auth.Lockout.Duration,
	})
	srv := &api.Server{Cfg: c, Paths: p, Store: st, Mgr: mgr, GPUs: mon, System: host, Auth: au, Runner: run, Terminal: terminals,
		Static: webui.FS(), Version: version, Disks: disks, Notify: notifier}

	// 请求的 context 都从 base 派生：停止时先取消它，日志 SSE 这类长连接马上退出
	base, cancelBase := context.WithCancel(context.Background())
	defer cancelBase()
	httpSrv := &http.Server{
		Handler:           srv.Handler(),
		BaseContext:       func(net.Listener) context.Context { return base },
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second, // 请求体最多 1MB
		WriteTimeout:      30 * time.Second, // 日志 SSE 每次写之前续期，下载时清掉（见 api）
		IdleTimeout:       2 * time.Minute,  // 面板直接开在公网上，空闲连接不能一直挂着
		MaxHeaderBytes:    64 << 10,
		// 公网上扫描器很多，TLS 握手失败的日志直接丢掉
		ErrorLog: log.New(filteredWriter{}, "", log.LstdFlags),
	}

	log.Printf("danlu %s 启动，数据目录 %s", version, p.Home)
	log.Printf("访问地址 %s", c.URL())
	serve := func() error { return httpSrv.Serve(ln) }
	switch c.TLS.Mode {
	case config.TLSLocalCA, config.TLSFile:
		certFile, keyFile := c.TLS.CertFile, c.TLS.KeyFile
		var renew func() error // file 模式：证书是用户自己管的，只在文件变了时重新加载
		if c.TLS.Mode == config.TLSLocalCA {
			f, ips := tlsutil.FilesIn(p.TLS), tlsutil.IPs(c.Server.PublicIP)
			certFile, keyFile = f.Cert, f.Key
			renew = func() error { return tlsutil.EnsureServer(p.TLS, ips) }
		}
		// 证书放在内存里，每次握手时取当前那张：续期和 cert --regen 都不用重启
		certs, err := tlsutil.NewCertManager(certFile, keyFile, renew)
		if err != nil {
			return err
		}
		go certs.Run(ctx)
		httpSrv.TLSConfig = &tls.Config{GetCertificate: certs.GetCertificate}
		serve = func() error { return httpSrv.ServeTLS(ln, "", "") }
	default:
		log.Printf("⚠ 没有启用 HTTPS，只能用于本地测试")
	}
	errc := make(chan error, 1)
	go func() { errc <- serve() }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	log.Printf("正在停止 danlu")
	cancelBase()
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(sctx) // 等正在处理的请求（比如提交任务）完成
	<-mgrDone                  // 等调度循环和正在启动的任务都落库
	log.Printf("danlu 已停止（正在运行的训练任务不受影响）")
	return nil
}

type filteredWriter struct{}

func (filteredWriter) Write(b []byte) (int, error) {
	if strings.Contains(string(b), "TLS handshake error") {
		return len(b), nil
	}
	return os.Stderr.Write(b)
}

// ---------- 管理命令 ----------

func loadForEdit() (config.Paths, *config.Config, error) {
	p := paths()
	c, err := config.Load(p)
	return p, c, err
}

const restartHint = "重启服务后生效：systemctl --user restart danlu"

func cmdInfo(args []string) error {
	fs := flag.NewFlagSet("info", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "以 JSON 输出（桌面客户端通过 SSH 读取）")
	noQR := fs.Bool("no-qr", false, "不打印访问二维码")
	fs.Parse(args)
	p, c, err := loadForEdit()
	if err != nil {
		return err
	}
	if *asJSON {
		return printInfoJSON(p, c)
	}
	fmt.Println("访问地址 ", c.URL())
	fmt.Println("用户名   ", c.Auth.Username, "（密码不会再显示，忘记了用 danlu passwd 重设）")
	fmt.Println("HTTPS    ", c.TLS.Mode)
	if c.TLS.Mode == config.TLSLocalCA {
		if fp, err := tlsutil.Fingerprint(tlsutil.FilesIn(p.TLS).CACert); err == nil {
			fmt.Println("CA 指纹  ", fp)
		}
	}
	fmt.Println("conda    ", orDash(c.Runtime.CondaBase))
	outputRoot, _ := config.ResolvePath(c.Runtime.OutputRoot)
	fmt.Println("输出目录 ", outputRoot)
	fmt.Println("数据目录 ", p.Home)
	if !*noQR {
		return printAccessQR(c.URL())
	}
	return nil
}

func printAccessQR(address string) error {
	qr, err := qrutil.Terminal(address)
	if err != nil {
		return fmt.Errorf("生成访问二维码失败: %w", err)
	}
	fmt.Println("手机扫码打开面板：")
	fmt.Print(qr)
	return nil
}

// printInfoJSON 给桌面客户端用：拿到地址和 CA 证书后，客户端只信任这张 CA。
// 输出走 SSH，SSH 已经证明了身份，所以 CA 证书本身可以直接信任，不必另外核对指纹。
func printInfoJSON(p config.Paths, c *config.Config) error {
	exe, _ := os.Executable()
	host, _ := os.Hostname()
	info := map[string]any{
		"version":     version,
		"api_version": api.APIVersion,
		"hostname":    host,
		"base_url":    c.BaseURL(),
		"url":         c.URL(),
		"tls_mode":    c.TLS.Mode,
		"username":    c.Auth.Username,
		"data_dir":    p.Home,
		"exe":         exe,
	}
	if c.TLS.Mode == config.TLSLocalCA {
		f := tlsutil.FilesIn(p.TLS).CACert
		pem, err := os.ReadFile(f)
		if err != nil {
			return fmt.Errorf("读取 CA 证书失败: %w", err)
		}
		fp, err := tlsutil.Fingerprint(f)
		if err != nil {
			return err
		}
		info["ca_pem"], info["ca_fingerprint"] = string(pem), fp
	}
	return printJSON(info)
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func cmdToken(args []string) error {
	const tokenUsage = "用法: danlu token create --name 名字 [--json] | list [--json] | revoke <id>"
	if len(args) == 0 {
		return errors.New(tokenUsage)
	}
	fs := flag.NewFlagSet("token "+args[0], flag.ExitOnError)
	name := fs.String("name", "", "令牌名称，一般写电脑名")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	fs.Parse(args[1:])

	st, err := store.Open(paths().DB)
	if err != nil {
		return err
	}
	defer st.Close()
	switch args[0] {
	case "create":
		if strings.TrimSpace(*name) == "" {
			return errors.New("请用 --name 写明是哪台电脑的令牌，方便以后吊销")
		}
		id, tok, err := auth.NewDeviceToken(st, strings.TrimSpace(*name))
		if err != nil {
			return err
		}
		st.Audit("cli", "token_create", fmt.Sprintf("#%d %s", id, *name))
		if *asJSON {
			return printJSON(map[string]any{"id": id, "name": *name, "token": tok})
		}
		fmt.Printf("已创建设备令牌 #%d（%s），只显示这一次：\n%s\n吊销：danlu token revoke %d，或在面板「设置」页操作\n", id, *name, tok, id)
	case "list":
		toks, err := st.ListDeviceTokens()
		if err != nil {
			return err
		}
		if *asJSON {
			return printJSON(map[string]any{"tokens": toks})
		}
		if len(toks) == 0 {
			fmt.Println("还没有设备令牌")
		}
		for _, t := range toks {
			used := "从未使用"
			if t.LastUsedAt != nil {
				used = "最后使用 " + time.Unix(*t.LastUsedAt, 0).Format("2006-01-02 15:04") + " " + t.LastIP
			}
			fmt.Printf("#%-4d %-20s 创建于 %s  %s\n", t.ID, t.Name, time.Unix(t.CreatedAt, 0).Format("2006-01-02 15:04"), used)
		}
	case "revoke":
		if fs.NArg() != 1 {
			return errors.New(tokenUsage)
		}
		id, err := strconv.ParseInt(fs.Arg(0), 10, 64)
		if err != nil {
			return fmt.Errorf("令牌 id %q 无效", fs.Arg(0))
		}
		name, err := st.RevokeDeviceToken(id)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("没有 #%d 这个令牌", id)
		}
		if err != nil {
			return err
		}
		st.Audit("cli", "token_revoke", fmt.Sprintf("#%d %s", id, name))
		fmt.Printf("已吊销 #%d（%s），立即生效\n", id, name)
	default:
		return errors.New(tokenUsage)
	}
	return nil
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func cmdPasswd(args []string) error {
	fs := flag.NewFlagSet("passwd", flag.ExitOnError)
	pw := fs.String("password", "", "新密码（不填则交互输入）")
	// 桌面客户端经 SSH 改密码时用：密码不出现在命令行里，别的用户 ps 看不到
	fromStdin := fs.Bool("password-stdin", false, "从标准输入读一行作为新密码")
	fs.Parse(args)
	p, c, err := loadForEdit()
	if err != nil {
		return err
	}
	if *fromStdin {
		line, _ := stdin.ReadString('\n')
		*pw = strings.TrimRight(line, "\r\n")
	} else if *pw == "" {
		*pw = askPassword("新密码（至少 8 位）")
		if again := askPassword("再输入一次"); again != *pw {
			return errors.New("两次输入不一致")
		}
	}
	if len(*pw) < 8 {
		return errors.New("密码至少 8 位")
	}
	if err := setPassword(p, *pw); err != nil {
		return err
	}
	// 旧版本把哈希放在配置文件里，删掉免得留着两份
	if c.Auth.PasswordHash != "" {
		c.Auth.PasswordHash = ""
		if err := config.Save(p, c); err != nil {
			return err
		}
	}
	fmt.Println("密码已修改，立即生效，所有已登录的会话已注销")
	return nil
}

func cmdTOTP(args []string) error {
	if len(args) != 1 || (args[0] != "enable" && args[0] != "disable") {
		return errors.New("用法: danlu totp enable | danlu totp disable")
	}
	p := paths()
	c, err := config.Load(p)
	if err != nil {
		return err
	}
	st, err := store.Open(p.DB)
	if err != nil {
		return err
	}
	defer st.Close()
	enabled, err := auth.TOTPEnabled(st)
	if err != nil {
		return err
	}
	if args[0] == "disable" {
		if !enabled {
			fmt.Println("两步验证尚未开启")
			return nil
		}
		if err := auth.DisableTOTP(st); err != nil {
			return err
		}
		st.Audit("local", "totp_disable", "")
		fmt.Println("✔ 已关闭两步验证")
		return nil
	}
	if enabled {
		return errors.New("两步验证已经开启；如需更换，请先执行 danlu totp disable")
	}
	secret, err := totp.GenerateSecret()
	if err != nil {
		return err
	}
	uri := totp.ProvisioningURI(secret, c.Auth.Username)
	qr, err := qrutil.Terminal(uri)
	if err != nil {
		return fmt.Errorf("生成设置二维码失败: %w", err)
	}
	fmt.Println("用验证器 App 扫描二维码，或手动输入密钥：")
	fmt.Print(qr)
	fmt.Println("密钥：", secret)
	fmt.Print("输入验证器显示的 6 位验证码以确认启用: ")
	line, _ := stdin.ReadString('\n')
	step, ok := totp.Verify(secret, strings.TrimSpace(line), time.Now())
	if !ok {
		return errors.New("验证码无效；两步验证未开启，请重新执行 danlu totp enable")
	}
	if err := auth.ConfigureTOTP(st, secret, step); err != nil {
		return err
	}
	st.Audit("local", "totp_enable", "")
	fmt.Println("✔ 已开启两步验证。登录时需要密码和验证器验证码。")
	return nil
}

// setPassword 把密码存进数据库，并让所有已登录的会话失效。服务正在运行时也立即生效。
func setPassword(p config.Paths, pw string) error {
	st, err := store.Open(p.DB)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := auth.SetPassword(st, pw); err != nil {
		return err
	}
	return st.DeleteSessionsExcept("")
}

func cmdEntrance(args []string) error {
	fs := flag.NewFlagSet("entrance", flag.ExitOnError)
	reset := fs.Bool("reset", false, "重新生成安全入口")
	fs.Parse(args)
	p, c, err := loadForEdit()
	if err != nil {
		return err
	}
	if *reset {
		c.Server.Entrance = config.RandomEntrance()
		if err := config.Save(p, c); err != nil {
			return err
		}
		fmt.Println("已生成新的安全入口，旧入口失效。" + restartHint)
	}
	fmt.Println("访问地址 ", c.URL())
	return nil
}

func cmdPort(args []string) error {
	if len(args) != 1 {
		return errors.New("用法: danlu port <端口>")
	}
	port, err := strconv.Atoi(args[0])
	if err != nil {
		return err
	}
	p, c, err := loadForEdit()
	if err != nil {
		return err
	}
	c.Server.Port = port
	if err := c.Validate(); err != nil {
		return err
	}
	if err := config.Save(p, c); err != nil {
		return err
	}
	fmt.Printf("端口已改为 %d，记得在防火墙放行。%s\n访问地址  %s\n", port, restartHint, c.URL())
	return nil
}

func cmdCert(args []string) error {
	fs := flag.NewFlagSet("cert", flag.ExitOnError)
	regen := fs.Bool("regen", false, "重新生成本地 CA 和服务器证书")
	ip := fs.String("public-ip", "", "新的公网 IP（默认沿用配置）")
	fs.Parse(args)
	if !*regen {
		return errors.New("用法: danlu cert --regen [--public-ip IP]")
	}
	p, c, err := loadForEdit()
	if err != nil {
		return err
	}
	if *ip != "" {
		if net.ParseIP(*ip) == nil {
			return fmt.Errorf("IP %q 无效", *ip)
		}
		c.Server.PublicIP = *ip
	}
	if err := tlsutil.Generate(p.TLS, tlsutil.IPs(c.Server.PublicIP)); err != nil {
		return err
	}
	if err := config.Save(p, c); err != nil {
		return err
	}
	fmt.Println("已重新生成 CA 和证书。之前安装过 CA 的设备需要删掉旧 CA，用 ssh <这台服务器> danlu ca > danlu-ca.crt 取回新的再安装。" + restartHint)
	return nil
}

// cmdCA 把本地 CA 证书打印到标准输出。首次接入和换 CA 时经 SSH 取回：SSH 已经核实了服务器身份，
// 装好 CA 再打开面板，就不用跳过浏览器的证书警告，密码不会交给冒充面板的人。
// 指纹打印到标准错误，不混进证书文件。
func cmdCA(args []string) error {
	fs := flag.NewFlagSet("ca", flag.ExitOnError)
	fs.Parse(args)
	p, c, err := loadForEdit()
	if err != nil {
		return err
	}
	if c.TLS.Mode != config.TLSLocalCA {
		return fmt.Errorf("HTTPS 模式是 %s，没有使用本地 CA", c.TLS.Mode)
	}
	f := tlsutil.FilesIn(p.TLS).CACert
	pem, err := os.ReadFile(f)
	if err != nil {
		return fmt.Errorf("读取 CA 证书失败: %w", err)
	}
	fp, err := tlsutil.Fingerprint(f)
	if err != nil {
		return err
	}
	if _, err := os.Stdout.Write(pem); err != nil {
		return err
	}
	fmt.Fprintln(os.Stderr, "CA 指纹（SHA-256）", fp)
	return nil
}

// ---------- 服务管理（桌面客户端经 SSH 调用） ----------

func cmdRestart() error {
	if err := restartService(); err != nil {
		return err
	}
	fmt.Println("已重启 danlu（正在跑的训练任务不受影响）")
	return nil
}

func restartService() error {
	if out, err := exec.Command("systemctl", "--user", "restart", "danlu").CombinedOutput(); err != nil {
		return fmt.Errorf("systemctl --user restart danlu 失败: %v\n%s", err, out)
	}
	return nil
}

func cmdLogs(args []string) error {
	fs := flag.NewFlagSet("logs", flag.ExitOnError)
	n := fs.Int("n", 200, "显示最后多少行")
	fs.Parse(args)
	cmd := exec.Command("journalctl", "--user", "-u", "danlu", "-n", strconv.Itoa(*n), "--no-pager", "-o", "short-iso")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// cmdUpdate 由上传的新程序执行，从运行中的用户服务找到旧程序的位置。
func cmdUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	noRestart := fs.Bool("no-restart", false, "只替换，不重启服务")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	fs.Parse(args)
	if fs.NArg() != 0 {
		return errors.New("用法: ./danlu.new update [--no-restart] [--json]")
	}
	target, err := installedExecutable()
	if err != nil {
		return err
	}
	return replaceExecutable(target, *noRestart, *asJSON)
}

// cmdReplace 由新版本的程序执行：把自己原子地换到 <路径> 上，再重启服务。
// 客户端先把新程序传成 <路径>.new，再执行 "<路径>.new replace <路径>"。直接覆盖正在运行的程序会报 Text file busy，改名不会。
func cmdReplace(args []string) error {
	fs := flag.NewFlagSet("replace", flag.ExitOnError)
	noRestart := fs.Bool("no-restart", false, "只替换，不重启服务")
	asJSON := fs.Bool("json", false, "以 JSON 输出")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("用法: danlu replace [--no-restart] <要替换的 danlu 路径>")
	}
	return replaceExecutable(fs.Arg(0), *noRestart, *asJSON)
}

func replaceExecutable(path string, noRestart, asJSON bool) error {
	target, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// PATH 中的 danlu 可以是软链接，升级必须替换服务实际使用的文件。
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	self, err := os.Executable()
	if err != nil {
		return err
	}
	self, _ = filepath.EvalSymlinks(self)
	if self == target {
		return errors.New("新程序和要替换的是同一个文件")
	}
	if err := keepPrevious(target); err != nil {
		return fmt.Errorf("备份旧程序失败: %w", err)
	}
	if filepath.Dir(self) == filepath.Dir(target) {
		err = os.Rename(self, target)
	} else {
		err = copyExecutable(self, target)
	}
	if err != nil {
		return fmt.Errorf("替换 %s 失败: %w", target, err)
	}
	setupCommandPath(target)
	restarted := false
	if !noRestart {
		if err := restartService(); err != nil {
			return err
		}
		restarted = true
	}
	if asJSON {
		return printJSON(map[string]any{"version": version, "path": target, "restarted": restarted})
	}
	fmt.Printf("已把 %s 换成 %s\n", target, version)
	return nil
}

// keepPrevious 把要被替换的旧程序留一份 <路径>.prev，回滚时换回来（README「升级与回滚」）。
// 优先用硬链接：不用复制，替换后它还指向旧文件。
func keepPrevious(target string) error {
	if _, err := os.Stat(target); errors.Is(err, os.ErrNotExist) {
		return nil
	}
	prev := target + ".prev"
	_ = os.Remove(prev)
	if os.Link(target, prev) == nil {
		return nil
	}
	return copyExecutable(target, prev)
}

// copyExecutable 先复制到同目录的临时文件，再改名过去，保证替换是原子的。
func copyExecutable(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := dst + ".tmp"
	if err := os.WriteFile(tmp, b, 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

const serviceUnit = `[Unit]
Description=danlu GPU 训练任务管理面板
After=network-online.target

[Service]
ExecStart=%s serve
Environment=DANLU_HOME=%s
Environment=PATH=%s
Restart=on-failure
RestartSec=3
# 关键：停止或重启 danlu 时只结束 danlu 自己，不要连带杀掉正在跑的训练进程
KillMode=process

[Install]
WantedBy=default.target
`

func cmdInstallService() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, _ = filepath.EvalSymlinks(exe)
	p := paths()
	if !config.Exists(p) {
		return errors.New("请先运行 danlu init")
	}
	setupCommandPath(exe)
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".config", "systemd", "user")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	unit := filepath.Join(dir, "danlu.service")
	// 把当前 PATH 写进服务里，训练脚本才能找到你平时用的命令
	content := fmt.Sprintf(serviceUnit, exe, p.Home, os.Getenv("PATH"))
	if err := os.WriteFile(unit, []byte(content), 0o644); err != nil {
		return err
	}
	fmt.Println("✔ 已写入", unit)
	steps := [][]string{
		{"systemctl", "--user", "daemon-reload"},
		{"systemctl", "--user", "enable", "--now", "danlu"},
	}
	for _, s := range steps {
		if out, err := exec.Command(s[0], s[1:]...).CombinedOutput(); err != nil {
			return fmt.Errorf("%s 失败: %v\n%s", strings.Join(s, " "), err, out)
		}
	}
	fmt.Println("✔ 服务已启动：systemctl --user status danlu")
	if u, err := user.Current(); err == nil {
		if out, err := exec.Command("loginctl", "enable-linger", u.Username).CombinedOutput(); err != nil {
			fmt.Printf("⚠ loginctl enable-linger 失败（%v %s）。\n  不开启的话，退出 SSH 后服务可能被停止。请执行: sudo loginctl enable-linger %s\n",
				err, strings.TrimSpace(string(out)), u.Username)
		} else {
			fmt.Println("✔ 已开启 linger：退出 SSH 后服务继续运行")
		}
	}
	fmt.Println("查看日志：journalctl --user -u danlu -f")
	return nil
}
