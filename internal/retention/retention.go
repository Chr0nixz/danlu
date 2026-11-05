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
	Pruned bool   `json:"pruned"`
}

type Report struct {
	DryRun     bool     `json:"dry_run"`
	Jobs       []Entry  `json:"jobs"`
	Envs       []string `json:"envs"`
	TotalBytes int64    `json:"total_bytes"`
	Bytes      int64    `json:"bytes"`
	Errors     []string `json:"errors"`
}

type Pruner struct {
	Store      *store.Store
	Paths      config.Paths
	OutputRoot string
	Options    Options
}

func (p *Pruner) Run(ctx context.Context) {
	tick := time.NewTicker(6 * time.Hour)
	defer tick.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		r, err := p.Prune(ctx)
		if err != nil {
			log.Printf("自动清理失败: %v", err)
		} else if r.Bytes > 0 {
			log.Printf("自动清理完成：%d 个任务，%d 字节", len(r.Jobs), r.Bytes)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

func pathsFor(id int64) []string {
	n := strconv.FormatInt(id, 10)
	return []string{"logs/" + n + ".log", "snapshots/" + n + ".tar.gz", "snapshots/" + n + ".meta.json", "snapshots/" + n + ".diff", runPath(id)}
}

func runPath(id int64) string { return "run/" + strconv.FormatInt(id, 10) }

// restoredFiles 是快照清单里的文件，也就是快照重跑在 code/ 里恢复出来的文件。
// 清单已经不在时返回空：code/ 里剩下的普通文件都当产物保留。
func restoredFiles(root *os.Root, id int64) []string {
	b, err := fs.ReadFile(root.FS(), "snapshots/"+strconv.FormatInt(id, 10)+".meta.json")
	if err != nil {
		return nil
	}
	var meta snapshot.Meta
	if json.Unmarshal(b, &meta) != nil {
		return nil
	}
	files := make([]string, 0, len(meta.Files))
	for _, f := range meta.Files {
		if fs.ValidPath(f.Path) && f.Path != "." {
			files = append(files, f.Path)
		}
	}
	return files
}

// pruneRun 清理（dryRun 时只统计）一个任务的运行目录，返回其中由丹炉管理的字节数。
// 快照重跑的 code/ 里有恢复出来的代码、指回原工作目录的链接，还有任务用相对路径写出的产物
// （checkpoints/ 之类）。只删前两种；产物和装着产物的目录留下，也不计入总量。
func pruneRun(root *os.Root, id int64, workdir, home string, protected []string, dryRun bool) (int64, error) {
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
		size, err := sizeOf(root, name, home, protected)
		if err != nil || dryRun {
			return size, err
		}
		return size, root.RemoveAll(name)
	}

	var size int64
	entries, err := fs.ReadDir(root.FS(), name)
	if err != nil {
		return 0, err
	}
	for _, e := range entries {
		if e.Name() == "code" {
			continue
		}
		child := name + "/" + e.Name()
		n, err := sizeOf(root, child, home, protected)
		if err != nil {
			return size, err
		}
		size += n
		if !dryRun {
			if err := root.RemoveAll(child); err != nil {
				return size, err
			}
		}
	}
	for _, rel := range restoredFiles(root, id) {
		p := code + "/" + rel
		if isProtected(home, p, protected) {
			continue
		}
		fi, err := root.Lstat(p)
		if err != nil || !fi.Mode().IsRegular() {
			continue // 已经不在、被任务换成了别的，或者路径经过链接：都留下
		}
		size += fi.Size()
		if !dryRun {
			if err := root.Remove(p); err != nil && !os.IsNotExist(err) {
				return size, err
			}
		}
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
	_ = root.Remove(name)
	return size, nil
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
func runTotal(root *os.Root, home string, protected []string) (int64, error) {
	entries, err := fs.ReadDir(root.FS(), "run")
	if err != nil {
		return 0, err
	}
	var total int64
	for _, e := range entries {
		name := "run/" + e.Name()
		id, parseErr := strconv.ParseInt(e.Name(), 10, 64)
		var n int64
		if parseErr == nil && runPath(id) == name {
			n, err = pruneRun(root, id, "", home, protected, true)
		} else {
			n, err = sizeOf(root, name, home, protected)
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

func sizeOf(root *os.Root, name, home string, protected []string) (int64, error) {
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
		return fi.Size(), nil
	}
	var size int64
	err = fs.WalkDir(root.FS(), name, func(rel string, d fs.DirEntry, err error) error {
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
			size += fi.Size()
		}
		return nil
	})
	return size, err
}

func (p *Pruner) Prune(ctx context.Context) (Report, error) {
	r := Report{DryRun: p.Options.DryRun, Jobs: []Entry{}, Envs: []string{}, Errors: []string{}}
	if p.Options.KeepDays == 0 && p.Options.MaxBytes == 0 && p.Options.OlderThan == nil {
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
	for _, path := range outputs {
		if path == "" {
			continue
		}
		path, err := canonical(path)
		if err != nil {
			return r, fmt.Errorf("核对输出目录失败: %w", err)
		}
		if inside(path, home) || inside(home, path) {
			protected = append(protected, path)
		}
	}
	// 拒绝跟随管理目录自身的符号链接；Root 再约束每一次文件操作。
	for _, dir := range []string{"logs", "snapshots", "run", "envs"} {
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
			n, err = runTotal(root, home, protected)
		} else {
			n, err = sizeOf(root, dir, home, protected)
		}
		if err != nil {
			return r, err
		}
		r.TotalBytes += n
	}
	now := time.Now()
	var cutoff *int64
	if p.Options.OlderThan != nil {
		v := now.Add(-*p.Options.OlderThan).Unix()
		cutoff = &v
	} else if p.Options.KeepDays > 0 {
		v := now.Add(-time.Duration(p.Options.KeepDays) * 24 * time.Hour).Unix()
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
		reason := ""
		if j.Pruning {
			reason = "继续上次中断的清理"
		} else if cutoff != nil && *j.EndedAt <= *cutoff {
			reason = "超过保留时长"
		} else if p.Options.OlderThan == nil && p.Options.MaxBytes > 0 && remaining > p.Options.MaxBytes {
			reason = "总大小超过上限"
		}
		if reason == "" {
			continue
		}
		var size int64
		var targetErr error
		for _, name := range pathsFor(j.ID) {
			if err := checkProtected(home, name, protected); err != nil {
				targetErr = err
				break
			}
			var n int64
			var err error
			if name == runPath(j.ID) {
				n, err = pruneRun(root, j.ID, j.Workdir, home, protected, true)
			} else {
				n, err = sizeOf(root, name, home, protected)
			}
			if err != nil {
				targetErr = err
				break
			}
			size += n
		}
		if targetErr != nil {
			r.Errors = append(r.Errors, fmt.Sprintf("任务 #%d: %v", j.ID, targetErr))
			continue
		}
		e := Entry{JobID: j.ID, Name: j.Name, Bytes: size, Reason: reason}
		if !p.Options.DryRun {
			claimed, err := p.Store.ClaimPrune(j.ID)
			if err != nil {
				return r, err
			}
			if !claimed {
				continue
			}
			// 运行目录先清：分辨 code/ 里恢复出来的文件要用快照清单，清单要留到最后再删
			if _, err := pruneRun(root, j.ID, j.Workdir, home, protected, false); err != nil {
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
		if !p.Options.DryRun {
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
