package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
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
