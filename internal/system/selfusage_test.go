package system

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func writeSized(t *testing.T, path string, size int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatal(err)
	}
}

func selfPart(t *testing.T, u SelfUsage, name string) SelfPart {
	t.Helper()
	for _, p := range u.Parts {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("统计缺少分类 %s: %+v", name, u.Parts)
	return SelfPart{}
}

// 分类、文件数、数据库详情和总量的基本口径。
func TestSelfUsageScan(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(home, "danlu.db")
	writeSized(t, db, 100)
	writeSized(t, db+"-wal", 200)
	writeSized(t, db+"-shm", 32)
	writeSized(t, filepath.Join(home, "logs", "1.log"), 1000)
	writeSized(t, filepath.Join(home, "logs", "2.log"), 2000)
	writeSized(t, filepath.Join(home, "snapshots", "1.tar.gz"), 500)
	writeSized(t, filepath.Join(home, "run", "1", "run.sh"), 100)
	writeSized(t, filepath.Join(home, "run", "1", "code", "x.py"), 50)
	writeSized(t, filepath.Join(home, "envs", "abc.txt"), 64)
	writeSized(t, filepath.Join(home, "backup", "danlu-old.db"), 400)
	writeSized(t, filepath.Join(home, "tls", "cert.pem"), 10)
	writeSized(t, filepath.Join(home, "config.toml"), 20)
	writeSized(t, filepath.Join(home, "secret.key"), 16)
	writeSized(t, filepath.Join(home, "serve.lock"), 0)
	writeSized(t, filepath.Join(home, "unknown-dir", "file"), 7)

	u, err := scanSelfUsage(context.Background(), home, db, func() (DBStats, error) {
		return DBStats{PageCount: 10, PageSize: 4096, FreePages: 2}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.At <= 0 || u.Path != home || u.Error != "" || u.DBError != "" {
		t.Fatalf("基本字段不正确: %+v", u)
	}
	want := map[string]struct {
		bytes uint64
		files int
	}{
		"db":        {332, 3},
		"logs":      {3000, 2},
		"snapshots": {500, 1},
		"run":       {150, 2},
		"envs":      {64, 1},
		"backup":    {400, 1},
		"other":     {53, 5}, // tls + config + secret + serve.lock + 未知目录
	}
	var total uint64
	var files int
	for name, w := range want {
		p := selfPart(t, u, name)
		if p.Bytes != w.bytes || p.Files != w.files || p.Error != "" {
			t.Errorf("%s = %+v，想要 %d 字节 %d 个文件", name, p, w.bytes, w.files)
		}
		total += w.bytes
		files += w.files
	}
	if u.TotalBytes != total || u.Files != files {
		t.Fatalf("总量 = %d 字节 %d 个文件，想要 %d / %d", u.TotalBytes, u.Files, total, files)
	}
	if u.DB == nil || u.DB.FileBytes != 100 || u.DB.WALBytes != 200 || u.DB.SHMBytes != 32 ||
		u.DB.LogicalBytes != 40960 || u.DB.FreeBytes != 8192 || u.DB.PageCount != 10 || u.DB.FreePages != 2 {
		t.Fatalf("数据库详情不正确: %+v", u.DB)
	}
	// 分类顺序固定，面板按它排行
	for i, name := range selfPartNames {
		if u.Parts[i].Name != name {
			t.Fatalf("分类顺序不对: %+v", u.Parts)
		}
	}
}

// 数据库统计失败只影响 DB 详情，目录统计照常。
func TestSelfUsageDBStatsError(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(home, "danlu.db")
	writeSized(t, db, 100)
	writeSized(t, filepath.Join(home, "logs", "1.log"), 10)
	u, err := scanSelfUsage(context.Background(), home, db, func() (DBStats, error) {
		return DBStats{}, errors.New("数据库被锁住")
	})
	if err != nil {
		t.Fatal(err)
	}
	if u.DB != nil || u.DBError == "" {
		t.Fatalf("DB 统计失败时应只有 DBError: %+v", u)
	}
	if selfPart(t, u, "logs").Bytes != 10 || selfPart(t, u, "db").Bytes != 100 {
		t.Fatalf("目录统计不应受 DB 失败影响: %+v", u.Parts)
	}
}

// WAL/SHM 不在（干净关闭后）时计 0，不报错。
func TestSelfUsageNoWAL(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(home, "danlu.db")
	writeSized(t, db, 100)
	u, err := scanSelfUsage(context.Background(), home, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if selfPart(t, u, "db").Bytes != 100 || u.DB != nil {
		t.Fatalf("没有 WAL 时 db 分类应只有主文件: %+v", u.Parts)
	}
}

func TestSelfUsageMissingHome(t *testing.T) {
	_, err := scanSelfUsage(context.Background(), filepath.Join(t.TempDir(), "不存在"), "x.db", nil)
	if err == nil {
		t.Fatal("数据目录不存在时应报错")
	}
}

func TestSelfUsageCanceled(t *testing.T) {
	home := t.TempDir()
	writeSized(t, filepath.Join(home, "logs", "1.log"), 10)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanSelfUsage(ctx, home, filepath.Join(home, "danlu.db"), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ctx 取消应报 context.Canceled: %v", err)
	}
}

// 管理目录本身是符号链接时不跟随，写明原因（与 retention 拒绝清理的语义一致）。
func TestSelfUsageSymlinkedPart(t *testing.T) {
	home := t.TempDir()
	outside := t.TempDir()
	writeSized(t, filepath.Join(outside, "big.log"), 10000)
	if err := os.Symlink(outside, filepath.Join(home, "logs")); err != nil {
		t.Skipf("创建符号链接失败（权限不足）: %v", err)
	}
	writeSized(t, filepath.Join(home, "config.toml"), 20)
	u, err := scanSelfUsage(context.Background(), home, filepath.Join(home, "danlu.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	p := selfPart(t, u, "logs")
	if p.Error == "" || p.Bytes != 0 {
		t.Fatalf("符号链接的 logs 应记错误且不跟随: %+v", p)
	}
	if u.TotalBytes != 20 || u.Error == "" {
		t.Fatalf("外部文件不应计入总量: %+v", u)
	}
}

// 失败后返回上一份好快照，而不是带新鲜时间戳的零值。
func TestSelfMonitorKeepsLastGoodOnError(t *testing.T) {
	home := t.TempDir()
	db := filepath.Join(home, "danlu.db")
	writeSized(t, db, 100)
	writeSized(t, filepath.Join(home, "logs", "1.log"), 1000)
	mon := NewSelfMonitor(home, db, nil)
	mon.Refresh(context.Background())
	u, _, err := mon.Latest()
	if err != nil || u.TotalBytes != 1100 || u.At <= 0 {
		t.Fatalf("首次统计应成功: %+v %v", u, err)
	}
	if err := os.RemoveAll(home); err != nil {
		t.Fatal(err)
	}
	mon.Refresh(context.Background())
	u, _, err = mon.Latest()
	if err == nil {
		t.Fatal("目录没了应报错")
	}
	if u.TotalBytes != 1100 || u.At <= 0 {
		t.Fatalf("失败后应保留上一份好快照: %+v", u)
	}
	// Clone：调用方改返回值不影响缓存
	u.Parts[0].Bytes = 0
	u2, _, _ := mon.Latest()
	if u2.Parts[0].Bytes == 0 {
		t.Fatal("Latest 应返回副本")
	}
}

// 超时以后才做完的统计留给下一次：一次统计比超时还长时，数据照样能出来，不会每轮从头扫、每轮超时
func TestSelfScanKeepsLateResult(t *testing.T) {
	release := make(chan struct{})
	calls := 0
	scan := boundSelfScan(func(context.Context, string, string, func() (DBStats, error)) (SelfUsage, error) {
		calls++
		<-release
		return SelfUsage{TotalBytes: 42}, nil
	}, 50*time.Millisecond)
	if _, err := scan(context.Background(), "", "", nil); err == nil {
		t.Fatal("超时应报错")
	}
	if _, err := scan(context.Background(), "", "", nil); err == nil || !strings.Contains(err.Error(), "仍未返回") {
		t.Fatalf("上一次还没回来时不应再起一次: %v", err)
	}
	close(release)
	deadline := time.Now().Add(2 * time.Second)
	for {
		u, err := scan(context.Background(), "", "", nil)
		if err == nil {
			if u.TotalBytes != 42 || calls != 1 {
				t.Fatalf("应交出迟到的那次结果，不重新统计: %+v，统计了 %d 次", u, calls)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("迟到的结果没有交出来: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// 遍历途中文件被删掉（清理、任务结束）不让整类统计中止
func TestWalkSizeSkipsVanishedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "logs", "1.log"), []byte("12345"), 0o644); err != nil {
		t.Fatal(err)
	}
	b, f, err := walkSize(context.Background(), root, "gone", map[hardlinkKey]bool{})
	if err != nil || b != 0 || f != 0 {
		t.Fatalf("不存在的路径应计 0、不报错: %d %d %v", b, f, err)
	}
	if b, f, err := walkSize(context.Background(), root, "logs", map[hardlinkKey]bool{}); err != nil || b != 5 || f != 1 {
		t.Fatalf("统计不对: %d %d %v", b, f, err)
	}
}
