package retention

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danlu/internal/config"
	"danlu/internal/snapshot"
	"danlu/internal/store"
)

func newPruner(t *testing.T) *Pruner {
	t.Helper()
	p := config.PathsFor(t.TempDir())
	if err := p.MkdirAll(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(p.DB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return &Pruner{Store: st, Paths: p, OutputRoot: filepath.Join(t.TempDir(), "outputs"), Options: Options{KeepDays: 90}}
}

func addJob(t *testing.T, p *Pruner, age int, status string) *store.Job {
	t.Helper()
	j := &store.Job{Name: "训练", Workdir: "/work", Cmd: "train", Status: status}
	if err := p.Store.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(-time.Duration(age) * 24 * time.Hour).Unix()
	j.EndedAt = &at
	j.OutputDir = filepath.Join(p.OutputRoot, fmt.Sprint(j.ID))
	if err := p.Store.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.Paths.Logs, fmt.Sprintf("%d.log", j.ID)), []byte(strings.Repeat("x", 100)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(j.OutputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j.OutputDir, "checkpoint.pt"), []byte("产物"), 0o600); err != nil {
		t.Fatal(err)
	}
	return j
}

func TestPrunePreviewProtectsStarsRunningAndOutputs(t *testing.T) {
	p := newPruner(t)
	old := addJob(t, p, 120, store.StatusSucceeded)
	star := addJob(t, p, 120, store.StatusFailed)
	if _, err := p.Store.SetStarred(star.ID, true); err != nil {
		t.Fatal(err)
	}
	fresh := addJob(t, p, 1, store.StatusSucceeded)
	running := addJob(t, p, 120, store.StatusRunning)
	pending := addJob(t, p, 120, store.StatusFailed)
	pending.Recipe = &store.Recipe{Pending: true}
	if err := p.Store.SaveJob(pending); err != nil {
		t.Fatal(err)
	}
	for _, name := range pathsFor(old.ID)[1:] {
		path := filepath.Join(p.Paths.Home, filepath.FromSlash(name))
		if strings.HasPrefix(name, "run/") {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
			path = filepath.Join(path, "cmd.sh")
		}
		if err := os.WriteFile(path, []byte("任务文件"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	usedHash, orphanHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	old.Recipe = &store.Recipe{EnvHash: usedHash}
	if err := p.Store.SaveJob(old); err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{usedHash, orphanHash} {
		if err := os.WriteFile(filepath.Join(p.Paths.Envs, hash+".txt"), []byte("包"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	p.Options.DryRun = true
	r, err := p.Prune(context.Background())
	if err != nil || len(r.Jobs) != 1 || r.Jobs[0].JobID != old.ID {
		t.Fatalf("预览 = %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(p.Paths.Logs, fmt.Sprintf("%d.log", old.ID))); err != nil {
		t.Fatal("预览删除了日志")
	}
	if got, _ := p.Store.GetJob(old.ID); got.PrunedAt != nil {
		t.Fatal("预览更改了任务记录")
	}
	// 包采集中时连孤儿清单也不清理。
	if len(r.Envs) != 0 {
		t.Fatalf("采集中清理了环境文件: %+v", r)
	}
	pending.Recipe.Pending = false
	if err := p.Store.SaveJob(pending); err != nil {
		t.Fatal(err)
	}
	p.Options.DryRun = false
	r, err = p.Prune(context.Background())
	if err != nil || len(r.Jobs) != 2 {
		t.Fatalf("清理 = %+v %v", r, err)
	}
	if got, _ := p.Store.GetJob(old.ID); got.PrunedAt == nil || got.Pruning {
		t.Fatal("清理状态未记录")
	}
	for _, j := range []*store.Job{star, fresh, running} {
		if _, err := os.Stat(filepath.Join(p.Paths.Logs, fmt.Sprintf("%d.log", j.ID))); err != nil {
			t.Fatalf("误删 #%d 日志: %v", j.ID, err)
		}
	}
	for _, j := range []*store.Job{old, star, fresh, running, pending} {
		if _, err := os.Stat(filepath.Join(j.OutputDir, "checkpoint.pt")); err != nil {
			t.Fatalf("误删 #%d 产物: %v", j.ID, err)
		}
	}
	if _, err := os.Stat(filepath.Join(p.Paths.Envs, usedHash+".txt")); err != nil {
		t.Fatal("清理后仍被任务引用的环境文件被删除")
	}
	// 运行任务会阻止孤儿环境 GC，结束后下一轮才删除。
	running.Status = store.StatusSucceeded
	if err := p.Store.SaveJob(running); err != nil {
		t.Fatal(err)
	}
	if _, err = p.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.Paths.Envs, orphanHash+".txt")); !os.IsNotExist(err) {
		t.Fatalf("孤儿环境文件未清掉: %v", err)
	}
}

func TestPruneSizeOldestFirst(t *testing.T) {
	p := newPruner(t)
	a := addJob(t, p, 30, store.StatusSucceeded)
	star := addJob(t, p, 40, store.StatusSucceeded)
	if _, err := p.Store.SetStarred(star.ID, true); err != nil {
		t.Fatal(err)
	}
	b := addJob(t, p, 20, store.StatusSucceeded)
	addJob(t, p, 10, store.StatusSucceeded)
	p.Options = Options{MaxBytes: 250, DryRun: true}
	r, err := p.Prune(context.Background())
	if err != nil || len(r.Jobs) != 2 || r.Jobs[0].JobID != a.ID || r.Jobs[1].JobID != b.ID {
		t.Fatalf("按大小清理顺序错误: %+v %v", r, err)
	}
}

func TestPruneExplicitAgeDoesNotEvictNewerJobsForQuota(t *testing.T) {
	p := newPruner(t)
	old := addJob(t, p, 10, store.StatusSucceeded)
	addJob(t, p, 1, store.StatusSucceeded)
	age := 72 * time.Hour
	p.Options = Options{OlderThan: &age, MaxBytes: 1, DryRun: true}
	r, err := p.Prune(context.Background())
	if err != nil || len(r.Jobs) != 1 || r.Jobs[0].JobID != old.ID {
		t.Fatalf("显式时长不应被总量策略扩大: %+v %v", r, err)
	}
}

func TestPruneDoesNotCountTrainingOutputsTowardsQuota(t *testing.T) {
	p := newPruner(t)
	addJob(t, p, 1, store.StatusSucceeded)
	p.OutputRoot = filepath.Join(p.Paths.Run, "outputs")
	if err := os.MkdirAll(p.OutputRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p.OutputRoot, "checkpoint.pt"), []byte(strings.Repeat("x", 1000)), 0o600); err != nil {
		t.Fatal(err)
	}
	p.Options = Options{MaxBytes: 250, DryRun: true}
	r, err := p.Prune(context.Background())
	if err != nil || r.TotalBytes != 100 || len(r.Jobs) != 0 {
		t.Fatalf("训练产物不应计入清理配额: %+v %v", r, err)
	}
}

func TestPruneRejectsOutputOverlapAndKeepsRecord(t *testing.T) {
	p := newPruner(t)
	j := addJob(t, p, 120, store.StatusSucceeded)
	j.OutputDir = filepath.Join(p.Paths.Run, fmt.Sprint(j.ID), "产物")
	if err := os.MkdirAll(j.OutputDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j.OutputDir, "model.pt"), []byte("不可删除"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := p.Store.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Prune(context.Background()); err == nil {
		t.Fatal("输出目录与管理目录重叠时应跳过")
	}
	if _, err := os.Stat(filepath.Join(j.OutputDir, "model.pt")); err != nil {
		t.Fatal("误删重叠的训练产物")
	}
	if got, _ := p.Store.GetJob(j.ID); got.PrunedAt != nil || got.Pruning {
		t.Fatal("被跳过的任务不应标为已清理")
	}
}

func TestPruneResumesInterruptedClaim(t *testing.T) {
	p := newPruner(t)
	j := addJob(t, p, 120, store.StatusSucceeded)
	if claimed, err := p.Store.ClaimPrune(j.ID); err != nil || !claimed {
		t.Fatalf("无法认领: %t %v", claimed, err)
	}
	if _, err := p.Store.SetStarred(j.ID, true); err != store.ErrPruning {
		t.Fatalf("清理中仍能改标星: %v", err)
	}
	if _, err := p.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	got, err := p.Store.GetJob(j.ID)
	if err != nil || got.PrunedAt == nil || got.Pruning {
		t.Fatalf("中断清理未完成: %+v %v", got, err)
	}
}

func TestPruneSymlinkCannotEscape(t *testing.T) {
	p := newPruner(t)
	j := addJob(t, p, 120, store.StatusSucceeded)
	outside := t.TempDir()
	file := filepath.Join(outside, "model.pt")
	if err := os.WriteFile(file, []byte("产物"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.Paths.Run, fmt.Sprint(j.ID))); err != nil {
		t.Skipf("当前系统不能创建符号链接: %v", err)
	}
	if _, err := p.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file); err != nil {
		t.Fatal("清理越过了符号链接")
	}
}

// 快照重跑在 run/<id>/code/ 里用相对路径写出的产物不清理，也不计入总量；
// 恢复出来的代码、指回工作目录的链接和包装脚本照常清理
func TestPruneKeepsArtifactsOfSnapshotRerun(t *testing.T) {
	p := newPruner(t)
	work := t.TempDir()
	j := &store.Job{Name: "重跑", Workdir: work, Cmd: "train", Status: store.StatusSucceeded}
	if err := p.Store.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	ended := time.Now().Add(-120 * 24 * time.Hour).Unix()
	j.EndedAt = &ended
	if err := p.Store.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	put := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	id := fmt.Sprint(j.ID)
	put(filepath.Join(p.Paths.Logs, id+".log"), strings.Repeat("x", 100))
	meta, _ := json.Marshal(snapshot.Meta{Files: []snapshot.FileEntry{{Path: "train.py"}, {Path: "cfg/a.yaml"}, {Path: "src/model.py"}}})
	put(filepath.Join(p.Paths.Snapshots, id+".meta.json"), string(meta))
	put(filepath.Join(p.Paths.Snapshots, id+".tar.gz"), "tar")
	run := filepath.Join(p.Paths.Run, id)
	code := filepath.Join(run, "code")
	put(filepath.Join(run, "run.sh"), "wrapper")
	put(filepath.Join(code, "train.py"), "print(1)")
	put(filepath.Join(code, "cfg/a.yaml"), "lr: 1")
	put(filepath.Join(code, "src/model.py"), "model")
	put(filepath.Join(work, "data/sample.bin"), "数据")
	if err := os.Symlink(filepath.Join(work, "data"), filepath.Join(code, "data")); err != nil {
		t.Skipf("当前系统不能创建符号链接: %v", err)
	}
	put(filepath.Join(code, "checkpoints/best.pt"), strings.Repeat("m", 1000))
	if err := os.Symlink("best.pt", filepath.Join(code, "checkpoints/latest.pt")); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(code, "cfg/out.json"), strings.Repeat("o", 1000))

	managed := int64(100 + len(meta) + len("tar") + len("wrapper") + len("print(1)") + len("lr: 1") + len("model"))
	p.Options = Options{KeepDays: 90, DryRun: true}
	r, err := p.Prune(context.Background())
	if err != nil || r.TotalBytes != managed || len(r.Jobs) != 1 || r.Jobs[0].Bytes != managed {
		t.Fatalf("产物不应计入总量: 期望 %d，得到 %+v %v", managed, r, err)
	}
	p.Options.DryRun = false
	if _, err := p.Prune(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"checkpoints/best.pt", "checkpoints/latest.pt", "cfg/out.json"} {
		if _, err := os.Lstat(filepath.Join(code, rel)); err != nil {
			t.Fatalf("误删产物 %s: %v", rel, err)
		}
	}
	for _, path := range []string{filepath.Join(run, "run.sh"), filepath.Join(code, "train.py"), filepath.Join(code, "cfg/a.yaml"),
		filepath.Join(code, "src"), filepath.Join(code, "data"), filepath.Join(p.Paths.Snapshots, id+".meta.json")} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatalf("%s 应该已清理: %v", path, err)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "data/sample.bin")); err != nil {
		t.Fatal("删链接时动了工作目录里的文件")
	}
	if got, _ := p.Store.GetJob(j.ID); got.PrunedAt == nil {
		t.Fatal("清理状态未记录")
	}
}
