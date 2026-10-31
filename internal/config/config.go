// Package config 负责 danlu 的配置文件、数据目录和初始化时用到的各种默认值。
package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Duration 让 TOML 里可以写 "30s"、"168h" 这样的时长。
type Duration struct{ time.Duration }

func (d *Duration) UnmarshalText(b []byte) error {
	v, err := time.ParseDuration(string(b))
	if err != nil {
		return err
	}
	d.Duration = v
	return nil
}

func (d Duration) MarshalText() ([]byte, error) { return []byte(d.String()), nil }

type Config struct {
	Server  Server  `toml:"server"`
	TLS     TLS     `toml:"tls"`
	Auth    Auth    `toml:"auth"`
	Runtime Runtime `toml:"runtime"`
}

type Server struct {
	Bind     string `toml:"bind"`
	Port     int    `toml:"port"`
	Entrance string `toml:"entrance"`
	PublicIP string `toml:"public_ip"`
}

// TLS.Mode: local-ca（默认，本地 CA 签发）| file（自备证书）| off（仅限本地测试）
type TLS struct {
	Mode     string `toml:"mode"`
	CertFile string `toml:"cert_file,omitempty"`
	KeyFile  string `toml:"key_file,omitempty"`
}

type Auth struct {
	Username string `toml:"username"`
	// PasswordHash 是旧版本放密码哈希的地方。现在密码存在数据库里（danlu passwd 修改），
	// 这里只在第一次启动新版本时导入一次，之后 danlu passwd 会把它删掉。
	PasswordHash string   `toml:"password_hash,omitempty"`
	SessionTTL   Duration `toml:"session_ttl"`
	MaxFailures  int      `toml:"max_failures"`
	Lockout      Duration `toml:"lockout"`
}

type Runtime struct {
	CondaBase          string   `toml:"conda_base"`
	KillGrace          Duration `toml:"kill_grace"`
	SnapshotMaxFileMB  int      `toml:"snapshot_max_file_mb"`
	SnapshotMaxTotalMB int      `toml:"snapshot_max_total_mb"`
}

const (
	TLSLocalCA = "local-ca"
	TLSFile    = "file"
	TLSOff     = "off"
)

func Default() *Config {
	return &Config{
		Server: Server{Bind: "0.0.0.0"},
		TLS:    TLS{Mode: TLSLocalCA},
		Auth: Auth{
			Username:    "admin",
			SessionTTL:  Duration{7 * 24 * time.Hour},
			MaxFailures: 5,
			Lockout:     Duration{15 * time.Minute},
		},
		Runtime: Runtime{
			KillGrace:          Duration{30 * time.Second},
			SnapshotMaxFileMB:  1,
			SnapshotMaxTotalMB: 50,
		},
	}
}

// Home 返回数据目录：优先 $DANLU_HOME，否则 ~/.danlu。
func Home() string {
	if h := os.Getenv("DANLU_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".danlu"
	}
	return filepath.Join(home, ".danlu")
}

type Paths struct {
	Home, Config, Secret, DB, Logs, Snapshots, Run, TLS string
}

func PathsFor(home string) Paths {
	return Paths{
		Home:      home,
		Config:    filepath.Join(home, "config.toml"),
		Secret:    filepath.Join(home, "secret.key"),
		DB:        filepath.Join(home, "danlu.db"),
		Logs:      filepath.Join(home, "logs"),
		Snapshots: filepath.Join(home, "snapshots"),
		Run:       filepath.Join(home, "run"),
		TLS:       filepath.Join(home, "tls"),
	}
}

func (p Paths) MkdirAll() error {
	for _, d := range []string{p.Home, p.Logs, p.Snapshots, p.Run, p.TLS} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return err
		}
	}
	return nil
}

func Exists(p Paths) bool {
	_, err := os.Stat(p.Config)
	return err == nil
}

func Load(p Paths) (*Config, error) {
	c := Default()
	if _, err := toml.DecodeFile(p.Config, c); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("还没有初始化，请先运行 danlu init（数据目录 %s）", p.Home)
		}
		return nil, fmt.Errorf("读取配置 %s 失败: %w", p.Config, err)
	}
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("配置 %s 有误: %w", p.Config, err)
	}
	return c, nil
}

func Save(p Paths, c *Config) error {
	if err := p.MkdirAll(); err != nil {
		return err
	}
	var sb strings.Builder
	sb.WriteString("# danlu 配置文件。修改后执行 systemctl --user restart danlu 生效。\n\n")
	if err := toml.NewEncoder(&sb).Encode(c); err != nil {
		return err
	}
	tmp := p.Config + ".tmp"
	if err := os.WriteFile(tmp, []byte(sb.String()), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.Config)
}

func (c *Config) Validate() error {
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("端口 %d 无效", c.Server.Port)
	}
	if !ValidEntrance(c.Server.Entrance) {
		return fmt.Errorf("安全入口 %q 无效（需要 6–64 位小写字母或数字）", c.Server.Entrance)
	}
	switch c.TLS.Mode {
	case TLSLocalCA, TLSOff:
	case TLSFile:
		if c.TLS.CertFile == "" || c.TLS.KeyFile == "" {
			return errors.New("tls.mode = file 时必须填写 cert_file 和 key_file")
		}
	default:
		return fmt.Errorf("未知的 tls.mode %q", c.TLS.Mode)
	}
	if c.Auth.Username == "" {
		return errors.New("缺少用户名")
	}
	if c.Runtime.SnapshotMaxFileMB <= 0 || c.Runtime.SnapshotMaxTotalMB <= 0 {
		return errors.New("快照大小上限必须大于 0")
	}
	return nil
}

func (c *Config) Scheme() string {
	if c.TLS.Mode == TLSOff {
		return "http"
	}
	return "https"
}

// BaseURL 是不带安全入口的地址，桌面客户端用设备令牌访问时用它。
func (c *Config) BaseURL() string {
	host := c.Server.PublicIP
	if ip := net.ParseIP(host); ip != nil && ip.To4() == nil {
		host = "[" + host + "]"
	}
	return fmt.Sprintf("%s://%s:%d", c.Scheme(), host, c.Server.Port)
}

// URL 是带安全入口的完整访问地址。
func (c *Config) URL() string { return c.BaseURL() + "/" + c.Server.Entrance }

func ValidEntrance(s string) bool {
	if len(s) < 6 || len(s) > 64 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9') {
			return false
		}
	}
	// 不能和内部路径撞名
	return s != "api" && s != "assets" && s != "login"
}

const (
	lowerDigits = "abcdefghijklmnopqrstuvwxyz0123456789"
	// 去掉了容易看混的 0/O、1/l/I
	passwordChars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
)

func randomString(n int, alphabet string) string {
	b := make([]byte, n)
	max := big.NewInt(int64(len(alphabet)))
	for i := range b {
		v, err := rand.Int(rand.Reader, max)
		if err != nil {
			panic(err)
		}
		b[i] = alphabet[v.Int64()]
	}
	return string(b)
}

func RandomEntrance() string { return randomString(12, lowerDigits) }
func RandomPassword() string { return randomString(16, passwordChars) }

// RandomPort 在 20000–60000 之间挑一个当前空闲的端口。
func RandomPort() int {
	for i := 0; i < 50; i++ {
		v, _ := rand.Int(rand.Reader, big.NewInt(40000))
		port := 20000 + int(v.Int64())
		if l, err := net.Listen("tcp", fmt.Sprintf(":%d", port)); err == nil {
			l.Close()
			return port
		}
	}
	return 28457
}

func EnsureSecret(p Paths) ([]byte, error) {
	if b, err := os.ReadFile(p.Secret); err == nil {
		return hex.DecodeString(strings.TrimSpace(string(b)))
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}
	if err := os.WriteFile(p.Secret, []byte(hex.EncodeToString(key)), 0o600); err != nil {
		return nil, err
	}
	return key, nil
}

// DetectIP 只看本机网卡：优先公网 IPv4，其次内网 IPv4，不向外部服务查询。
func DetectIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "127.0.0.1"
	}
	var private string
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipn.IP.To4()
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if !ip.IsPrivate() && !isCGNAT(ip) {
			return ip.String()
		}
		if private == "" {
			private = ip.String()
		}
	}
	if private != "" {
		return private
	}
	return "127.0.0.1"
}

func isCGNAT(ip net.IP) bool { return ip[0] == 100 && ip[1]&0xc0 == 64 }

// DetectConda 找到包含 etc/profile.d/conda.sh 的 conda 安装目录。
func DetectConda() string {
	var cands []string
	if exe := os.Getenv("CONDA_EXE"); exe != "" {
		cands = append(cands, filepath.Dir(filepath.Dir(exe)))
	}
	if home, err := os.UserHomeDir(); err == nil {
		for _, n := range []string{"miniconda3", "anaconda3", "miniforge3", "mambaforge", "micromamba"} {
			cands = append(cands, filepath.Join(home, n))
		}
	}
	cands = append(cands, "/opt/conda", "/opt/miniconda3", "/opt/anaconda3")
	for _, c := range cands {
		if _, err := os.Stat(filepath.Join(c, "etc", "profile.d", "conda.sh")); err == nil {
			return c
		}
	}
	return ""
}
