package shellquote

import (
	"os/exec"
	"runtime"
	"slices"
	"strings"
	"testing"
)

var cases = [][]string{
	{"python", "train.py", "--lr", "3e-4"},
	{"torchrun", "--nproc_per_node", "4", "train.py", "--name", "a b"},
	{"echo", "it's", `"q"`, "$HOME", "`id`", "a;b", "x|y", "*.py", "~", "k=v"},
	{"printf", "", "\t", "line1\nline2", "中文 参数", `back\slash`, "!"},
	{"--flag=1", "a,b", "user@host:/p", "50%"},
}

func TestQuoteKnown(t *testing.T) {
	for in, want := range map[string]string{
		"":      "''",
		"abc":   "abc",
		"a b":   "'a b'",
		"it's":  `'it'\''s'`,
		"$HOME": "'$HOME'",
		"~":     "'~'",
		"k=v":   "'k=v'",
		"3e-4":  "3e-4",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %s，应为 %s", in, got, want)
		}
	}
}

// 拼出来的命令交给 sh 执行，还原出的 argv 与原来逐字相同
func TestJoinRoundTripThroughSh(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("需要 sh")
	}
	for _, args := range cases {
		script := `for a in "$@"; do printf '%s\0' "$a"; done`
		out, err := exec.Command("sh", "-c", "set -- "+Join(args)+"; "+script).Output()
		if err != nil {
			t.Fatalf("%q: %v", args, err)
		}
		got := strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
		if !slices.Equal(got, args) {
			t.Errorf("往返不一致：\n  原来 %q\n  得到 %q\n  命令 %s", args, got, Join(args))
		}
	}
}
