package api

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"sync"
)

// 面板的内容安全策略（CSP）。日志、代码高亮、diff 都经 innerHTML 渲染，防线只有 ansi_up 和 highlight.js 自己的转义；
// 万一哪里漏了，script-src 只放行自己的脚本文件和 index.html 里那段内联脚本（按哈希），注入的 <script>、
// 事件属性、javascript: 链接都不会执行，也连不出去（connect-src 只到本机）。
// 样式放开内联：xterm 会插 <style>，ansi_up 的 256 色和真彩色写在 style 属性里，样式注入的危害远小于脚本。
type cspPolicy struct {
	once    sync.Once
	scripts string // index.html 里内联脚本的哈希，空格分隔
}

var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// scriptHashes 算 index.html 里每段不带属性的内联脚本的 sha256（CSP 按标签里的原文算，空白也算）。
// 只认 <script>：带 src 的是脚本文件，'self' 已经放行
func scriptHashes(index []byte) string {
	var out []string
	for _, m := range inlineScript.FindAllSubmatch(index, -1) {
		sum := sha256.Sum256(m[1])
		out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
	}
	return strings.Join(out, " ")
}

// wsHost 只认像主机名和端口的 Host 头，免得把怪字符拼进响应头
var wsHost = regexp.MustCompile(`^[A-Za-z0-9.\-]+(:\d{1,5})?$|^\[[0-9A-Fa-f:.]+\](:\d{1,5})?$`)

func (s *Server) cspHeader(r *http.Request) string {
	s.csp.once.Do(func() {
		if s.Static == nil {
			return
		}
		if index, err := fs.ReadFile(s.Static, "index.html"); err == nil {
			s.csp.scripts = scriptHashes(index)
		}
	})
	script := "'self'"
	if s.csp.scripts != "" {
		script += " " + s.csp.scripts
	}
	connect := "'self'"
	// 网页终端走 WebSocket。CSP3 里 'self' 已经包含同源的 ws/wss，旧一点的 Safari 不认，明确写上
	if wsHost.MatchString(r.Host) {
		if r.TLS != nil {
			connect += " wss://" + r.Host
		} else {
			connect += " ws://" + r.Host
		}
	}
	return "default-src 'self'; script-src " + script + "; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; " +
		"font-src 'self' data:; connect-src " + connect + "; object-src 'none'; base-uri 'none'; form-action 'self'; frame-ancestors 'none'"
}
