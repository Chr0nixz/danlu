package api

import (
	"slices"
	"strings"
	"testing"
)

func TestCommandTokensKeepShellWordsAndOriginalLayout(t *testing.T) {
	cmd := "BATCH=32 python train.py --name=\"run with spaces\" --tag a\\ b \\\n  --path \"$(pwd)/model\" && echo done"
	tokens := commandTokens(cmd)
	if strings.Join(tokens, "") != cmd {
		t.Fatalf("分词改变了命令原文: %q", tokens)
	}
	for _, word := range []string{"--name=\"run with spaces\"", "a\\ b", "\"$(pwd)/model\"", "&&"} {
		if !slices.Contains(tokens, word) {
			t.Fatalf("缺少完整的 shell 词 %q: %q", word, tokens)
		}
	}
}
