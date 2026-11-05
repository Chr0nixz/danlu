package logs

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func searchFixture(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "train.log")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSearchVisibleLinesAndBlockBoundaries(t *testing.T) {
	first := "被覆盖的 ERROR\r进度 100%\r\n"
	long := strings.Repeat("x", searchBlock-len(first)-2) + "Er\x1b[31mRoR\x1b[0m 中文"
	p := searchFixture(t, first+long+"\n旧行\r最终 ERROR\r\n")
	result, err := Search(context.Background(), p, "error", 200, 1<<30, false, nil)
	if err != nil || result.Truncated || len(result.Matches) != 2 {
		t.Fatalf("搜索未按可见内容匹配: %+v, %v", result, err)
	}
	a, b := result.Matches[0], result.Matches[1]
	if a.Offset != int64(len(first)) || !strings.HasPrefix(a.Text, "…") || !strings.Contains(a.Text, "ErRoR 中文") ||
		b.Offset != int64(len(first)+len(long)+1) || b.Text != "最终 ERROR" {
		t.Fatalf("跨块搜索的偏移或文字错误: %+v", result.Matches)
	}
}

func TestSearchCapsAndIncompleteUTF8(t *testing.T) {
	p := searchFixture(t, "字\n字\n字\xe4\xb8")
	for _, tc := range []struct {
		limit int
		bytes int64
		count int
	}{
		{1, 1 << 30, 1},
		{200, 5, 1},
	} {
		result, err := Search(context.Background(), p, "字", tc.limit, tc.bytes, false, nil)
		if err != nil || !result.Truncated || len(result.Matches) != tc.count || result.Scanned > tc.bytes {
			t.Fatalf("没有遵守搜索上限: %+v, %v", result, err)
		}
	}
	for _, final := range []bool{false, true} {
		result, err := Search(context.Background(), p, "字�", 200, 1<<30, final, nil)
		if err != nil || (len(result.Matches) == 1) != final || result.Truncated {
			t.Fatalf("末尾残缺 UTF-8 处理错误（已结束 %t）: %+v, %v", final, result, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := Search(ctx, p, "字", 200, 1<<30, false, nil)
	if err != nil || result.Scanned != 0 || !result.Truncated {
		t.Fatalf("取消后仍在搜索: %+v, %v", result, err)
	}
}
