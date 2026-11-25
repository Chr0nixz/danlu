//go:build unix

package retention

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"danlu/internal/store"
)

// 快照重跑在 snapshots/ 里给源快照建硬链接：总量只计一次；删掉还有别的链接的那个腾不出空间，不从余量里扣。
// 两个任务的日志各 100 字节，共用一份 1000 字节的快照包：实际占用 1200
func linkedJobs(t *testing.T) (*Pruner, *store.Job, *store.Job) {
	t.Helper()
	p := newPruner(t)
	a := addJob(t, p, 30, store.StatusSucceeded)
	b := addJob(t, p, 20, store.StatusSucceeded)
	src := filepath.Join(p.Paths.Snapshots, fmt.Sprintf("%d.tar.gz", a.ID))
	if err := os.WriteFile(src, []byte(strings.Repeat("x", 1000)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(src, filepath.Join(p.Paths.Snapshots, fmt.Sprintf("%d.tar.gz", b.ID))); err != nil {
		t.Fatal(err)
	}
	return p, a, b
}

func TestPruneCountsHardLinksOnce(t *testing.T) {
	p, _, _ := linkedJobs(t)
	p.Options = Options{MaxBytes: 1500, DryRun: true}
	r, err := p.Prune(context.Background())
	if err != nil || r.TotalBytes != 1200 || len(r.Jobs) != 0 {
		t.Fatalf("硬链接应只计一次，没超上限不该清理: %+v %v", r, err)
	}
}

// 只清 a：b 还链着那份快照，腾出的只有 a 的日志
func TestPruneSharedLinkFreesNothing(t *testing.T) {
	p, a, _ := linkedJobs(t)
	p.Options = Options{MaxBytes: 1150, DryRun: true}
	r, err := p.Prune(context.Background())
	if err != nil || len(r.Jobs) != 1 || r.Jobs[0].JobID != a.ID || r.Jobs[0].Bytes != 100 || r.Bytes != 100 {
		t.Fatalf("删掉还有别的链接的文件不应算腾出空间: %+v %v", r, err)
	}
}

// 两个都清时，最后一个链接才算腾出：预览不真删，也要算对
func TestPruneLastLinkFrees(t *testing.T) {
	for _, dry := range []bool{true, false} {
		p, a, b := linkedJobs(t)
		p.Options = Options{MaxBytes: 1050, DryRun: dry}
		r, err := p.Prune(context.Background())
		if err != nil || len(r.Jobs) != 2 || r.Jobs[0].JobID != a.ID || r.Jobs[0].Bytes != 100 ||
			r.Jobs[1].JobID != b.ID || r.Jobs[1].Bytes != 1100 || r.Bytes != 1200 {
			t.Fatalf("dryRun=%v：腾出的空间应等于实际占用: %+v %v", dry, r, err)
		}
		if !dry {
			entries, err := os.ReadDir(p.Paths.Snapshots)
			if err != nil || len(entries) != 0 {
				t.Fatalf("两个任务的快照都应删掉: %v %v", entries, err)
			}
		}
	}
}
