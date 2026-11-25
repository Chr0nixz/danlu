package snapshot

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
}

func readCode(t *testing.T, code, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(code, rel))
	if err != nil {
		t.Fatalf("读不到 %s: %v", rel, err)
	}
	return string(b)
}

func isLink(t *testing.T, code, rel string) bool {
	t.Helper()
	fi, err := os.Lstat(filepath.Join(code, rel))
	return err == nil && fi.Mode()&os.ModeSymlink != 0
}

// 快照只收了 data/ 里已跟踪的配置；被 .gitignore 排除的权重要从工作目录补上，
// 快照之后新建的代码文件不能混进来
func TestRestoreLinksIgnoredFilesInMixedDirectory(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	work, snaps, runs := t.TempDir(), t.TempDir(), t.TempDir()
	gitIn(t, work, "init", "-q")
	write(t, work, ".gitignore", "data/weights.bin\noutputs/\n")
	write(t, work, "train.py", "print(1)\n")
	write(t, work, "data/config.json", "v1")
	write(t, work, "models/big.bin", strings.Repeat("x", 200)) // 已跟踪，但超过单文件上限
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-q", "-m", "init")
	write(t, work, "data/weights.bin", "w1")
	write(t, work, "outputs/old.txt", "old")
	if _, err := Take(context.Background(), work, snaps, 1, Options{MaxFile: 100, MaxTotal: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	// 快照之后：已跟踪的配置改了，新建了一个代码文件，权重也换了
	write(t, work, "data/config.json", "v2")
	write(t, work, "data/extra.py", "x = 1\n")
	write(t, work, "data/weights.bin", "w2")

	_, note, err := Restore(context.Background(), snaps, runs, work, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	code := RunCodeDir(runs, 2)
	if got := readCode(t, code, "data/config.json"); got != "v1" || isLink(t, code, "data/config.json") {
		t.Fatalf("已跟踪的配置应该用快照里的版本: %q", got)
	}
	if got := readCode(t, code, "data/weights.bin"); got != "w2" || !isLink(t, code, "data/weights.bin") {
		t.Fatalf("被忽略的权重应该链接到工作目录: %q", got)
	}
	if !isLink(t, code, "outputs") || readCode(t, code, "outputs/old.txt") != "old" {
		t.Fatal("整个被忽略的目录应该链接一次")
	}
	if readCode(t, code, "models/big.bin") != strings.Repeat("x", 200) {
		t.Fatal("快照跳过的大文件应该补上")
	}
	if _, err := os.Lstat(filepath.Join(code, "data/extra.py")); !os.IsNotExist(err) {
		t.Fatalf("快照之后新建的代码文件不应该出现: %v", err)
	}
	for _, want := range []string{"data/weights.bin", "models/big.bin", "outputs/"} {
		if !strings.Contains(note, want) {
			t.Fatalf("备注应该写明 %s 来自工作目录: %s", want, note)
		}
	}
	if strings.Contains(note, "extra.py") || strings.Contains(note, "config.json") {
		t.Fatalf("备注不应该列出没链接的文件: %s", note)
	}
}

// 非 git 目录：快照规则不收的文件和目录从工作目录补上，快照之后新建的代码文件不混进来
func TestRestoreNonGitLinksFilesOutsideSnapshotRules(t *testing.T) {
	work, snaps, runs := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, work, "train.py", "print(1)\n")
	write(t, work, "cfg/a.yaml", "lr: 1")
	write(t, work, "cfg/weights.pt", "w")
	write(t, work, "data/img.png", "PNG")
	write(t, work, "wandb/run.log", "log")
	if _, err := Take(context.Background(), work, snaps, 1, Options{MaxFile: 1 << 20, MaxTotal: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	write(t, work, "cfg/new.py", "x = 1\n")
	if _, _, err := Restore(context.Background(), snaps, runs, work, 1, 2); err != nil {
		t.Fatal(err)
	}
	code := RunCodeDir(runs, 2)
	if isLink(t, code, "cfg/a.yaml") || readCode(t, code, "cfg/a.yaml") != "lr: 1" {
		t.Fatal("代码文件应该用快照里的版本")
	}
	for _, rel := range []string{"cfg/weights.pt", "data", "wandb"} {
		if !isLink(t, code, rel) {
			t.Fatalf("%s 应该链接到工作目录", rel)
		}
	}
	if _, err := os.Lstat(filepath.Join(code, "cfg/new.py")); !os.IsNotExist(err) {
		t.Fatalf("快照之后新建的代码文件不应该出现: %v", err)
	}
}

// 工作目录里 git clone 过一个依赖、没加进 .gitignore 也不是子模块：快照把它按目录跳过（不带尾斜杠），
// 重跑时链接回工作目录
func TestSnapshotNestedRepoRestores(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	work, snaps, runs := t.TempDir(), t.TempDir(), t.TempDir()
	gitIn(t, work, "init", "-q")
	write(t, work, "train.py", "print(1)\n")
	gitIn(t, work, "add", ".")
	gitIn(t, work, "commit", "-q", "-m", "init")
	lib := filepath.Join(work, "third_party", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, lib, "init", "-q")
	write(t, lib, "x.c", "int x;\n")

	meta, err := Take(context.Background(), work, snaps, 1, Options{MaxFile: 1 << 20, MaxTotal: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Skipped) != 1 || meta.Skipped[0].Path != "third_party/lib" {
		t.Fatalf("嵌套仓库应按目录跳过、不带尾斜杠: %+v", meta.Skipped)
	}
	if _, _, err := Restore(context.Background(), snaps, runs, work, 1, 2); err != nil {
		t.Fatalf("有嵌套仓库时恢复失败: %v", err)
	}
	code := RunCodeDir(runs, 2)
	if !isLink(t, code, "third_party/lib") || readCode(t, code, "third_party/lib/x.c") != "int x;\n" {
		t.Fatal("嵌套仓库应该链接回工作目录")
	}
}

// 旧版快照的跳过清单里带着尾斜杠，磁盘上已有的这种快照照样能恢复
func TestRestoreToleratesTrailingSlashInOldManifest(t *testing.T) {
	work, snaps, runs := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, work, "train.py", "print(1)\n")
	write(t, work, "third_party/lib/x.c", "int x;\n")
	meta, err := Take(context.Background(), work, snaps, 1, Options{MaxFile: 1 << 20, MaxTotal: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	meta.Files = []FileEntry{{Path: "train.py", Size: 10}}
	meta.Skipped, meta.SkipCount = []Skip{{Path: "third_party/lib/", Reason: "子模块或目录"}}, 1
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath(snaps, 1), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Restore(context.Background(), snaps, runs, work, 1, 2); err != nil {
		t.Fatalf("旧清单带尾斜杠时恢复失败: %v", err)
	}
	if readCode(t, RunCodeDir(runs, 2), "third_party/lib/x.c") != "int x;\n" {
		t.Fatal("跳过的目录应该链接回工作目录")
	}
}

// 名字不是合法 UTF-8 的文件不进快照（恢复和对比都认不了），重跑时链接工作目录里的
func TestSnapshotSkipsInvalidUTF8Names(t *testing.T) {
	work, snaps, runs := t.TempDir(), t.TempDir(), t.TempDir()
	write(t, work, "train.py", "print(1)\n")
	bad := "bad\xff.py"
	if err := os.WriteFile(filepath.Join(work, bad), []byte("x = 1\n"), 0o644); err != nil {
		t.Skipf("文件系统不接受非 UTF-8 文件名: %v", err)
	}
	meta, err := Take(context.Background(), work, snaps, 1, Options{MaxFile: 1 << 20, MaxTotal: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if names(meta)[bad] || !strings.Contains(meta.Note, "UTF-8") {
		t.Fatalf("非法文件名应不进快照并写进备注: %+v", meta)
	}
	if _, err := FileHashes(context.Background(), snaps, 1); err != nil {
		t.Fatalf("对比读不了快照: %v", err)
	}
	if _, _, err := Restore(context.Background(), snaps, runs, work, 1, 2); err != nil {
		t.Fatalf("恢复失败: %v", err)
	}
	if got := readCode(t, RunCodeDir(runs, 2), bad); got != "x = 1\n" {
		t.Fatalf("非法文件名的文件应链接回工作目录: %q", got)
	}
}
