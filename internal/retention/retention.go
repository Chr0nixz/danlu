// Package retention 清理丹炉管理的任务文件，保留数据库记录和所有训练产物。
package retention

import (
	"context"
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
	return []string{"logs/" + n + ".log", "snapshots/" + n + ".tar.gz", "snapshots/" + n + ".meta.json", "snapshots/" + n + ".diff", "run/" + n}
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

func sizeOf(root *os.Root, name, home string, protected []string) (int64, error) {
	excluded := func(rel string) bool {
		full := filepath.Join(home, filepath.FromSlash(rel))
		for _, output := range protected {
			if inside(full, output) {
				return true
			}
		}
		return false
	}
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
		n, err := sizeOf(root, dir, home, protected)
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
			n, err := sizeOf(root, name, home, protected)
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
			for _, name := range pathsFor(j.ID) {
				if err := root.RemoveAll(name); err != nil {
					targetErr = err
					break
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
