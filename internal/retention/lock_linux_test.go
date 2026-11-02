package retention

import (
	"os"
	"testing"
)

func TestPruneLockExcludesOtherRuns(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	unlock, err := lock(root)
	if err != nil {
		t.Fatal(err)
	}
	if another, err := lock(root); err == nil {
		another()
		t.Fatal("同时取得了两个清理锁")
	}
	unlock()
	unlock, err = lock(root)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}
