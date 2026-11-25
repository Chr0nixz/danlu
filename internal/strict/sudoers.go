package strict

import (
	"fmt"
	"strings"
)

// sudoersRule 是写进 /etc/sudoers.d 的规则：允许 uid 以 root 运行助手的 start 和 check。
// noTTY 时加一行 !requiretty：老的 RHEL 系在全局开了 requiretty，danlu 作为服务没有终端，不加就调不动 sudo。
func sudoersRule(uid int, user string, noTTY bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# 由 danlu strict install 生成：允许 uid %d 以 root 运行严格限卡助手的 start 和 check。\n", uid)
	b.WriteString("# 助手只会把这个用户自己的任务放进只放行分到的卡的 systemd 服务。删除：sudo danlu strict uninstall\n")
	if noTTY {
		fmt.Fprintf(&b, "Defaults!%s !requiretty\n", HelperPath)
	}
	fmt.Fprintf(&b, "%s ALL=(root) NOPASSWD: %[2]s start, %[2]s check\n", user, HelperPath)
	return b.String()
}

// clip 截短命令输出，放进错误信息
func clip(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

// pickSudoersRule 选一条本机 sudo 认的规则：validate 写入并校验一条规则，失败时返回 visudo 的输出。
// 先用带 !requiretty 的；校验因为不认 requiretty 失败时（sudo-rs，Ubuntu 25.10 起的默认 sudo，
// 它从不要求终端）去掉这一行再试。去掉只会让 sudo 更严，不会多放行什么；其他原因的失败不重试。
func pickSudoersRule(uid int, user string, validate func(rule string) ([]byte, error)) (string, error) {
	rule := sudoersRule(uid, user, true)
	out, err := validate(rule)
	if err == nil {
		return rule, nil
	}
	if !strings.Contains(string(out), "requiretty") {
		return "", fmt.Errorf("sudoers 规则校验失败: %v %s", err, clip(out))
	}
	plain := sudoersRule(uid, user, false)
	if out2, err2 := validate(plain); err2 != nil {
		return "", fmt.Errorf("sudoers 规则校验失败: %v %s（不带 requiretty 一行也失败: %v %s）", err, clip(out), err2, clip(out2))
	}
	return plain, nil
}
