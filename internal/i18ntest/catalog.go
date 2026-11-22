// Package i18ntest 给 Go 单测读前端文案目录（packages/core/src/locales/*.ts），
// 核对服务器发出的错误码、中文原文与目录同步。只被 _test.go 引用，不进二进制。
package i18ntest

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// RepoRoot 从当前目录（go test 时是包目录）往上找 go.mod 所在目录。
func RepoRoot(t testing.TB) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("找不到 go.mod")
		}
		dir = parent
	}
}

// 'key': '值' 或 'key': "值"（目录里的普通字符串条目；复数对象不收）
var entryRe = regexp.MustCompile(`(?m)^\s*'([\w.]+)':\s*(?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)")`)

var unescapeRe = regexp.MustCompile(`\\(.)`)

// Core 读 core 目录（locale 是 "zh-CN" 或 "en"），返回 键 → 文案。
func Core(t testing.TB, locale string) map[string]string {
	t.Helper()
	return Load(t, filepath.Join("packages", "core", "src", "locales", locale+".ts"))
}

// Load 读仓库里的一个目录文件（相对仓库根目录）。
func Load(t testing.TB, rel string) map[string]string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(RepoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, m := range entryRe.FindAllStringSubmatch(string(b), -1) {
		v := m[2]
		if v == "" {
			v = m[3]
		}
		out[m[1]] = unescapeRe.ReplaceAllString(v, "$1")
	}
	if len(out) == 0 {
		t.Fatalf("%s 里没有读到任何条目", rel)
	}
	return out
}

var placeholderRe = regexp.MustCompile(`\{(\w+)\}`)

// Placeholders 返回文案里的 {name} 占位符名。
func Placeholders(s string) []string {
	var out []string
	for _, m := range placeholderRe.FindAllStringSubmatch(s, -1) {
		out = append(out, m[1])
	}
	return out
}

// ErrorKeys 返回目录里所有 error.<code> 的 code。
func ErrorKeys(cat map[string]string) []string {
	var out []string
	for k := range cat {
		if code, ok := strings.CutPrefix(k, "error."); ok {
			out = append(out, code)
		}
	}
	return out
}
