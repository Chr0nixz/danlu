//go:build linux

package snapshot

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 工作目录属于其他用户时，git 拒绝读取：快照要在备注里写明，不能静默当成非 git 目录。
// GIT_TEST_ASSUME_DIFFERENT_OWNER 让 git 按"属于其他用户"处理（git 2.35.2 起）。
func TestTakeNotesDubiousOwnership(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("没有 git")
	}
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(dir, "train.py"), []byte("print(1)\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_TEST_ASSUME_DIFFERENT_OWNER", "1")
	if ok, note := gitCheck(context.Background(), dir); ok || note == "" {
		probe, _ := exec.Command("git", "-C", dir, "rev-parse", "--is-inside-work-tree").CombinedOutput()
		if !strings.Contains(string(probe), "dubious ownership") {
			t.Skipf("这个 git 版本不支持 GIT_TEST_ASSUME_DIFFERENT_OWNER: %s", probe)
		}
		t.Fatalf("gitCheck = %v %q，应识别 dubious ownership", ok, note)
	}
	meta, err := Take(context.Background(), dir, t.TempDir(), 1, Options{MaxFile: 1 << 20, MaxTotal: 1 << 20})
	if err != nil {
		t.Fatal(err)
	}
	if meta.Git != nil || !strings.Contains(meta.Note, "safe.directory") || !strings.Contains(meta.Note, dir) {
		t.Fatalf("快照应注明 git 拒绝读取并给出 safe.directory 提示: git=%v note=%q", meta.Git, meta.Note)
	}
}
