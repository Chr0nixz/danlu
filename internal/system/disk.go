package system

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"danlu/internal/sample"
)

type Disk struct {
	ID             string   `json:"id"`
	Path           string   `json:"path"`
	Paths          []string `json:"paths"`
	TotalBytes     uint64   `json:"total_bytes"`
	AvailableBytes uint64   `json:"available_bytes"`
	Data           bool     `json:"data"`
	Warn           bool     `json:"warn"`
	Error          string   `json:"error,omitempty"`
}

type DiskMonitor = sample.Sampler[[]Disk]

func NewDiskMonitor(dataDir, outputRoot string, recent func() ([]string, error), warnBytes uint64) *DiskMonitor {
	stat := boundedStat(diskStats, statTimeout)
	query := func(context.Context) ([]Disk, error) {
		paths := []string{dataDir, outputRoot}
		dirs, err := recent()
		if err != nil {
			return nil, fmt.Errorf("读取磁盘监测目录失败: %w", err)
		}
		paths = append(paths, dirs...)
		return collectDisks(paths, warnBytes, stat), nil
	}
	return &DiskMonitor{Name: "磁盘数据", Every: time.Minute, Stale: 3 * time.Minute, Query: query, Clone: cloneDisks}
}

// statTimeout 限制单个目录的查询：网络存储卡住时 statfs 可能永远不返回。
const statTimeout = 5 * time.Second

type diskStatFunc func(string) (id string, total, available uint64, err error)

type diskStat struct {
	id               string
	total, available uint64
	err              error
}

// boundedStat 给查询加超时。超时的查询留在后台等它返回，期间同一目录直接报错，不叠加新的查询。
func boundedStat(stat diskStatFunc, timeout time.Duration) diskStatFunc {
	var mu sync.Mutex
	busy := map[string]bool{}
	return func(path string) (string, uint64, uint64, error) {
		mu.Lock()
		if busy[path] {
			mu.Unlock()
			return "", 0, 0, errors.New("上一次查询仍未返回，可能是网络存储卡住了")
		}
		busy[path] = true
		mu.Unlock()
		done := make(chan diskStat, 1)
		go func() {
			var r diskStat
			r.id, r.total, r.available, r.err = stat(path)
			mu.Lock()
			delete(busy, path)
			mu.Unlock()
			done <- r
		}()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		select {
		case r := <-done:
			return r.id, r.total, r.available, r.err
		case <-timer.C:
			return "", 0, 0, fmt.Errorf("查询超过 %s 未返回，可能是网络存储卡住了", timeout)
		}
	}
}

// collectDisks 并行查询各目录，一个目录卡住不会拖住其他分区，尤其是调度要看的数据目录。
func collectDisks(paths []string, warn uint64, stat diskStatFunc) []Disk {
	stats := map[string]*diskStat{}
	var wg sync.WaitGroup
	for _, path := range paths {
		if _, ok := stats[path]; ok {
			continue
		}
		r := &diskStat{}
		stats[path] = r
		wg.Go(func() { r.id, r.total, r.available, r.err = stat(path) })
	}
	wg.Wait()
	disks := []Disk{}
	for i, path := range paths {
		r := stats[path]
		if r.err != nil {
			disks = append(disks, Disk{Path: path, Paths: []string{path}, Data: i == 0, Error: r.err.Error()})
			continue
		}
		if n := slices.IndexFunc(disks, func(d Disk) bool { return d.ID == r.id && d.Error == "" }); n >= 0 {
			if !slices.Contains(disks[n].Paths, path) {
				disks[n].Paths = append(disks[n].Paths, path)
			}
			continue
		}
		disks = append(disks, Disk{ID: r.id, Path: path, Paths: []string{path}, Data: i == 0,
			TotalBytes: r.total, AvailableBytes: r.available, Warn: warn > 0 && r.available < warn})
	}
	return disks
}

// 首次启动时 output_root 可能尚不存在，沿父目录找到实际会使用的分区。
func existingParent(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for {
		_, err := os.Stat(path)
		if err == nil {
			return path, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", err
		}
		path = parent
	}
}

func cloneDisks(in []Disk) []Disk {
	out := slices.Clone(in)
	for i := range out {
		out[i].Paths = slices.Clone(out[i].Paths)
	}
	return out
}
