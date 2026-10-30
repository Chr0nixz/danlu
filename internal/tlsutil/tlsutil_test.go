package tlsutil

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"os"
	"testing"
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
