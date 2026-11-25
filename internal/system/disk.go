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
	// LastID 是出错的目录上一次查询成功时所在分区的 ID（出错的条目没有 ID）。告警按分区 ID 认：
	// 没有它就找不到原来那条，一次查询出错就清掉、恢复后余量还低又推送一次
	LastID string `json:"-"`
}

func DiskSpace(path string) (total, available uint64, err error) {
	_, total, available, err = boundedDoctorDiskStats(path)
	return
}

var boundedDoctorDiskStats = boundedStat(diskStats, statTimeout)

type DiskMonitor = sample.Sampler[[]Disk]

// warnBytes 每次采样时取一次：低余量提示线可以在面板上随时改。为 nil 或返回 0 时不提示。
func NewDiskMonitor(dataDir, outputRoot string, recent func() ([]string, error), warnBytes func() uint64) *DiskMonitor {
	stat := boundedStat(diskStats, statTimeout)
	var memory diskMemory
	query := func(context.Context) ([]Disk, error) {
		paths := []string{dataDir, outputRoot}
		dirs, err := recent()
		if err != nil {
			return nil, fmt.Errorf("读取磁盘监测目录失败: %w", err)
		}
		paths = append(paths, dirs...)
		var warn uint64
		if warnBytes != nil {
			warn = warnBytes()
		}
		disks := collectDisks(paths, warn, stat)
		memory.apply(disks, warn)
		return disks, nil
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

// diskMemory 记住上一次采样，给下一次用：
//   - 出错的目录记下上次成功时的分区 ID（Disk.LastID）；
//   - 低余量提示带回差：提示过的分区要回到提示线以上一段（提示线的 5%，至少 1 GiB）才算恢复，
//     余量在线附近来回时，不会每次越线都推一条。
type diskMemory struct {
	mu     sync.Mutex
	ids    map[string]string // 目录 → 上次成功时的分区 ID
	warned map[string]bool   // 分区 ID → 上次是否在提示
}

func warnHysteresis(warn uint64) uint64 { return max(warn/20, 1<<30) }

func (m *diskMemory) apply(disks []Disk, warn uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := map[string]string{}
	warned := map[string]bool{}
	for i := range disks {
		d := &disks[i]
		if d.Error != "" {
			// 查不到就沿用上次的状态
			if id := m.ids[d.Path]; id != "" {
				d.LastID = id
				ids[d.Path] = id
				warned[id] = warned[id] || m.warned[id]
			}
			continue
		}
		for _, p := range d.Paths {
			ids[p] = d.ID
		}
		if !d.Warn && warn > 0 && m.warned[d.ID] && d.AvailableBytes < warn+warnHysteresis(warn) {
			d.Warn = true
		}
		warned[d.ID] = warned[d.ID] || d.Warn
	}
	m.ids, m.warned = ids, warned
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
