package auth

import (
	"net/http/httptest"
	"os"
	"runtime"
	"testing"
)

// socket 上的对端 uid 只认连接 context 里的值；请求头里写什么都没用
func TestLocalPeer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上没有 euid")
	}
	a, _ := newSecurityAuth(t)
	plain := httptest.NewRequest("GET", "/api/jobs", nil)
	plain.Header.Set("X-Forwarded-For", "cli")
	if Local(plain) || a.Authorized(plain) || ClientIP(plain) == "cli" {
		t.Fatal("没有对端 uid 的请求不是命令行")
	}
	same := plain.WithContext(WithLocalPeer(plain.Context(), os.Geteuid()))
	if !Local(same) || !a.Authorized(same) || ClientIP(same) != "cli" {
		t.Fatal("同一个用户的 socket 请求应放行并记为 cli")
	}
	other := plain.WithContext(WithLocalPeer(plain.Context(), os.Geteuid()+1))
	if Local(other) || a.Authorized(other) {
		t.Fatal("别的 uid 不能放行")
	}
	if ClientIP(other) != "cli" {
		t.Fatal("socket 上的请求都记为 cli")
	}
	unknown := plain.WithContext(WithLocalPeer(plain.Context(), -1))
	if Local(unknown) {
		t.Fatal("取不到 uid 时不能放行")
	}
}
