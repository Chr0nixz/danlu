package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

func TestGenerateVerifies(t *testing.T) {
	dir := t.TempDir()
	ips := IPs("203.0.113.10")
	if err := Generate(dir, ips); err != nil {
		t.Fatal(err)
	}
	f := FilesIn(dir)
	ca, _ := loadCert(f.CACert)
	pool := x509.NewCertPool()
	pool.AddCert(ca)

	chain, _ := os.ReadFile(f.Cert)
	blk, _ := pem.Decode(chain)
	leaf, err := x509.ParseCertificate(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	for _, host := range []string{"203.0.113.10", "127.0.0.1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: host}); err != nil {
			t.Errorf("%s 应该校验通过: %v", host, err)
		}
	}
	if caPermits(ca, net.ParseIP("198.51.100.1")) {
		t.Error("CA 不应允许其他 IP")
	}
}

func TestEnsureServerRejectsNewIP(t *testing.T) {
	dir := t.TempDir()
	if err := Generate(dir, IPs("203.0.113.10")); err != nil {
		t.Fatal(err)
	}
	if err := EnsureServer(dir, IPs("203.0.113.10")); err != nil {
		t.Fatalf("同一 IP 不应报错: %v", err)
	}
	if err := EnsureServer(dir, IPs("198.51.100.1")); err == nil {
		t.Fatal("IP 变化后应提示重新生成 CA")
	}
}

// ca.key 读不到（挪去离线保存、存储偶尔出错）不能悄悄换掉整个 CA：所有设备都会不再信任这台服务器。
// 服务器证书还新鲜就照常用，要续签时报错；两个文件都不存在才算还没生成过
func TestEnsureServerKeepsCAWhenKeyMissing(t *testing.T) {
	dir := t.TempDir()
	ips := IPs("203.0.113.10")
	if err := Generate(dir, ips); err != nil {
		t.Fatal(err)
	}
	f := FilesIn(dir)
	before, err := Fingerprint(f.CACert)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.CAKey, f.CAKey+".offline"); err != nil {
		t.Fatal(err)
	}
	if err := EnsureServer(dir, ips); err != nil {
		t.Fatalf("服务器证书还新鲜时不需要 CA 私钥: %v", err)
	}
	if err := os.Remove(f.Cert); err != nil {
		t.Fatal(err)
	}
	if err := EnsureServer(dir, ips); err == nil || !strings.Contains(err.Error(), "cert --regen") {
		t.Fatalf("要续签而 CA 私钥不在时应报错并提示 cert --regen: %v", err)
	}
	if after, _ := Fingerprint(f.CACert); after != before {
		t.Fatal("CA 私钥不在时不应重新生成 CA")
	}
	if _, err := os.Stat(f.CAKey); !os.IsNotExist(err) {
		t.Fatal("不应生成新的 CA 私钥")
	}

	// 私钥放回来就能续签，CA 不变
	if err := os.Rename(f.CAKey+".offline", f.CAKey); err != nil {
		t.Fatal(err)
	}
	if err := EnsureServer(dir, ips); err != nil {
		t.Fatal(err)
	}
	if after, _ := Fingerprint(f.CACert); after != before {
		t.Fatal("续签不应换 CA")
	}

	// 两个都不存在：第一次生成
	fresh := t.TempDir()
	if err := EnsureServer(fresh, ips); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(FilesIn(fresh).CAKey); err != nil {
		t.Fatalf("还没有 CA 时应生成: %v", err)
	}
}

func leafOf(t *testing.T, m *CertManager) *x509.Certificate {
	t.Helper()
	c, _ := m.GetCertificate(nil)
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	return leaf
}

// 证书快过期时，运行中也会用本地 CA 重签；证书文件变了就换上新的，不用重启
func TestCertManagerRenewsAndReloads(t *testing.T) {
	dir := t.TempDir()
	ips := IPs("127.0.0.1")
	if err := Generate(dir, ips); err != nil {
		t.Fatal(err)
	}
	f := FilesIn(dir)
	// 模拟上次启动时证书还剩 31 天，之后一个月没重启：现在只剩 10 天
	if err := issueServerFor(f, ips, 10*24*time.Hour); err != nil {
		t.Fatal(err)
	}
	m, err := NewCertManager(f.Cert, f.Key, nil)
	if err != nil {
		t.Fatal(err)
	}
	if d := time.Until(leafOf(t, m).NotAfter); d > 11*24*time.Hour {
		t.Fatalf("应该先加载那张快过期的证书，剩 %s", d)
	}
	m.renew = func() error { return EnsureServer(dir, ips) }
	m.check(time.Now())
	if d := time.Until(leafOf(t, m).NotAfter); d < 300*24*time.Hour {
		t.Fatalf("应该重签并换上新证书，剩 %s", d)
	}

	// cert --regen 换了证书：下一次检查就换上，不用等一天
	before := leafOf(t, m).SerialNumber
	if err := Generate(dir, ips); err != nil {
		t.Fatal(err)
	}
	m.check(time.Now())
	if leafOf(t, m).SerialNumber.Cmp(before) == 0 {
		t.Fatal("证书文件变了以后应该换上新证书")
	}

	// 私钥坏了（比如正写到一半）：继续用原来的证书
	cur := leafOf(t, m).SerialNumber
	os.WriteFile(f.Key, []byte("broken"), 0o600)
	m.check(time.Now())
	if leafOf(t, m).SerialNumber.Cmp(cur) != 0 {
		t.Fatal("新证书加载失败时应该继续用原来的")
	}
}

// 证书链在握手时就能看到：CA 和服务器证书里都不能带产品名
func TestCertificatesCarryNoProductName(t *testing.T) {
	dir := t.TempDir()
	if err := Generate(dir, IPs("203.0.113.10")); err != nil {
		t.Fatal(err)
	}
	chain, err := os.ReadFile(FilesIn(dir).Cert)
	if err != nil {
		t.Fatal(err)
	}
	for rest := chain; ; {
		var blk *pem.Block
		blk, rest = pem.Decode(rest)
		if blk == nil {
			break
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			t.Fatal(err)
		}
		names := append([]string{c.Subject.String(), c.Issuer.String()}, c.PermittedDNSDomains...)
		names = append(names, c.PermittedEmailAddresses...)
		names = append(names, c.PermittedURIDomains...)
		for _, n := range names {
			if strings.Contains(strings.ToLower(n), "danlu") {
				t.Fatalf("证书里带着产品名: %q", n)
			}
		}
	}
}
