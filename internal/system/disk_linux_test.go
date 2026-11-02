package system

import "testing"

func TestDiskStats(t *testing.T) {
	id, total, available, err := diskStats(t.TempDir())
	if err != nil || id == "" || total == 0 || available > total {
		t.Fatalf("statfs 返回值不合法: %s %d %d %v", id, total, available, err)
	}
}
