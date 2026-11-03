package doctor

import (
	"context"
	"net"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"danlu/internal/config"
	"danlu/internal/tlsutil"
)

func TestParseProperties(t *testing.T) {
	got := parseProperties("LoadState=loaded\nActiveState=active\nKillMode=process\nRestart=on-failure\n")
	for key, want := range map[string]string{"LoadState": "loaded", "ActiveState": "active", "KillMode": "process", "Restart": "on-failure"} {
		if got[key] != want {
			t.Errorf("%s = %q, want %q", key, got[key], want)
		}
	}
}

func TestReleaseVersion(t *testing.T) {
	for version, want := range map[string]bool{
		"v0.2.0": true, "v0.2.0-rc1": true,
		"": false, "dev": false, "dev-20261103-1200": false, "dev-v0.2.0-3-gabc1234": false,
		"v0.2.0-dirty": false,
	} {
		if got := releaseVersion(version); got != want {
			t.Errorf("releaseVersion(%q) = %v, want %v", version, got, want)
		}
	}
}

// testSetup 准备一份不依赖本机 conda、磁盘阈值和端口的配置；端口指向一个正在监听的临时端口。
func testSetup(t *testing.T) (*config.Config, config.Paths) {
	t.Helper()
	t.Setenv("DANLU_FAKE_GPUS", "2")
	t.Setenv("DANLU_FAKE_BUSY", "")
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	c := config.Default()
	c.Server.Bind = "127.0.0.1"
	c.Server.Port = ln.Addr().(*net.TCPAddr).Port
	c.Server.PublicIP = "127.0.0.1"
	c.Runtime.CondaBase = filepath.Join(t.TempDir(), "missing-conda")
	c.Runtime.OutputRoot = t.TempDir()
	c.Runtime.DiskWarnGB, c.Runtime.DiskMinGB = 0, 0
	return c, config.PathsFor(t.TempDir())
}

func byID(t *testing.T, r Report) map[string]Check {
	t.Helper()
	checks := map[string]Check{}
	for _, c := range r.Checks {
		if c.ID == "" || c.Title == "" || (c.Level != "ok" && c.Level != "warn" && c.Level != "fail") {
			t.Errorf("检查项不完整: %+v", c)
		}
		if _, dup := checks[c.ID]; dup {
			t.Errorf("检查项 id 重复: %s", c.ID)
		}
		checks[c.ID] = c
	}
	return checks
}

func TestRunWithoutTLS(t *testing.T) {
	c, p := testSetup(t)
	c.TLS.Mode = config.TLSOff
	checks := byID(t, Run(context.Background(), c, p, "dev-test", 1))
	for _, id := range []string{"service_unit", "service_active", "service_kill_mode", "service_restart", "linger", "time_sync"} {
		if _, ok := checks[id]; !ok {
			t.Errorf("缺少平台检查 %s", id)
		}
	}
	expect := map[string]string{"gpu": "ok", "conda": "fail", "server_cert": "warn", "port": "ok", "version": "warn"}
	if runtime.GOOS == "linux" { // 磁盘采集只在 Linux 上实现
		expect["data_disk"], expect["output_disk"] = "ok", "ok"
	}
	for id, want := range expect {
		if got := checks[id]; got.Level != want {
			t.Errorf("%s = %s（%s），want %s", id, got.Level, got.Detail, want)
		}
	}
	if !strings.Contains(checks["gpu"].Detail, "2 张 GPU") {
		t.Errorf("GPU 检查应报告卡数: %q", checks["gpu"].Detail)
	}
	if checks["version"].Fix == "" || !strings.Contains(checks["version"].Detail, "API 1") {
		t.Errorf("开发版本应提示修复并带上 API 版本: %+v", checks["version"])
	}
}

func TestRunLocalCA(t *testing.T) {
	c, p := testSetup(t)
	c.TLS.Mode = config.TLSLocalCA
	if err := tlsutil.Generate(p.TLS, tlsutil.IPs("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	checks := byID(t, Run(context.Background(), c, p, "v0.2.0", 1))
	for id, want := range map[string]string{"server_cert": "ok", "ca_cert": "ok", "cert_ip": "ok", "version": "ok"} {
		if got := checks[id]; got.Level != want {
			t.Errorf("%s = %s（%s），want %s", id, got.Level, got.Detail, want)
		}
	}

	// 公网 IP 变了而证书没重新生成：提示 cert --regen
	c.Server.PublicIP = "203.0.113.7"
	checks = byID(t, Run(context.Background(), c, p, "v0.2.0", 1))
	if got := checks["cert_ip"]; got.Level != "fail" || !strings.Contains(got.Fix, "cert --regen") {
		t.Errorf("证书不含 public_ip 时应失败并提示重新生成: %+v", got)
	}
}
