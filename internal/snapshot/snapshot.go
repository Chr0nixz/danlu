// Package snapshot 在任务启动前把工作目录里的代码打包保存，并记录 git 信息。
//
// 每个任务生成三个文件：<id>.tar.gz（代码）、<id>.meta.json（清单和 git 信息）、<id>.diff（未提交的改动）。
package snapshot

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"danlu/internal/execx"
)

type Options struct {
	MaxFile  int64
	MaxTotal int64
}

type GitInfo struct {
	Commit string `json:"commit"`
	Branch string `json:"branch"`
	Dirty  bool   `json:"dirty"`
	Status string `json:"status"`
}

type FileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type Skip struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Meta struct {
	Workdir   string      `json:"workdir"`
	CreatedAt int64       `json:"created_at"`
	Git       *GitInfo    `json:"git"`
	Files     []FileEntry `json:"files"`
	Skipped   []Skip      `json:"skipped"`
	SkipCount int         `json:"skip_count"`
	Truncated bool        `json:"truncated"`
	TotalSize int64       `json:"total_size"`
}

const maxSkipList = 200

// 非 git 目录只收这些扩展名，避免把数据集、权重打包进去
var codeExt = map[string]bool{
	".py": true, ".pyx": true, ".pyi": true, ".sh": true, ".bash": true, ".zsh": true,
	".yaml": true, ".yml": true, ".json": true, ".toml": true, ".cfg": true, ".ini": true, ".conf": true,
	".txt": true, ".md": true, ".rst": true, ".ipynb": true,
	".c": true, ".cc": true, ".cpp": true, ".cxx": true, ".h": true, ".hpp": true, ".cu": true, ".cuh": true,
	".js": true, ".ts": true, ".go": true, ".rs": true, ".java": true, ".scala": true, ".r": true, ".jl": true,
	".lua": true, ".proto": true, ".sql": true, ".cmake": true,
}

var codeNames = map[string]bool{
	"Makefile": true, "Dockerfile": true, "CMakeLists.txt": true, "requirements.txt": true,
	".gitignore": true, "LICENSE": true,
}

var skipDirs = map[string]bool{
	".git": true, "__pycache__": true, "node_modules": true, "wandb": true, "swanlog": true,
	".ipynb_checkpoints": true, ".venv": true, "venv": true, ".mypy_cache": true, ".pytest_cache": true,
	".ruff_cache": true, ".idea": true,
}

func ArchivePath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("%d.tar.gz", id))
}
func metaPath(dir string, id int64) string {
	return filepath.Join(dir, fmt.Sprintf("%d.meta.json", id))
}
func diffPath(dir string, id int64) string { return filepath.Join(dir, fmt.Sprintf("%d.diff", id)) }

// Take 生成快照。超时（ctx）或出错时删除半成品并返回错误。
func Take(ctx context.Context, workdir, dir string, id int64, opt Options) (*Meta, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	meta := &Meta{Workdir: workdir, CreatedAt: time.Now().Unix(), Files: []FileEntry{}, Skipped: []Skip{}}
	var candidates []string
	var diff []byte
	if gitOK(ctx, workdir) {
		meta.Git, diff = gitInfo(ctx, workdir)
		files, err := gitFiles(ctx, workdir)
		if err != nil {
			return nil, err
		}
		candidates = files
	} else {
		files, err := walkFiles(ctx, workdir, meta)
		if err != nil {
			return nil, err
		}
		candidates = files
	}
	sort.Strings(candidates)

	arch := ArchivePath(dir, id)
	err := writeArchive(ctx, workdir, arch, candidates, opt, meta)
	if err != nil {
		os.Remove(arch)
		return nil, err
	}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath(dir, id), mb, 0o600); err != nil {
		return nil, err
	}
	if len(diff) > 0 {
		_ = os.WriteFile(diffPath(dir, id), diff, 0o600)
	}
	return meta, nil
}

func (m *Meta) skip(p, reason string) {
	m.SkipCount++
	if len(m.Skipped) < maxSkipList {
		m.Skipped = append(m.Skipped, Skip{Path: p, Reason: reason})
	}
}

func writeArchive(ctx context.Context, workdir, out string, files []string, opt Options, meta *Meta) error {
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	for _, rel := range files {
		if err := ctx.Err(); err != nil {
			return errors.New("快照超时")
		}
		full := filepath.Join(workdir, filepath.FromSlash(rel))
		fi, err := os.Lstat(full)
		if err != nil {
			continue // 已删除但 git 里还登记着的文件
		}
		if !fi.Mode().IsRegular() {
			if fi.IsDir() {
				meta.skip(rel, "子模块或目录")
			} else if fi.Mode()&fs.ModeSymlink != 0 {
				meta.skip(rel, "符号链接")
			}
			continue
		}
		if fi.Size() > opt.MaxFile {
			meta.skip(rel, fmt.Sprintf("超过单文件上限（%s）", humanSize(fi.Size())))
			continue
		}
		if meta.TotalSize+fi.Size() > opt.MaxTotal {
			meta.Truncated = true
			meta.skip(rel, "超过快照总大小上限")
			continue
		}
		if err := addFile(tw, full, rel, fi); err != nil {
			return err
		}
		meta.TotalSize += fi.Size()
		meta.Files = append(meta.Files, FileEntry{Path: rel, Size: fi.Size()})
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

func addFile(tw *tar.Writer, full, rel string, fi os.FileInfo) error {
	src, err := os.Open(full)
	if err != nil {
		return err
	}
	defer src.Close()
	hdr := &tar.Header{Name: rel, Mode: int64(fi.Mode().Perm()), Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.CopyN(tw, src, fi.Size())
	return err
}

func walkFiles(ctx context.Context, root string, meta *Meta) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if ctx.Err() != nil {
			return errors.New("快照超时")
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if codeNames[d.Name()] || codeExt[strings.ToLower(filepath.Ext(d.Name()))] {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := execx.Command(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "LC_ALL=C")
	return execx.Output(cmd)
}

func gitOK(ctx context.Context, dir string) bool {
	out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

const maxDiff = 5 << 20

func gitInfo(ctx context.Context, dir string) (*GitInfo, []byte) {
	g := &GitInfo{}
	if out, err := git(ctx, dir, "rev-parse", "HEAD"); err == nil {
		g.Commit = strings.TrimSpace(string(out))
	}
	if out, err := git(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD"); err == nil {
		g.Branch = strings.TrimSpace(string(out))
	}
	if out, err := git(ctx, dir, "status", "--porcelain", "--", "."); err == nil {
		g.Status = strings.TrimRight(string(out), "\n")
		g.Dirty = g.Status != ""
		if len(g.Status) > 20000 {
			g.Status = g.Status[:20000] + "\n…"
		}
	}
	var diff []byte
	if g.Commit != "" {
		if out, err := git(ctx, dir, "diff", "HEAD", "--no-color", "--no-ext-diff", "--", "."); err == nil {
			diff = out
		}
	}
	if len(diff) > maxDiff {
		diff = append(diff[:maxDiff], []byte("\n…（diff 太大，已截断）\n")...)
	}
	return g, diff
}

// gitFiles 列出已跟踪的文件，加上未跟踪但没被 .gitignore 忽略的文件（相对 dir）。
func gitFiles(ctx context.Context, dir string) ([]string, error) {
	out, err := git(ctx, dir, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return nil, fmt.Errorf("git ls-files 失败: %w", err)
	}
	seen := map[string]bool{}
	var files []string
	for _, p := range bytes.Split(out, []byte{0}) {
		s := string(p)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		files = append(files, s)
	}
	return files, nil
}

// GitHead 返回目录当前的 commit 和是否有未提交改动，用于重跑时提示"代码已变化"。
func GitHead(ctx context.Context, dir string) (*GitInfo, bool) {
	if !gitOK(ctx, dir) {
		return nil, false
	}
	g, _ := gitInfo(ctx, dir)
	g.Status = ""
	return g, true
}

// ---------- 读取 ----------

func LoadMeta(dir string, id int64) (*Meta, error) {
	b, err := os.ReadFile(metaPath(dir, id))
	if err != nil {
		return nil, err
	}
	var m Meta
	return &m, json.Unmarshal(b, &m)
}

func Diff(dir string, id int64) ([]byte, error) {
	b, err := os.ReadFile(diffPath(dir, id))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

type FileContent struct {
	Path    string `json:"path"`
	Size    int64  `json:"size"`
	Binary  bool   `json:"binary"`
	Content string `json:"content"`
}

// ReadFile 从快照包里取出一个文件。
func ReadFile(dir string, id int64, p string) (*FileContent, error) {
	p = path.Clean(strings.TrimPrefix(p, "/"))
	f, err := os.Open(ArchivePath(dir, id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil, os.ErrNotExist
		}
		if err != nil {
			return nil, err
		}
		if h.Name != p {
			continue
		}
		b, err := io.ReadAll(io.LimitReader(tr, 2<<20))
		if err != nil {
			return nil, err
		}
		fc := &FileContent{Path: p, Size: h.Size}
		head := b[:min(len(b), 8000)]
		if bytes.IndexByte(head, 0) >= 0 || (int64(len(b)) == h.Size && !utf8.Valid(b)) {
			fc.Binary = true
		} else {
			fc.Content = string(b)
		}
		return fc, nil
	}
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%dB", n)
}
