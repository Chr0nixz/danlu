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
	"path"
	"path/filepath"
	"sort"
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
	err = visitArchive(ctx, ArchivePath(snapshots, source), func(h *tar.Header, r io.Reader) error {
		if strings.Contains("/"+h.Name+"/", "/.git/") {
			return fmt.Errorf("快照包含 git 管理文件 %q", h.Name)
		}
		name, err := filepath.Localize(h.Name)
		if err != nil {
			return err
		}
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

	note, err := linkCurrentFiles(ctx, code, workdir, meta)
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

// linkCurrentFiles 把快照之外、任务仍要用的文件链接回原工作目录：
//   - git 目录：现在被 .gitignore 忽略的（数据、权重、输出等），以及快照时因大小等原因跳过的；
//   - 非 git 目录：快照规则本来就不收的（非代码文件、一个文件都没收的目录），以及跳过的。
//
// 快照之后才新建的代码文件不链接，跑的仍是快照里的代码。只往下走快照里也有的目录：
// 快照一个文件都没收的目录整个链接一次，大的数据目录不用逐个文件处理。
func linkCurrentFiles(ctx context.Context, code *os.Root, workdir string, meta *Meta) (string, error) {
	skipped := map[string]bool{}
	for _, s := range meta.Skipped {
		if !fs.ValidPath(s.Path) || s.Path == "." {
			return "", fmt.Errorf("快照清单包含无效路径 %q", s.Path)
		}
		skipped[s.Path] = true
	}
	// 原目录里有、快照里没有的条目
	type candidate struct {
		rel string
		dir bool
	}
	var candidates []candidate
	workdirMissing := false
	var walk func(dir string) error
	walk = func(dir string) error {
		entries, err := os.ReadDir(filepath.Join(workdir, filepath.FromSlash(dir)))
		if dir == "" && errors.Is(err, os.ErrNotExist) {
			workdirMissing = true
			return nil
		}
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			rel := path.Join(dir, entry.Name())
			if rel == ".git" {
				continue
			}
			fi, err := code.Lstat(filepath.FromSlash(rel))
			switch {
			case err == nil:
				// 快照里的版本优先；两边都是目录时往下找快照没收的文件
				if fi.IsDir() && entry.IsDir() {
					if err := walk(rel); err != nil {
						return err
					}
				}
			case errors.Is(err, os.ErrNotExist):
				candidates = append(candidates, candidate{rel, entry.IsDir()})
			default:
				return err
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return "", err
	}

	// 跳过清单不全时分不清哪些是被跳过的，只能把快照之外的都链上
	linkAll := meta.SkipCount > len(meta.Skipped)
	var ignored map[string]bool
	var ignoreNote string
	if meta.Git != nil && !linkAll {
		rels := make([]string, len(candidates))
		for i, c := range candidates {
			rels[i] = c.rel
		}
		var err error
		if ignored, err = gitIgnored(ctx, workdir, rels); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			linkAll = true
			ignoreNote = "判断不了哪些文件被 Git 忽略（" + err.Error() + "），快照之外的文件都用了工作目录里现在的版本"
		}
	}
	wanted := func(c candidate) bool {
		switch {
		case linkAll || skipped[c.rel]:
			return true
		case meta.Git != nil:
			return ignored[c.rel]
		default:
			return c.dir || !isCodeFile(path.Base(c.rel))
		}
	}

	var linked, missing []string
	linkedSet := map[string]bool{}
	link := func(rel string) error {
		name := filepath.FromSlash(rel)
		original := filepath.Join(workdir, name)
		fi, err := os.Lstat(original)
		if errors.Is(err, os.ErrNotExist) {
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
		linkedSet[rel] = true
		if fi.IsDir() {
			rel += "/"
		}
		linked = append(linked, rel)
		return nil
	}
	for _, c := range candidates {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if wanted(c) {
			if err := link(c.rel); err != nil {
				return "", err
			}
		}
	}
	// 跳过的文件在没有链接的目录里时，单独补上
	for _, s := range meta.Skipped {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		top, _, _ := strings.Cut(s.Path, "/")
		if top == ".git" || underLinked(s.Path, linkedSet) {
			continue
		}
		name, err := filepath.Localize(s.Path)
		if err != nil {
			return "", fmt.Errorf("快照清单包含无效路径 %q", s.Path)
		}
		if _, err := code.Lstat(name); err == nil {
			continue
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if err := link(s.Path); err != nil {
			return "", err
		}
	}
	sort.Strings(linked)
	var notes []string
	if len(linked) > 0 {
		notes = append(notes, "以下内容不在快照里，用的是工作目录里现在的版本："+listPaths(linked))
	}
	if len(missing) > 0 {
		notes = append(notes, "原目录中缺少："+listPaths(missing))
	}
	if ignoreNote != "" {
		notes = append(notes, ignoreNote)
	}
	if meta.SkipCount > len(meta.Skipped) || workdirMissing {
		notes = append(notes, "快照跳过清单不全或原工作目录不存在，可能缺文件")
	}
	return strings.Join(notes, "。"), ctx.Err()
}

// underLinked 判断 rel 本身或它的某个上级目录已经链接过
func underLinked(rel string, linked map[string]bool) bool {
	for p := rel; p != "." && p != "/"; p = path.Dir(p) {
		if linked[p] {
			return true
		}
	}
	return false
}

// listPaths 最多列 20 项，免得一个大目录把备注撑满
func listPaths(paths []string) string {
	if len(paths) <= 20 {
		return strings.Join(paths, "、")
	}
	return strings.Join(paths[:20], "、") + fmt.Sprintf(" 等 %d 项", len(paths))
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
