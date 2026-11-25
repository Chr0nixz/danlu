package snapshot

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
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
	write(t, work, "train.py", "lr = 2\n")       // 未提交的改动
	write(t, work, "new_util.py", "x = 1\n")     // 未跟踪
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

func gitRepo(t *testing.T) (string, func(...string)) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	work := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", work}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	run("init", "-q")
	return work, run
}

// 总大小上限先给被跟踪的代码：排在字母序前面、没写进 .gitignore 的日志不能把预算占满
func TestBudgetPrefersTrackedFiles(t *testing.T) {
	work, run := gitRepo(t)
	write(t, work, "train.py", "lr = 1\n")
	write(t, work, "zz/model.py", "class M: pass\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	for _, name := range []string{"a_logs/1.txt", "a_logs/2.txt", "a_logs/3.txt"} {
		write(t, work, name, strings.Repeat("x", 400))
	}
	m, err := Take(context.Background(), work, t.TempDir(), 1, Options{MaxFile: 1 << 20, MaxTotal: 1000})
	if err != nil {
		t.Fatal(err)
	}
	got := names(m)
	if !got["train.py"] || !got["zz/model.py"] {
		t.Fatalf("被跟踪的代码应先收进快照: %v", got)
	}
	if !m.Truncated || m.SkipCount == 0 || len(m.Files) != 4 {
		t.Fatalf("未跟踪的日志应按剩下的预算收，超出的跳过: %+v", m)
	}
	if strings.Contains(m.Note, "git 跟踪") {
		t.Fatalf("代码都收进去了，不该提示: %q", m.Note)
	}
	for i := 1; i < len(m.Files); i++ {
		if m.Files[i-1].Path > m.Files[i].Path {
			t.Fatalf("清单应按路径排序: %+v", m.Files)
		}
	}

	// 代码本身超过上限时，备注要说清楚：快照重跑会用工作目录里的版本
	m, err = Take(context.Background(), work, t.TempDir(), 2, Options{MaxFile: 1 << 20, MaxTotal: 5})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Note, "2 个 git 跟踪的文件") {
		t.Fatalf("跳过被跟踪的文件时应写进备注: %q", m.Note)
	}
}

// 一个文件读不了（docker 留下的 root 文件之类）只跳过它，不让整份快照失败
func TestUnreadableFileIsSkipped(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 读得了任何文件")
	}
	work, run := gitRepo(t)
	write(t, work, "train.py", "lr = 1\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	secret := filepath.Join(work, "root_owned.log")
	write(t, work, "root_owned.log", "x")
	if err := os.Chmod(secret, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(secret, 0o644) })
	m, err := Take(context.Background(), work, t.TempDir(), 1, Options{MaxFile: 1 << 20, MaxTotal: 1 << 20})
	if err != nil {
		t.Fatalf("一个文件读不了不应让整份快照失败: %v", err)
	}
	if got := names(m); !got["train.py"] || got["root_owned.log"] {
		t.Fatalf("文件筛选不对: %v", got)
	}
	if len(m.Skipped) != 1 || m.Skipped[0].Path != "root_owned.log" || m.Skipped[0].Reason != "没有读取权限" {
		t.Fatalf("读不了的文件应记进跳过清单: %+v", m.Skipped)
	}
	if !strings.Contains(m.Note, "1 个文件读不了") {
		t.Fatalf("备注应提到读不了的文件: %q", m.Note)
	}
}

// 包写不出来时快照失败，但已经读到的 git 信息要交回去，任务照样记下 commit
func TestFailureKeepsGitInfo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root 写得了只读目录")
	}
	work, run := gitRepo(t)
	write(t, work, "train.py", "lr = 1\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	out := t.TempDir()
	if err := os.Chmod(out, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(out, 0o700) })
	m, err := Take(context.Background(), work, out, 1, Options{MaxFile: 1 << 20, MaxTotal: 1 << 20})
	if err == nil {
		t.Fatal("快照目录不可写时应该失败")
	}
	if m == nil || m.Git == nil || len(m.Git.Commit) != 40 || len(m.Files) != 0 {
		t.Fatalf("失败时应只交回 git 信息: %+v", m)
	}
}

// 后台的 git status 不能拿 index.lock 回写用户仓库的索引：超时被杀时会留下锁文件，
// 也会和用户自己的 git add/commit 抢锁
func TestGitInfoDoesNotRewriteIndex(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	work := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", work}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, b)
		}
	}
	run("init", "-q")
	write(t, work, "train.py", "lr = 1\n")
	run("add", ".")
	run("commit", "-q", "-m", "init")
	// 内容不变、修改时间变了：索引里的 stat 信息过期，普通的 git status 会刷新并回写索引
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(filepath.Join(work, "train.py"), later, later); err != nil {
		t.Fatal(err)
	}
	index := filepath.Join(work, ".git", "index")
	before, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if g, _ := gitInfo(context.Background(), work, true); g.Dirty {
		t.Fatalf("只改了修改时间，不应算有改动: %+v", g)
	}
	after, err := os.ReadFile(index)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("后台 git 调用回写了用户仓库的索引")
	}
}
