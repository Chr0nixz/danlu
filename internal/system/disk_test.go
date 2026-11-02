package system

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestCollectDisksDeduplicatesAndKeepsErrors(t *testing.T) {
	stat := func(path string) (string, uint64, uint64, error) {
		if path == "/broken" {
			return "", 0, 0, errors.New("无法读取")
		}
		if path == "/other" {
			return "2", 100, 10, nil
		}
		return "1", 100, 80, nil
	}
	disks := collectDisks([]string{"/data", "/outputs", "/data", "/other", "/broken"}, 20, stat)
	if len(disks) != 3 || !disks[0].Data || len(disks[0].Paths) != 2 || disks[0].Warn || !disks[1].Warn || disks[2].Error == "" {
		t.Fatalf("分区采集或合并错误: %+v", disks)
	}
	copy := cloneDisks(disks)
	copy[0].Paths[0] = "changed"
	if disks[0].Paths[0] != "/data" {
		t.Fatal("克隆后仍共用目录列表")
	}
}

func TestStuckMountDoesNotBlockDataDisk(t *testing.T) {
	release := make(chan struct{})
	var calls atomic.Int32
	stat := boundedStat(func(path string) (string, uint64, uint64, error) {
		if path == "/nfs" {
			calls.Add(1)
			<-release
		}
		return path, 100, 50, nil
	}, 20*time.Millisecond)
	start := time.Now()
	disks := collectDisks([]string{"/data", "/nfs"}, 0, stat)
	if time.Since(start) > time.Second || !disks[0].Data || disks[0].Error != "" || disks[1].Error == "" {
		t.Fatalf("卡住的目录拖住了数据目录或没有报错: %+v", disks)
	}
	// 上一次查询还卡着时直接报错，不再叠加新的 statfs。
	if disks = collectDisks([]string{"/data", "/nfs"}, 0, stat); disks[1].Error == "" || calls.Load() != 1 {
		t.Fatalf("卡住期间重复查询: %d 次, %+v", calls.Load(), disks)
	}
	close(release)
	deadline := time.Now().Add(time.Second)
	for disks[1].Error != "" {
		if time.Now().After(deadline) {
			t.Fatal("恢复后仍然报错")
		}
		time.Sleep(5 * time.Millisecond)
		disks = collectDisks([]string{"/data", "/nfs"}, 0, stat)
	}
}

func TestDiskExistingParent(t *testing.T) {
	dir := t.TempDir()
	got, err := existingParent(filepath.Join(dir, "not-created", "outputs"))
	if err != nil || got != dir {
		t.Fatalf("查找最近父目录 = %q, %v", got, err)
	}
	if err := os.Mkdir(filepath.Join(dir, "created"), 0o700); err != nil {
		t.Fatal(err)
	}
	got, err = existingParent(filepath.Join(dir, "created"))
	if err != nil || got != filepath.Join(dir, "created") {
		t.Fatalf("现有目录 = %q, %v", got, err)
	}
}
