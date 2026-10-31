package tlsutil

import (
	"bytes"
	"context"
	"crypto/tls"
	"log"
	"os"
	"sync"
	"time"
)

// CertManager 把服务器证书放在内存里，每次 TLS 握手时通过 GetCertificate 给出当前那张，
// 所以换证书不用重启服务：
//
//   - 每分钟看一次证书文件，变了就重新加载（cert --regen、file 模式下换证书）；
//   - 每天调用一次 renew。local-ca 模式下它是 EnsureServer：剩不到 30 天就用本地 CA 重签，
//     不会因为一个月没重启，证书在运行中过期。
type CertManager struct {
	certFile, keyFile string
	renew             func() error // 可以为空（file 模式：证书是用户自己管的）

	mu      sync.RWMutex
	cert    *tls.Certificate
	certPEM []byte
	keyPEM  []byte

	renewedAt time.Time // 只有 Run 所在的 goroutine 读写
}

// NewCertManager 先 renew 一次，再加载证书。
func NewCertManager(certFile, keyFile string, renew func() error) (*CertManager, error) {
	m := &CertManager{certFile: certFile, keyFile: keyFile, renew: renew}
	if renew != nil {
		if err := renew(); err != nil {
			return nil, err
		}
		m.renewedAt = time.Now()
	}
	if _, err := m.reload(); err != nil {
		return nil, err
	}
	return m, nil
}

func (m *CertManager) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cert, nil
}

// Run 每分钟调用一次 check，直到 ctx 结束。
func (m *CertManager) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.check(now)
		}
	}
}

func (m *CertManager) check(now time.Time) {
	if m.renew != nil && now.Sub(m.renewedAt) >= 24*time.Hour {
		m.renewedAt = now
		if err := m.renew(); err != nil {
			log.Printf("检查服务器证书失败: %v", err)
		}
	}
	changed, err := m.reload()
	switch {
	case err != nil:
		log.Printf("加载服务器证书失败，继续用原来的: %v", err)
	case changed:
		log.Printf("已换用新的服务器证书 %s", m.certFile)
	}
}

// reload 在证书或私钥文件的内容变了时重新加载，返回是否换了证书。
// 证书和私钥对不上（比如正写到一半）时不换，下一分钟再试。
func (m *CertManager) reload() (bool, error) {
	c, err := os.ReadFile(m.certFile)
	if err != nil {
		return false, err
	}
	k, err := os.ReadFile(m.keyFile)
	if err != nil {
		return false, err
	}
	m.mu.RLock()
	same := bytes.Equal(c, m.certPEM) && bytes.Equal(k, m.keyPEM)
	m.mu.RUnlock()
	if same {
		return false, nil
	}
	pair, err := tls.X509KeyPair(c, k)
	if err != nil {
		return false, err
	}
	m.mu.Lock()
	m.cert, m.certPEM, m.keyPEM = &pair, c, k
	m.mu.Unlock()
	return true, nil
}
