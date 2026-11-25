package logs

import "testing"

// 与 packages/core/src/terminal.ts 的 collapseCR 同一套规则
func TestCollapseCR(t *testing.T) {
	for in, want := range map[string]string{
		"plain\nlines\n": "plain\nlines\n",
		"a\r\nb\r\n":     "a\nb\n",
		"\r 10%|█   |\r 50%|███ |\r100%|████|\n": "100%|████|\n",
		"epoch 1\r 30%\r 60%": " 60%",
		"x\r":                 "x",
		"x\r\r":               "x",
		"a\r\r\nb":            "a\nb",
		"epoch 1\r 30%\r":     " 30%",
		"\r\r\n":              "\n",
		"a\rb\nc\rd\r\ne":     "b\nd\ne",
		"":                    "",
	} {
		if got := CollapseCR(in); got != want {
			t.Errorf("CollapseCR(%q) = %q，应为 %q", in, got, want)
		}
	}
}
