package system

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"danlu/internal/sample"
)

const (
	// SelfInterval 是丹炉自身占用的统计间隔。目录大小变化慢，不必跟主机采样一样每 2 秒走一遍。
	SelfInterval = 30 * time.Second
	// selfScanTimeout 限制单次统计：网络存储卡住时目录遍历可能永远不返回。
	selfScanTimeout = 30 * time.Second
)

// selfPartNames 是固定的分类顺序，面板按它排行，轮询时不跳行。
var selfPartNames = []string{"db", "logs", "snapshots", "run", "envs", "backup", "other"}

// SelfPart 是一类文件的表观大小合计。Error 只记这一类的问题（权限、符号链接），
// 不影响其它类别继续统计。
type SelfPart struct {
	Name  string `json:"name"`
	Bytes uint64 `json:"bytes"`
	Files int    `json:"files"`
	Error string `json:"error,omitempty"`
}

// SelfDB 是数据库大小：逻辑大小按页数×页大小（含空闲页），文件字节另计 WAL/SHM。
type SelfDB struct {
	FileBytes    uint64 `json:"file_bytes"` // 主文件 danlu.db
	WALBytes     uint64 `json:"wal_bytes"`
	SHMBytes     uint64 `json:"shm_bytes"`
	LogicalBytes uint64 `json:"logical_bytes"`
	FreeBytes    uint64 `json:"free_bytes"` // 空闲页，VACUUM 可回收
	PageSize     int64  `json:"page_size"`
	PageCount    int64  `json:"page_count"`
	FreePages    int64  `json:"free_pages"`
}

// SelfUsage 是数据目录自身占用的一份统计。
type SelfUsage struct {
	At         int64      `json:"-"` // 统计完成的时间；接口按它给出 self_updated_at，0 表示还没有成功过
	Path       string     `json:"path"`
	TotalBytes uint64     `json:"total_bytes"`
	Files      int        `json:"files"`
	Parts      []SelfPart `json:"parts"`
	DB         *SelfDB    `json:"db,omitempty"`
	Error      string     `json:"error,omitempty"`
	DBError    string     `json:"db_error,omitempty"`
}

// DBStats 镜像 store.DBStats：system 不依赖 store，由 main 用闭包转换注入（GPUStats 同款做法）。
type DBStats struct {
	PageCount, PageSize, FreePages int64
}

type SelfMonitor = sample.Sampler[SelfUsage]

// NewSelfMonitor 每 30 秒统计一次数据目录占用和数据库大小。
// 独立的 sampler：目录遍历卡住只让这份数据过期，不拖累 2 秒的主机采样和调度。
func NewSelfMonitor(home, dbPath string, dbStats func() (DBStats, error)) *SelfMonitor {
	scan := boundedSelfScan()
	// lastGood 只在采集 goroutine 里读写（Sampler.Run 同步调用 Query），无需加锁。
	var lastGood SelfUsage
	return &SelfMonitor{
		Name:  "丹炉占用数据",
		Hint:  "（数据目录统计可能卡住了）",
		Every: SelfInterval,
		Stale: 3 * time.Minute,
		Query: func(ctx context.Context) (SelfUsage, error) {
			u, err := scan(ctx, home, dbPath, dbStats)
			if err == nil {
				lastGood = u
				return u, nil
			}
			// 失败时返回上一份好快照而不是零值，避免零值带着新鲜时间戳进接口（ResourceMonitor 同款）。
			return lastGood, err
		},
		Clone: cloneSelfUsage,
	}
}

type selfScanFunc func(context.Context, string, string, func() (DBStats, error)) (SelfUsage, error)

// boundedSelfScan 给整次统计加超时。超时的统计留在后台等它返回，期间直接报错，不叠加新的遍历
// （boundedStat 同款）；ctx 取消时扫描内部也会尽快退出。
//
// 超时以后才做完的那次结果留着，下一次调用直接交出去：数据目录大、存储慢时一次统计可能比超时还长，
// 丢掉的话下一轮又从头扫、又超时，这份数据就永远出不来。
func boundedSelfScan() selfScanFunc {
	return boundSelfScan(scanSelfUsage, selfScanTimeout)
}

func boundSelfScan(scan selfScanFunc, timeout time.Duration) selfScanFunc {
	type result struct {
		u   SelfUsage
		err error
	}
	var mu sync.Mutex
	busy, abandoned := false, false
	var late *result
	return func(ctx context.Context, home, dbPath string, dbStats func() (DBStats, error)) (SelfUsage, error) {
		mu.Lock()
		if late != nil {
			r := *late
			late = nil
			mu.Unlock()
			return r.u, r.err
		}
		if busy {
			mu.Unlock()
			return SelfUsage{}, errors.New("上一次统计仍未返回，可能是存储卡住了")
		}
		busy, abandoned = true, false
		mu.Unlock()
		done := make(chan result, 1)
		go func() {
			u, err := scan(ctx, home, dbPath, dbStats)
			mu.Lock()
			defer mu.Unlock()
			busy = false
			if abandoned {
				late = &result{u, err} // 调用方已经不等了：留给下一次
				return
			}
			done <- result{u, err}
		}()
		timer := time.NewTimer(timeout)
		defer timer.Stop()
		var giveUp error
		select {
		case r := <-done:
			return r.u, r.err
		case <-timer.C:
			giveUp = fmt.Errorf("统计超过 %s 未返回，可能是存储卡住了", timeout)
		case <-ctx.Done():
			giveUp = ctx.Err()
		}
		mu.Lock()
		defer mu.Unlock()
		select {
		case r := <-done: // 正好在这时做完
			return r.u, r.err
		default:
			abandoned = true
			return SelfUsage{}, giveUp
		}
	}
}

// scanSelfUsage 走一遍数据目录，按类别统计表观大小。
// 口径：WalkDir 不跟随符号链接（链接计自身大小，与 retention 的 sizeOf 一致）；
// 硬链接只计一次（快照重跑会在 snapshots/ 里建硬链接，朴素求和会重复）；
// 任务写在运行目录里的产物也计入——它们真实占着数据目录的磁盘。
// 子树读失败只记入对应类别；顶层读不到或 ctx 取消才整体报错。
func scanSelfUsage(ctx context.Context, home, dbPath string, dbStats func() (DBStats, error)) (SelfUsage, error) {
	entries, err := os.ReadDir(home)
	if err != nil {
		return SelfUsage{}, fmt.Errorf("读取数据目录失败: %w", err)
	}
	u := SelfUsage{Path: home}
	parts := make(map[string]*SelfPart, len(selfPartNames))
	for _, name := range selfPartNames {
		parts[name] = &SelfPart{Name: name}
	}
	seen := map[hardlinkKey]bool{}
	dbBase := filepath.Base(dbPath)
	var dbFile, walFile, shmFile uint64

	addFile := func(p *SelfPart, fi fs.FileInfo) {
		if fi.Mode().IsRegular() {
			if key, ok := hardlinkID(fi); ok {
				if seen[key] {
					return
				}
				seen[key] = true
			}
		}
		p.Bytes += uint64(fi.Size())
		p.Files++
	}

	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return SelfUsage{}, err
		}
		name := e.Name()
		switch {
		case name == dbBase || name == dbBase+"-wal" || name == dbBase+"-shm" || name == dbBase+"-journal":
			// 数据库文件单独归类；WAL/SHM 在检查点或干净关闭后可能不在，读不到就计 0。
			fi, err := e.Info()
			if err != nil {
				continue
			}
			addFile(parts["db"], fi)
			switch name {
			case dbBase:
				dbFile = uint64(fi.Size())
			case dbBase + "-wal":
				walFile = uint64(fi.Size())
			case dbBase + "-shm":
				shmFile = uint64(fi.Size())
			}
		case name == "logs" || name == "snapshots" || name == "run" || name == "envs" || name == "backup":
			p := parts[name]
			if e.Type()&fs.ModeSymlink != 0 {
				// retention 拒绝清理符号链接的管理目录；统计同样不跟随，写明原因。
				p.Error = "是符号链接，未统计指向的内容"
				continue
			}
			b, f, err := walkSize(ctx, home, name, seen)
			p.Bytes += b
			p.Files += f
			if err != nil {
				if ctx.Err() != nil {
					return SelfUsage{}, ctx.Err()
				}
				p.Error = err.Error()
			}
		default:
			p := parts["other"]
			b, f, err := walkSize(ctx, home, name, seen)
			p.Bytes += b
			p.Files += f
			if err != nil {
				if ctx.Err() != nil {
					return SelfUsage{}, ctx.Err()
				}
				p.Error = err.Error()
			}
		}
	}

	for _, name := range selfPartNames {
		p := parts[name]
		u.TotalBytes += p.Bytes
		u.Files += p.Files
		u.Parts = append(u.Parts, *p)
	}
	var errs []string
	for _, p := range u.Parts {
		if p.Error != "" {
			errs = append(errs, p.Name+": "+p.Error)
		}
	}
	u.Error = strings.Join(errs, "；")

	if dbStats != nil {
		st, err := dbStats()
		if err != nil {
			u.DBError = err.Error()
		} else {
			db := &SelfDB{
				FileBytes: dbFile, WALBytes: walFile, SHMBytes: shmFile,
				PageSize: st.PageSize, PageCount: st.PageCount, FreePages: st.FreePages,
			}
			if st.PageCount > 0 && st.PageSize > 0 {
				db.LogicalBytes = uint64(st.PageCount) * uint64(st.PageSize)
			}
			if st.FreePages > 0 && st.PageSize > 0 {
				db.FreeBytes = uint64(st.FreePages) * uint64(st.PageSize)
			}
			u.DB = db
		}
	}
	u.At = time.Now().Unix()
	return u, nil
}

// walkSize 统计 root/rel 下的表观大小。rel 是文件时 WalkDir 只回调它自己。
// 每 256 个条目检查一次 ctx，关停时尽快退出。
func walkSize(ctx context.Context, root, rel string, seen map[hardlinkKey]bool) (bytes uint64, files int, err error) {
	n := 0
	err = filepath.WalkDir(filepath.Join(root, rel), func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil // 遍历途中被删掉了（清理、任务结束）：跳过，不让整类统计中止、曲线假下跌
		}
		if err != nil {
			return err
		}
		n++
		if n%256 == 0 {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}
		}
		if d.IsDir() {
			return nil
		}
		fi, err := d.Info()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if fi.Mode().IsRegular() {
			if key, ok := hardlinkID(fi); ok {
				if seen[key] {
					return nil
				}
				seen[key] = true
			}
		}
		bytes += uint64(fi.Size())
		files++
		return nil
	})
	return bytes, files, err
}

func cloneSelfUsage(in SelfUsage) SelfUsage {
	out := in
	out.Parts = append([]SelfPart(nil), in.Parts...)
	if in.DB != nil {
		db := *in.DB
		out.DB = &db
	}
	return out
}
