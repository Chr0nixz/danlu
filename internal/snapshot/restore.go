package snapshot

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func RunCodeDir(runRoot string, id int64) string {
	return filepath.Join(runRoot, strconv.FormatInt(id, 10), "code")
}

type contextReader struct {
	ctx context.Context
	io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

// visitArchive 只接受丹炉快照使用的普通文件。归档读取到结尾时也校验 gzip 校验和。
func visitArchive(ctx context.Context, filename string, visit func(*tar.Header, io.Reader) error) error {
	f, err := os.Open(filename)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(contextReader{ctx, f})
	if err != nil {
		return err
	}
	defer gz.Close()
	stream := contextReader{ctx, gz}
	tr := tar.NewReader(stream)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			_, err = io.Copy(io.Discard, stream)
			return err
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || !fs.ValidPath(h.Name) || h.Name == "." {
			return fmt.Errorf("快照包含无效文件 %q", h.Name)
		}
		if err := visit(h, tr); err != nil {
			return err
		}
	}
}

// FileHashes 按包内的文件内容计算 SHA-256，避免把同大小的修改当成相同文件。
func FileHashes(ctx context.Context, dir string, id int64) (map[string][sha256.Size]byte, error) {
	result := make(map[string][sha256.Size]byte)
	err := visitArchive(ctx, ArchivePath(dir, id), func(h *tar.Header, r io.Reader) error {
		hash := sha256.New()
		if _, err := io.Copy(hash, r); err != nil {
			return err
		}
		result[h.Name] = [sha256.Size]byte(hash.Sum(nil))
		return nil
	})
	return result, err
}

// Restore 把快照恢复到新任务的运行目录，并在新任务编号下复用源快照。
// 源任务在复制完成并落库之前由保留策略保护。已有快照时可从本任务自己的文件重试。
func Restore(ctx context.Context, snapshots, runRoot, workdir string, source, id int64) (*Meta, string, error) {
	meta, err := LoadMeta(snapshots, source)
	if err != nil {
		return nil, "", err
	}
	runs, err := os.OpenRoot(runRoot)
	if err != nil {
		return nil, "", err
	}
	defer runs.Close()
	name := strconv.FormatInt(id, 10)
	if err := runs.Mkdir(name, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return nil, "", err
	}
	info, err := runs.Lstat(name)
	if err != nil {
		return nil, "", err
	}
	if !info.IsDir() {
		return nil, "", errors.New("任务运行目录不是普通目录")
	}
	run, err := runs.OpenRoot(name)
	if err != nil {
		return nil, "", err
	}
	defer run.Close()
	// 重启后，尚未启动的任务可能留下了半成品。
	if err := run.RemoveAll("code"); err != nil {
		return nil, "", err
	}
	if err := run.Mkdir("code", 0o700); err != nil {
		return nil, "", err
	}
	complete := false
	defer func() {
		if !complete {
			_ = run.RemoveAll("code")
		}
	}()
	code, err := run.OpenRoot("code")
	if err != nil {
		return nil, "", err
	}
	defer code.Close()
	archivedTop := map[string]bool{}
	err = visitArchive(ctx, ArchivePath(snapshots, source), func(h *tar.Header, r io.Reader) error {
		if strings.Contains("/"+h.Name+"/", "/.git/") {
			return fmt.Errorf("快照包含 git 管理文件 %q", h.Name)
		}
		name, err := filepath.Localize(h.Name)
		if err != nil {
			return err
		}
		top, _, _ := strings.Cut(h.Name, "/")
		archivedTop[top] = true
		if err := code.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		f, err := code.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fs.FileMode(h.Mode).Perm())
		if err != nil {
			return err
		}
		_, err = io.Copy(f, r)
		return errors.Join(err, f.Close())
	})
	if err != nil {
		return nil, "", err
	}

	note, err := linkCurrentFiles(ctx, code, workdir, archivedTop, meta)
	if err != nil {
		return nil, "", err
	}
	if source != id {
		if err := linkSnapshot(snapshots, source, id); err != nil {
			return nil, "", err
		}
	}
	complete = true
	return meta, note, nil
}

func linkCurrentFiles(ctx context.Context, code *os.Root, workdir string, archivedTop map[string]bool, meta *Meta) (string, error) {
	var linked, missing []string
	link := func(rel string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		name, err := filepath.Localize(rel)
		if err != nil || name == "." {
			return fmt.Errorf("快照清单包含无效路径 %q", rel)
		}
		if _, err := code.Lstat(name); err == nil {
			return nil // 已恢复的快照文件优先。
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		original := filepath.Join(workdir, name)
		if _, err := os.Stat(original); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, rel)
			return nil
		} else if err != nil {
			return err
		}
		if err := code.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			return err
		}
		if err := code.Symlink(original, name); err != nil {
			return err
		}
		return nil
	}
	entries, err := os.ReadDir(workdir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	for _, entry := range entries {
		if entry.Name() != ".git" && !archivedTop[entry.Name()] {
			if err := link(entry.Name()); err != nil {
				return "", err
			}
		}
	}
	// 已链接的顶层目录会带上子文件。只在从快照恢复的目录中单独补链接。
	for _, skipped := range meta.Skipped {
		top, _, _ := strings.Cut(skipped.Path, "/")
		if top == ".git" {
			continue
		}
		original := filepath.Join(workdir, filepath.FromSlash(skipped.Path))
		if !fs.ValidPath(skipped.Path) || skipped.Path == "." {
			return "", fmt.Errorf("快照清单包含无效路径 %q", skipped.Path)
		}
		if _, err := os.Stat(original); errors.Is(err, os.ErrNotExist) {
			missing = append(missing, skipped.Path)
			continue
		} else if err != nil {
			return "", err
		}
		if archivedTop[top] {
			if err := link(skipped.Path); err != nil {
				return "", err
			}
		}
		linked = append(linked, skipped.Path)
	}
	var notes []string
	if len(linked) > 0 {
		notes = append(notes, "以下文件用的是现在的版本："+strings.Join(linked, "、"))
	}
	if len(missing) > 0 {
		notes = append(notes, "原目录中缺少："+strings.Join(missing, "、"))
	}
	if meta.SkipCount > len(meta.Skipped) || errors.Is(err, os.ErrNotExist) {
		notes = append(notes, "快照跳过清单不全或原工作目录不存在，可能缺文件")
	}
	return strings.Join(notes, "。"), ctx.Err()
}

func linkSnapshot(dir string, source, id int64) error {
	var linked []string
	for _, file := range []struct {
		path     func(string, int64) string
		optional bool
	}{{ArchivePath, false}, {metaPath, false}, {diffPath, true}} {
		from, to := file.path(dir, source), file.path(dir, id)
		err := os.Link(from, to)
		if err == nil {
			linked = append(linked, to)
			continue
		}
		if file.optional && errors.Is(err, os.ErrNotExist) {
			continue
		}
		if errors.Is(err, os.ErrExist) {
			a, aErr := os.Stat(from)
			b, bErr := os.Stat(to)
			if aErr == nil && bErr == nil && os.SameFile(a, b) {
				continue // 上一次准备已建立硬链接。
			}
		}
		for _, path := range linked {
			_ = os.Remove(path)
		}
		return err
	}
	return nil
}
