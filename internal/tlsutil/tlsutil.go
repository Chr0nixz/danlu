// Package tlsutil 生成"只对本机 IP 有效"的本地 CA，并用它签发服务器证书。
package tlsutil

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

type Files struct{ CACert, CAKey, Cert, Key string }

func FilesIn(dir string) Files {
	return Files{
		CACert: filepath.Join(dir, "ca.crt"),
		CAKey:  filepath.Join(dir, "ca.key"),
		Cert:   filepath.Join(dir, "server.crt"),
		Key:    filepath.Join(dir, "server.key"),
	}
}

// IPs 返回证书里要包含的地址：公网 IP 加上 127.0.0.1（方便本机或 SSH 隧道访问）。
func IPs(publicIP string) []net.IP {
	ips := []net.IP{net.ParseIP("127.0.0.1")}
	if ip := net.ParseIP(publicIP); ip != nil && !ip.Equal(ips[0]) {
		ips = append([]net.IP{ip}, ips...)
	}
	return ips
}

// Generate 重新生成 CA 和服务器证书。CA 带 NameConstraints，
// 只能为 ips 里的地址签发证书，即使私钥泄露也无法冒充其他网站。
func Generate(dir string, ips []net.IP) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f := FilesIn(dir)
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	var ranges []*net.IPNet
	for _, ip := range ips {
		bits := 32
		if ip.To4() == nil {
			bits = 128
		}
		ranges = append(ranges, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Danlu Local CA (" + ips[0].String() + ")", Organization: []string{"Danlu"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLenZero:        true,
		// 域名、邮箱、URI 只允许一个永远不存在的 .invalid 域，等于全部禁止
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         []string{"danlu.invalid"},
		PermittedEmailAddresses:     []string{"danlu.invalid"},
		PermittedURIDomains:         []string{"danlu.invalid"},
		PermittedIPRanges:           ranges,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &caKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	if err := writePEM(f.CACert, "CERTIFICATE", der, 0o644); err != nil {
		return err
	}
	if err := writeKey(f.CAKey, caKey); err != nil {
		return err
	}
	return issueServer(f, ips)
}

// EnsureServer 在服务器证书缺失、快过期或 IP 不匹配时，用现有 CA 重新签发。
func EnsureServer(dir string, ips []net.IP) error {
	f := FilesIn(dir)
	if _, err := os.Stat(f.CAKey); err != nil {
		return Generate(dir, ips)
	}
	if cert, err := loadCert(f.Cert); err == nil {
		fresh := time.Until(cert.NotAfter) > 30*24*time.Hour
		if fresh && sameIPs(cert.IPAddresses, ips) {
			return nil
		}
	}
	ca, err := loadCert(f.CACert)
	if err != nil {
		return err
	}
	for _, ip := range ips {
		if !caPermits(ca, ip) {
			return fmt.Errorf("本地 CA 不允许为 %s 签发证书（公网 IP 变了？），请执行 danlu cert --regen 并重新安装 CA", ip)
		}
	}
	return issueServer(f, ips)
}

// 服务器证书的有效期：397 天是浏览器接受的上限
const serverValidity = 397 * 24 * time.Hour

func issueServer(f Files, ips []net.IP) error { return issueServerFor(f, ips, serverValidity) }

func issueServerFor(f Files, ips []net.IP, validity time.Duration) error {
	ca, err := loadCert(f.CACert)
	if err != nil {
		return err
	}
	caKey, err := loadKey(f.CAKey)
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		// CN 不能写成 IP：OpenSSL 会把像主机名的 CN 当作域名，拿去和 CA 的域名限制比对，导致校验失败。
		// 浏览器只看 SAN 里的 IP。
		Subject: pkix.Name{CommonName: "Danlu Server"},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(validity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	// 证书链：服务器证书 + CA，部分客户端需要完整链
	chain := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	chain = append(chain, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})...)
	if err := os.WriteFile(f.Cert, chain, 0o644); err != nil {
		return err
	}
	return writeKey(f.Key, key)
}

// Fingerprint 返回证书的 SHA-256 指纹（冒号分隔的十六进制）。
func Fingerprint(path string) (string, error) {
	c, err := loadCert(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(c.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":"), nil
}

func caPermits(ca *x509.Certificate, ip net.IP) bool {
	for _, r := range ca.PermittedIPRanges {
		if r.Contains(ip) {
			return true
		}
	}
	return false
}

func sameIPs(a, b []net.IP) bool {
	if len(a) != len(b) {
		return false
	}
	for _, ip := range b {
		if !slices.ContainsFunc(a, ip.Equal) {
			return false
		}
	}
	return true
}

func serial() *big.Int {
	n, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	return n
}

func writePEM(path, typ string, der []byte, mode os.FileMode) error {
	return os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), mode)
}

func writeKey(path string, key *ecdsa.PrivateKey) error {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	return writePEM(path, "EC PRIVATE KEY", der, 0o600)
}

func loadCert(path string) (*x509.Certificate, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New(path + " 不是 PEM 证书")
	}
	return x509.ParseCertificate(blk.Bytes)
}

func loadKey(path string) (*ecdsa.PrivateKey, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return nil, errors.New(path + " 不是 PEM 私钥")
	}
	return x509.ParseECPrivateKey(blk.Bytes)
}
