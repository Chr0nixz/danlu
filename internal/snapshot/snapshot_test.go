package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func names(m *Meta) map[string]bool {
	out := map[string]bool{}
	for _, f := range m.Files {
		out[f.Path] = true
	}
	return out
}

func TestNonGitWhitelist(t *testing.T) {
	work, out := t.TempDir(), t.TempDir()
	write(t, work, "train.py", "print(1)")
	write(t, work, "cfg/a.yaml", "lr: 1")
	write(t, work, "data/img.png", "PNG")
	write(t, work, "ckpt.pt", "weights")
	write(t, work, "__pycache__/x.pyc", "x")
	write(t, work, "big.py", strings.Repeat("x", 2000))
	m, err := Take(context.Background(), work, out, 1, Options{MaxFile: 1000, MaxTotal: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	got := names(m)
	if !got["train.py"] || !got["cfg/a.yaml"] || got["data/img.png"] || got["ckpt.pt"] || got["big.py"] || got["__pycache__/x.pyc"] {
		t.Fatalf("文件筛选不对: %v", got)
	}
	if m.Git != nil {
		t.Fatal("非 git 目录不应有 git 信息")
	}
	fc, err := ReadFile(out, 1, "cfg/a.yaml")
	if err != nil || fc.Content != "lr: 1" {
		t.Fatalf("读取快照文件失败: %v %+v", err, fc)
	}
}

func TestGitRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	work, out := t.TempDir(), t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", work}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	run("init", "-q")
	write(t, work, ".gitignore", "outputs/\n")
	write(t, work, "train.py", "lr = 1\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	write(t, work, "train.py", "lr = 2\n")    // 未提交的改动
	write(t, work, "new_util.py", "x = 1\n")  // 未跟踪
	write(t, work, "outputs/log.txt", "ignored") // 被忽略
	m, err := Take(context.Background(), work, out, 2, Options{MaxFile: 1 << 20, MaxTotal: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	got := names(m)
	if !got["train.py"] || !got["new_util.py"] || got["outputs/log.txt"] {
		t.Fatalf("文件筛选不对: %v", got)
	}
	if m.Git == nil || len(m.Git.Commit) != 40 || !m.Git.Dirty {
		t.Fatalf("git 信息不对: %+v", m.Git)
	}
	diff, _ := Diff(out, 2)
	if !strings.Contains(string(diff), "+lr = 2") {
		t.Fatalf("diff 应包含未提交的改动: %s", diff)
	}
	fc, _ := ReadFile(out, 2, "train.py")
	if fc == nil || fc.Content != "lr = 2\n" {
		t.Fatal("快照应保存实际运行时的代码（含未提交改动）")
	}
}
