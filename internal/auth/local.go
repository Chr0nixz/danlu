package auth

import (
	"context"
	"net/http"
	"os"
)

// 本机命令行入口（FEATURES 5.1）：serve 在数据目录里监听一个 unix socket，
// 连接建立时用 SO_PEERCRED 取对端 uid，经 WithLocalPeer 放进连接的 context。
// 这个值只能从连接本身得到，请求头伪造不了；TCP 上的请求永远没有它。
// 对端和 danlu 是同一个用户时（Local），跳过入口 gate 和会话检查：能 SSH 登录这个用户，
// 本来就能读写数据目录、执行 danlu passwd，这条路径不扩大权限。

type localPeerKey struct{}

// WithLocalPeer 记下 unix socket 对端的 uid；取不到时传 -1。
func WithLocalPeer(ctx context.Context, uid int) context.Context {
	return context.WithValue(ctx, localPeerKey{}, uid)
}

// LocalPeer 返回经 unix socket 连进来的请求的对端 uid；不是 socket 上的请求时 ok 为 false。
func LocalPeer(r *http.Request) (uid int, ok bool) {
	uid, ok = r.Context().Value(localPeerKey{}).(int)
	return uid, ok
}

// Local 报告请求是否来自本机 socket 上、和 danlu 同一个用户的命令行。
func Local(r *http.Request) bool {
	uid, ok := LocalPeer(r)
	return ok && uid >= 0 && uid == os.Geteuid()
}
