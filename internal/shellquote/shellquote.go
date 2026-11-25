// Package shellquote 按 POSIX sh 的规则给参数加引号：danlu submit -- a b c 有多个参数时，
// 逐个加引号再用空格拼成任务命令，记录下来的命令交给 sh 执行时还原出同一个 argv。
package shellquote

import "strings"

// Quote 返回 s 在 sh 里的字面写法：只含安全字符时原样返回，否则用单引号包起来，
// 内部的每个单引号先结束引号、转义、再重新开始；空串写成一对单引号。
func Quote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, unsafe) < 0 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Join 逐个加引号后用空格拼接。
func Join(args []string) string {
	out := make([]string, len(args))
	for i, a := range args {
		out[i] = Quote(a)
	}
	return strings.Join(out, " ")
}

// unsafe 报告 r 在 sh 里是否需要引号。安全字符只有字母、数字和几个不会被 shell 解释的标点；
// 其余一律加引号（包括 ~ 和 =：前者会展开，后者出现在第一个词里会被当成变量赋值）。
func unsafe(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	}
	return !strings.ContainsRune("-_./:,+@%", r)
}
