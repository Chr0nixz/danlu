// Package retention 清理丹炉管理的任务文件，保留数据库记录和所有训练产物。
package retention

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"danlu/internal/config"
	"danlu/internal/snapshot"
	"danlu/internal/store"
)

type Options struct {
	KeepDays  int64
	MaxBytes  int64
	OlderThan *time.Duration
	DryRun    bool
}

type Entry struct {
	JobID  int64  `json:"job_id"`
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	Reason string `json:"reason"`
	// ReasonCode 供前端本地化（resume/age/size）；Reason 仍是中文原文，旧客户端回退用
	ReasonCode string `json:"reason_code,omitempty"`
	Pruned     bool   `json:"pruned"`
}

type Report struct {
	DryRun     bool     `json:"dry_run"`
	Jobs       []Entry  `json:"jobs"`
	Envs       []string `json:"envs"`
	TotalBytes int64    `json:"total_bytes"`
	Bytes      int64    `json:"bytes"`
	Errors     []string `json:"errors"`
}

// ErrBusy 表示另一次清理（自动清理、danlu prune 或预览）正拿着清理锁。
var ErrBusy = errors.New("另一个清理正在运行")

type Pruner struct {
	Store      *store.Store
	Paths      config.Paths
	OutputRoot string
	Options    Options
	// Live 不为 nil 时，每次清理前取一次保留天数和总量上限（字节），盖住 Options 里的同名项：
	// 面板上可以随时改。按 OlderThan 手动清理时不用它。
	Live func() (keepDays, maxBytes int64)

	kickOnce sync.Once
	kick     chan struct{}
	mu       sync.Mutex
	last     *Status
}

// Status 是最近一次自动清理的结果，设置页「数据保留」里显示。
type Status struct {
	At    int64  `json:"at"`
	Jobs  int    `json:"jobs"`
	Envs  int    `json:"envs"`
	Bytes int64  `json:"bytes"`
	Error string `json:"error,omitempty"`
}

func (p *Pruner) kickCh() chan struct{} {
	p.kickOnce.Do(func() { p.kick = make(chan struct{}, 1) })
	return p.kick
}

// Kick 让 Run 马上再清理一次，不等 6 小时。面板上收紧保留策略后调用。
func (p *Pruner) Kick() {
	select {
	case p.kickCh() <- struct{}{}:
	default:
	}
}

// Last 返回最近一次自动清理的结果；服务启动后还没清理过（或策略关闭）时 ok 为 false。
func (p *Pruner) Last() (Status, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.last == nil {
		return Status{}, false
	}
	return *p.last, true
}

func (p *Pruner) Run(ctx context.Context) {
	tick := time.NewTicker(6 * time.Hour)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		opt := p.effective()
		if opt.KeepDays != 0 || opt.MaxBytes != 0 {
			r, err := p.prune(ctx, opt)
			st := Status{At: time.Now().Unix(), Jobs: len(r.Jobs), Envs: len(r.Envs), Bytes: r.Bytes}
			if err != nil {
				st.Error = err.Error()
				log.Printf("自动清理失败: %v", err)
			} else if r.Bytes > 0 {
				log.Printf("自动清理完成：%d 个任务，%d 字节", len(r.Jobs), r.Bytes)
			}
			if ctx.Err() == nil {
				p.mu.Lock()
				p.last = &st
				p.mu.Unlock()
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-p.kickCh():
		}
	}
}

// effective 是这一次清理实际用的选项。
func (p *Pruner) effective() Options {
	opt := p.Options
	if p.Live != nil && opt.OlderThan == nil {
		opt.KeepDays, opt.MaxBytes = p.Live()
	}
	return opt
}

// Preview 按给定的保留天数和总量上限（字节）算一遍会清理什么，不删任何东西。
// 和自动清理共用清理锁，正在清理时返回 ErrBusy。
//
// ctx 结束时立即返回 ctx.Err()，不等统计做完：存储卡住时，一次 lstat 也可能迟迟不返回，
// ctx 打断不了它。统计在后台遇到下一个检查点就停下，然后释放清理锁；预览不删东西，丢下不管是安全的。
func (p *Pruner) Preview(ctx context.Context, keepDays, maxBytes int64) (Report, error) {
	type result struct {
		r   Report
		err error
	}
	done := make(chan result, 1)
	go func() {
		r, err := p.prune(ctx, Options{KeepDays: keepDays, MaxBytes: maxBytes, DryRun: true})
		done <- result{r, err}
	}()
	select {
	case res := <-done:
		return res.r, res.err
	case <-ctx.Done():
		return Report{DryRun: true, Jobs: []Entry{}, Envs: []string{}, Errors: []string{}}, ctx.Err()
	}
}

func pathsFor(id int64) []string {
	n := strconv.FormatInt(id, 10)
	return []string{"logs/" + n + ".log", "snapshots/" + n + ".tar.gz", "snapshots/" + n + ".meta.json", "snapshots/" + n + ".diff", runPath(id)}
}

func runPath(id int64) string { return "run/" + strconv.FormatInt(id, 10) }

// restoredFiles 是快照清单里的文件，也就是快照重跑在 code/ 里恢复出来的文件，带快照时的大小。
// 清单已经不在时返回空：code/ 里剩下的普通文件都当产物保留。
func restoredFiles(root *os.Root, id int64) []snapshot.FileEntry {
	b, err := fs.ReadFile(root.FS(), "snapshots/"+strconv.FormatInt(id, 10)+".meta.json")
	if err != nil {
		return nil
	}
	var meta snapshot.Meta
	if json.Unmarshal(b, &meta) != nil {
		return nil
	}
	files := make([]snapshot.FileEntry, 0, len(meta.Files))
	for _, f := range meta.Files {
		if fs.ValidPath(f.Path) && f.Path != "." {
			files = append(files, f)
		}
	}
	return files
}

// ownRunFile 是丹炉自己写在运行目录里的文件：包装脚本、用户命令、pid、退出码、OOM 计数、
// 严格任务的环境文件，以及它们写到一半的临时文件。快照重跑的任务用 ../ 写进运行目录的东西不在其中。
func ownRunFile(name string) bool {
	switch strings.TrimSuffix(name, ".tmp") {
	case "run.sh", "cmd.sh", "pid", "exitcode", "oom_kill", "env":
		return true
	}
	return strings.HasPrefix(name, "env.") // 严格任务每次启动换名的环境文件（env.<随机后缀>）
}

// pruneRun 清理（dryRun 时只统计）一个任务的运行目录，返回其中由丹炉管理的字节数。
// 快照重跑的 code/ 里有恢复出来的代码、指回原工作目录的链接，还有任务用相对路径写出的产物
// （checkpoints/ 之类，也包括改写过的恢复文件）；运行目录里除了丹炉自己的文件，还可能有任务用 ../ 写出的产物。
// 只删恢复出来以后没动过的文件、恢复时建的链接和丹炉自己的文件；产物和装着产物的目录留下，也不计入总量。
//
// measure 决定每个文件计多少字节（见 links：统计总量用 total，算能腾出多少用 freed）。
func pruneRun(ctx context.Context, root *os.Root, id int64, workdir, home string, protected []string, measure func(fs.FileInfo) int64, dryRun bool) (int64, error) {
	name := runPath(id)
	code := name + "/code"
	fi, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	whole := !fi.IsDir() // 运行目录本身是符号链接时只删链接
	if !whole {
		ci, err := root.Lstat(code)
		if err != nil && !os.IsNotExist(err) {
			return 0, err
		}
		whole = err != nil || !ci.IsDir() // 不是快照重跑：整个运行目录都是丹炉写的
	}
	if whole {
		size, err := sizeOf(ctx, root, name, home, protected, measure)
		if err != nil || dryRun {
			return size, err
		}
		return size, root.RemoveAll(name)
	}

	// 包装脚本由丹炉在恢复代码之后、任务开始之前写出（每次重新启动都重写），它的修改时间就是
	// "恢复以后有没有被任务动过"的分界：改写过的恢复文件是这次运行的结果，要当产物留下。
	// 分界要在删掉包装脚本之前取，所以 code/ 先清，运行目录里丹炉自己的文件最后删。
	// 没有包装脚本（任务没走到启动，或者旧版清理删到一半）时分不清，code/ 里的文件都留下。
	var size int64
	if wrapper, err := root.Lstat(name + "/run.sh"); err == nil && wrapper.Mode().IsRegular() {
		boundary := wrapper.ModTime()
		checked := map[string]bool{}
		for _, f := range restoredFiles(root, id) {
			if err := ctx.Err(); err != nil {
				return size, err
			}
			p := code + "/" + f.Path
			if isProtected(home, p, protected) || !realParents(root, code, f.Path, checked) {
				continue
			}
			fi, err := root.Lstat(p)
			if err != nil || !fi.Mode().IsRegular() || fi.Size() != f.Size || fi.ModTime().After(boundary) {
				continue // 已经不在、被任务换成了别的，或者被任务改写过：都留下
			}
			size += measure(fi)
			if !dryRun {
				if err := root.Remove(p); err != nil && !os.IsNotExist(err) {
					return size, err
				}
			}
		}
	}
	entries, err := fs.ReadDir(root.FS(), name)
	if err != nil {
		return size, err
	}
	var own []string
	for _, e := range entries {
		if !ownRunFile(e.Name()) || e.IsDir() {
			continue // code/ 和任务用 ../ 写进来的东西
		}
		child := name + "/" + e.Name()
		n, err := sizeOf(ctx, root, child, home, protected, measure)
		if err != nil {
			return size, err
		}
		size += n
		own = append(own, child)
	}
	if dryRun {
		return size, nil
	}
	// 指回原工作目录的链接是恢复时建的；任务自己建的链接指向别处，留下。WalkDir 不跟随链接
	var dirs []string
	err = fs.WalkDir(root.FS(), code, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, p)
			return nil
		}
		if d.Type()&fs.ModeSymlink == 0 {
			return nil
		}
		target, err := root.Readlink(p)
		if err != nil || target != filepath.Join(workdir, filepath.FromSlash(strings.TrimPrefix(p, code+"/"))) {
			return nil
		}
		return root.Remove(p)
	})
	if err != nil {
		return size, err
	}
	// 从里往外删空目录；还装着产物的目录删不掉，正好留下
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = root.Remove(dirs[i])
	}
	for _, child := range own {
		if err := root.Remove(child); err != nil && !os.IsNotExist(err) {
			return size, err
		}
	}
	_ = root.Remove(name) // 还有任务写进来的东西时删不掉，留下
	return size, nil
}

// realParents 从 code 往下逐级确认 rel 的每一级父目录都是真目录，不是链接。
// Root 只拦住逃出数据目录的链接，数据目录里面的相对链接照样跟随：任务把 code/cfg 换成
// ../../<另一个任务>/code/cfg 时，不先确认就会删到另一个任务的文件。checked 记下查过的目录及结果。
func realParents(root *os.Root, code, rel string, checked map[string]bool) bool {
	dir := code
	parts := strings.Split(rel, "/")
	for _, part := range parts[:len(parts)-1] {
		dir += "/" + part
		ok, seen := checked[dir]
		if !seen {
			fi, err := root.Lstat(dir) // 上一级已经确认是真目录，这里只看这一级本身
			ok = err == nil && fi.IsDir()
			checked[dir] = ok
		}
		if !ok {
			return false
		}
	}
	return true
}

// outputCheckTimeout 限制解析输出目录的时间。
const outputCheckTimeout = 30 * time.Second

// canonicalOutputs 解析各输出目录里的符号链接，跳过空路径。输出目录常在网络存储上，
// 存储卡住时 lstat 不会返回，ctx 也打断不了，所以放在单独的 goroutine 里，最多等
// outputCheckTimeout，ctx 结束也不再等：清理锁照常释放，停止服务也不会被它拖住。
func canonicalOutputs(ctx context.Context, paths []string) ([]string, error) {
	type result struct {
		paths []string
		err   error
	}
	done := make(chan result, 1)
	go func() {
		var out []string
		for _, path := range paths {
			if path == "" {
				continue
			}
			c, err := canonical(path)
			if err != nil {
				done <- result{err: fmt.Errorf("核对输出目录失败: %w", err)}
				return
			}
			out = append(out, c)
		}
		done <- result{paths: out}
	}()
	timer := time.NewTimer(outputCheckTimeout)
	defer timer.Stop()
	select {
	case r := <-done:
		return r.paths, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, fmt.Errorf("核对输出目录超过 %d 秒没有完成，存储可能卡住", int(outputCheckTimeout/time.Second))
	}
}

// canonical 也支持尚未创建或已经清掉的路径；解析最近的现有父目录里的符号链接。
func canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	var suffix []string
	for {
		resolved, err := filepath.EvalSymlinks(abs)
		if err == nil {
			for i := len(suffix) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, suffix[i])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return "", err
		}
		suffix = append(suffix, filepath.Base(abs))
		abs = parent
	}
}

func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func checkProtected(home, rel string, protected []string) error {
	target := filepath.Join(home, filepath.FromSlash(rel))
	for _, output := range protected {
		if inside(target, output) || inside(output, target) {
			return fmt.Errorf("清理路径 %s 与输出目录 %s 重叠，已跳过", rel, output)
		}
	}
	return nil
}

// runTotal 统计 run/ 里由丹炉管理的字节数，快照重跑写在 code/ 里的产物不算
func runTotal(ctx context.Context, root *os.Root, home string, protected []string, measure func(fs.FileInfo) int64) (int64, error) {
	entries, err := fs.ReadDir(root.FS(), "run")
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		name := "run/" + e.Name()
		id, parseErr := strconv.ParseInt(e.Name(), 10, 64)
		var n int64
		if parseErr == nil && runPath(id) == name {
			n, err = pruneRun(ctx, root, id, "", home, protected, measure, true)
		} else {
			n, err = sizeOf(ctx, root, name, home, protected, measure)
		}
		if err != nil {
			return total, err
		}
		total += n
	}
	return total, nil
}

// isProtected 判断数据目录里的 rel 是否落在某个输出目录里
func isProtected(home, rel string, protected []string) bool {
	full := filepath.Join(home, filepath.FromSlash(rel))
	for _, output := range protected {
		if inside(full, output) {
			return true
		}
	}
	return false
}

// uncounted 用在真正删除那一遍：能腾出多少已经在之前的统计里算过了
func uncounted(fs.FileInfo) int64 { return 0 }

// sizeOf 统计 name 里不在输出目录内的字节数，每个文件按 measure 计（硬链接见 links）。每个目录项都检查一次 ctx：
// 目录很大或存储很慢时，预览超时和停止服务都能让它停下。
func sizeOf(ctx context.Context, root *os.Root, name, home string, protected []string, measure func(fs.FileInfo) int64) (int64, error) {
	excluded := func(rel string) bool { return isProtected(home, rel, protected) }
	if excluded(name) {
		return 0, nil
	}
	fi, err := root.Lstat(name)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if !fi.IsDir() {
		return measure(fi), nil
	}
	var size int64
	err = fs.WalkDir(root.FS(), name, func(rel string, d fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if os.IsNotExist(err) {
			return fs.SkipDir
		}
		if err != nil {
			return err
		}
		if excluded(rel) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if !d.IsDir() {
			fi, err := d.Info()
			if err != nil {
				return err
			}
			size += measure(fi)
		}
		return nil
	})
	return size, err
}

func (p *Pruner) Prune(ctx context.Context) (Report, error) { return p.prune(ctx, p.effective()) }

func (p *Pruner) prune(ctx context.Context, opt Options) (Report, error) {
	r := Report{DryRun: opt.DryRun, Jobs: []Entry{}, Envs: []string{}, Errors: []string{}}
	if opt.KeepDays == 0 && opt.MaxBytes == 0 && opt.OlderThan == nil {
		return r, nil
	}
	root, err := os.OpenRoot(p.Paths.Home)
	if err != nil {
		return r, err
	}
	defer root.Close()
	unlock, err := lock(root)
	if err != nil {
		return r, err
	}
	defer unlock()
	home, err := canonical(p.Paths.Home)
	if err != nil {
		return r, err
	}
	jobs, err := p.Store.RetentionJobs()
	if err != nil {
		return r, err
	}
	var protected []string
	pinnedSnapshots := map[int64]bool{}
	outputs := []string{p.OutputRoot}
	for _, j := range jobs {
		if j.CodeFrom != nil && !j.SnapshotOK && !j.Finished() {
			pinnedSnapshots[*j.CodeFrom] = true
		}
		if j.OutputDir != "" {
			outputs = append(outputs, j.OutputDir)
		}
	}
	resolved, err := canonicalOutputs(ctx, outputs)
	if err != nil {
		return r, err
	}
	for _, path := range resolved {
		if inside(path, home) || inside(home, path) {
			protected = append(protected, path)
		}
	}
	lk := newLinks()
	// 拒绝跟随管理目录自身的符号链接；Root 再约束每一次文件操作。
	for _, dir := range []string{"logs", "snapshots", "run", "envs"} {
		if err := ctx.Err(); err != nil {
			return r, err
		}
		fi, err := root.Lstat(dir)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return r, err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return r, fmt.Errorf("管理目录 %s 是符号链接，停止清理", dir)
		}
		var n int64
		if dir == "run" {
			n, err = runTotal(ctx, root, home, protected, lk.total)
		} else {
			n, err = sizeOf(ctx, root, dir, home, protected, lk.total)
		}
		if err != nil {
			return r, err
		}
		r.TotalBytes += n
	}
	now := time.Now()
	var cutoff *int64
	if opt.OlderThan != nil {
		v := now.Add(-*opt.OlderThan).Unix()
		cutoff = &v
	} else if opt.KeepDays > 0 {
		v := now.Add(-time.Duration(opt.KeepDays) * 24 * time.Hour).Unix()
		cutoff = &v
	}
	remaining := r.TotalBytes
	for _, j := range jobs {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		if !j.Finished() || j.EndedAt == nil || j.Starred || j.PrunedAt != nil || pinnedSnapshots[j.ID] || (j.Recipe != nil && j.Recipe.Pending) {
			continue
		}
		reason, reasonCode := "", ""
		if j.Pruning {
			reason, reasonCode = "继续上次中断的清理", "resume"
		} else if cutoff != nil && *j.EndedAt <= *cutoff {
			reason, reasonCode = "超过保留时长", "age"
		} else if opt.OlderThan == nil && opt.MaxBytes > 0 && remaining > opt.MaxBytes {
			reason, reasonCode = "总大小超过上限", "size"
		}
		if reason == "" {
			continue
		}
		var size int64
		var targetErr error
		lk.reset() // 上一个任务没算进清单时，它那几个链接不算删掉
		for _, name := range pathsFor(j.ID) {
			if err := checkProtected(home, name, protected); err != nil {
				targetErr = err
				break
			}
			var n int64
			var err error
			if name == runPath(j.ID) {
				n, err = pruneRun(ctx, root, j.ID, j.Workdir, home, protected, lk.freed, true)
			} else {
				n, err = sizeOf(ctx, root, name, home, protected, lk.freed)
			}
			if err != nil {
				targetErr = err
				break
			}
			size += n
		}
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		if targetErr != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("任务 #%d: %v", j.ID, targetErr))
			continue
		}
		e := Entry{JobID: j.ID, Name: j.Name, Bytes: size, Reason: reason, ReasonCode: reasonCode}
		if !opt.DryRun {
			claimed, err := p.Store.ClaimPrune(j.ID)
			if err != nil {
				return r, err
			}
			if !claimed {
				continue
			}
			// 运行目录先清：分辨 code/ 里恢复出来的文件要用快照清单，清单要留到最后再删。
			// 已经认领的任务清完再停，ctx 只在任务之间检查
			if _, err := pruneRun(context.WithoutCancel(ctx), root, j.ID, j.Workdir, home, protected, uncounted, false); err != nil {
				targetErr = err
			}
			for _, name := range pathsFor(j.ID) {
				if targetErr != nil {
					break
				}
				if name != runPath(j.ID) {
					targetErr = root.RemoveAll(name)
				}
			}
			var at *int64
			if targetErr == nil {
				ts := now.Unix()
				at = &ts
				e.Pruned = true
			}
			if err := p.Store.FinishPrune(j.ID, at); err != nil {
				return r, err
			}
			if targetErr != nil {
				r.Errors = append(r.Errors, fmt.Sprintf("任务 #%d: %v", j.ID, targetErr))
				continue
			}
		}
		lk.commit()
		r.Jobs = append(r.Jobs, e)
		r.Bytes += size
		remaining -= size
	}
	files, err := fs.ReadDir(root.FS(), "envs")
	if err != nil && !os.IsNotExist(err) {
		return r, err
	}
	for _, file := range files {
		if ctx.Err() != nil {
			return r, ctx.Err()
		}
		if !envName.MatchString(file.Name()) || file.Type()&os.ModeSymlink != 0 || file.IsDir() {
			continue
		}
		hash := strings.TrimSuffix(file.Name(), ".txt")
		used, err := p.Store.EnvInUse(hash)
		if err != nil {
			return r, err
		}
		if used {
			continue
		}
		name := "envs/" + file.Name()
		if err := checkProtected(home, name, protected); err != nil {
			r.Errors = append(r.Errors, err.Error())
			continue
		}
		fi, err := file.Info()
		if err != nil {
			return r, err
		}
		if !opt.DryRun {
			removed, err := p.Store.RemoveUnusedEnv(hash, func() error { return root.Remove(name) })
			if err != nil {
				return r, err
			}
			if !removed {
				continue
			}
		}
		r.Envs = append(r.Envs, file.Name())
		r.Bytes += fi.Size()
	}
	if len(r.Errors) > 0 {
		return r, errors.New(strings.Join(r.Errors, "；"))
	}
	return r, nil
}

var envName = regexp.MustCompile(`^[a-f0-9]{64}\.txt$`)
