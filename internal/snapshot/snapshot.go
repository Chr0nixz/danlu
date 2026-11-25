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
	"os/exec"
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
	// Note 是需要用户知道的情况，比如 git 拒绝读取工作目录
	Note string `json:"note,omitempty"`
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

// Take 生成快照。超时（ctx）或出错时删除半成品并返回错误；这时如果已经读到 git 信息，
// 返回的 Meta 不为 nil、只有 Git 有效，调用方照样记下 commit、分支和有没有未提交改动。
//
// git 目录先收被跟踪的文件，再收未跟踪的：总大小上限按先到先得，没写进 .gitignore 的日志、输出
// 不能把预算占满、挤掉代码。单个文件读不了（没有权限、刚被删掉）记进跳过清单，不让整份快照失败。
func Take(ctx context.Context, workdir, dir string, id int64, opt Options) (*Meta, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	meta := &Meta{Workdir: workdir, CreatedAt: time.Now().Unix(), Files: []FileEntry{}, Skipped: []Skip{}}
	var candidates []string
	var tracked int
	var diff []byte
	isGit, note := gitCheck(ctx, workdir)
	meta.Note = note
	if isGit {
		meta.Git, diff = gitInfo(ctx, workdir, true)
		files, others, err := gitFiles(ctx, workdir)
		if err != nil {
			return gitOnly(meta), err
		}
		candidates, tracked = append(files, others...), len(files)
	} else {
		files, err := walkFiles(ctx, workdir, meta)
		if err != nil {
			return nil, err
		}
		sort.Strings(files)
		candidates = files
	}

	arch := ArchivePath(dir, id)
	err := writeArchive(ctx, workdir, arch, candidates, tracked, opt, meta)
	if err != nil {
		os.Remove(arch)
		return gitOnly(meta), err
	}
	mb, _ := json.Marshal(meta)
	if err := os.WriteFile(metaPath(dir, id), mb, 0o600); err != nil {
		return gitOnly(meta), err
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

// addNote 在备注后面接一句
func (m *Meta) addNote(note string) {
	if m.Note != "" {
		note = m.Note + "；" + note
	}
	m.Note = note
}

// gitOnly 是快照失败时交给调用方的 Meta：只留 git 信息（commit 在失败前已经读到了）。没有 git 信息时为 nil
func gitOnly(m *Meta) *Meta {
	if m.Git == nil {
		return nil
	}
	return &Meta{Workdir: m.Workdir, CreatedAt: m.CreatedAt, Git: m.Git}
}

// writeArchive 按顺序把 files 收进包里，前 tracked 个是 git 跟踪的文件（非 git 目录为 0）。
func writeArchive(ctx context.Context, workdir, out string, files []string, tracked int, opt Options, meta *Meta) error {
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz := gzip.NewWriter(f)
	tw := tar.NewWriter(gz)
	badNames, trackedTooBig, unreadable := 0, 0, 0
	for i, rel := range files {
		if err := ctx.Err(); err != nil {
			return errors.New("快照超时")
		}
		if !fs.ValidPath(rel) || rel == "." {
			// 恢复和对比都认不了这样的名字，跳过清单里也写不进：不收，计数写进备注
			badNames++
			continue
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
		// 先打开再看大小：打开失败（没有读取权限、刚被删掉）只跳过这一个文件；
		// 大小取打开后的 fstat，和真正读的是同一个文件
		src, err := os.Open(full)
		if errors.Is(err, fs.ErrNotExist) {
			continue // 在 Lstat 和打开之间被删掉了，和已删除的文件一样
		}
		if err != nil {
			unreadable++
			reason := "读取失败"
			if errors.Is(err, fs.ErrPermission) {
				reason = "没有读取权限"
			}
			meta.skip(rel, reason)
			continue
		}
		if fi, err = src.Stat(); err != nil || !fi.Mode().IsRegular() {
			src.Close()
			unreadable++
			meta.skip(rel, "读取失败")
			continue
		}
		tooBig := ""
		if fi.Size() > opt.MaxFile {
			tooBig = fmt.Sprintf("超过单文件上限（%s）", humanSize(fi.Size()))
		} else if meta.TotalSize+fi.Size() > opt.MaxTotal {
			meta.Truncated = true
			tooBig = "超过快照总大小上限"
		}
		if tooBig != "" {
			src.Close()
			meta.skip(rel, tooBig)
			if i < tracked {
				trackedTooBig++
			}
			continue
		}
		err = addFile(tw, src, rel, fi)
		src.Close()
		if err != nil {
			return err
		}
		meta.TotalSize += fi.Size()
		meta.Files = append(meta.Files, FileEntry{Path: rel, Size: fi.Size()})
	}
	// 被跟踪的文件先收、未跟踪的后收；清单还是按路径排好，查看和对比都按这个顺序
	sort.Slice(meta.Files, func(i, j int) bool { return meta.Files[i].Path < meta.Files[j].Path })
	if trackedTooBig > 0 {
		meta.addNote(fmt.Sprintf("有 %d 个 git 跟踪的文件超过快照大小上限没有保存，用快照重跑时它们用的是工作目录里那时的版本，"+
			"可以在设置里调大快照的大小上限", trackedTooBig))
	}
	if unreadable > 0 {
		meta.addNote(fmt.Sprintf("有 %d 个文件读不了（比如没有读取权限），没有保存", unreadable))
	}
	if badNames > 0 {
		meta.addNote(fmt.Sprintf("有 %d 个文件的名字不是合法的 UTF-8，没有收进快照，用快照重跑时链接工作目录里现在的版本", badNames))
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Close()
}

// addFile 把已经打开的文件写进包。文件在读的过程中变短或读出错时整份快照失败：
// 包头里已经写了大小，不能只收半个文件（这比打开失败少见得多：要正好在复制时被截断）。
func addFile(tw *tar.Writer, src *os.File, rel string, fi os.FileInfo) error {
	hdr := &tar.Header{Name: rel, Mode: int64(fi.Mode().Perm()), Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	if _, err := io.CopyN(tw, src, fi.Size()); err != nil {
		return fmt.Errorf("读 %s 时出错: %w", rel, err)
	}
	return nil
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
		if isCodeFile(d.Name()) {
			out = append(out, rel)
		}
		return nil
	})
	return out, err
}

// isCodeFile 是非 git 目录收进快照的文件
func isCodeFile(name string) bool {
	return codeNames[name] || codeExt[strings.ToLower(filepath.Ext(name))]
}

func git(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := execx.Command(ctx, "git", append([]string{"-C", dir}, args...)...)
	cmd.Env = gitEnv()
	return execx.Output(cmd)
}

// gitEnv 是在用户仓库里跑 git 的环境。GIT_OPTIONAL_LOCKS=0：git status 默认会拿 .git/index.lock
// 回写索引，超时整组 SIGKILL 时来不及删锁，用户之后的 git add/commit 就报另一个 git 进程在运行；
// 后台调用也不该和用户自己的提交抢锁。git 文档对后台调用就是这么建议的。
func gitEnv() []string {
	return append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GIT_OPTIONAL_LOCKS=0", "LC_ALL=C")
}

func gitOK(ctx context.Context, dir string) bool {
	ok, _ := gitCheck(ctx, dir)
	return ok
}

// gitCheck 判断 dir 是不是 git 工作区。git 因为目录属于其他用户而拒绝读取（dubious ownership）时，
// 返回一条说明：这种情况不能静默当成非 git 目录，用户要知道 commit 信息为什么没了。
// 不用 -c safe.directory 绕过：这个检查防的是别人仓库里的配置在我们身上执行命令。
func gitCheck(ctx context.Context, dir string) (bool, string) {
	out, err := git(ctx, dir, "rev-parse", "--is-inside-work-tree")
	if err == nil {
		return strings.TrimSpace(string(out)) == "true", ""
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) && bytes.Contains(ee.Stderr, []byte("dubious ownership")) {
		return false, fmt.Sprintf("工作目录属于其他用户，git 拒绝读取（dubious ownership），没有记下 commit，按非 git 目录保存。"+
			"确认可信后执行 git config --global --add safe.directory %s", dir)
	}
	return false, ""
}

const maxDiff = 5 << 20

// gitInfo 读 commit、分支和工作区状态；withDiff 时再取未提交的改动（相对 HEAD，可能很慢）。
//
// 改动用底层命令 diff-index 取，不用 git diff：git diff 会先刷新索引再拿 index.lock 回写，
// 不看 GIT_OPTIONAL_LOCKS。两者输出的补丁一样，只改了修改时间的文件也都不算改动；
// diff-index 还不读用户的 diff.* 界面配置，格式稳定。
func gitInfo(ctx context.Context, dir string, withDiff bool) (*GitInfo, []byte) {
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
	if withDiff && g.Commit != "" {
		if out, err := git(ctx, dir, "diff-index", "-p", "--no-color", "--no-ext-diff", "HEAD", "--", "."); err == nil {
			diff = out
		}
	}
	if len(diff) > maxDiff {
		diff = append(diff[:maxDiff], []byte("\n…（diff 太大，已截断）\n")...)
	}
	return g, diff
}

// gitFiles 列出已跟踪的文件（tracked），和未跟踪但没被 .gitignore 忽略的文件（others），相对 dir，各自按路径排序。
// 分开列是为了先收代码：快照总大小上限按先到先得。
func gitFiles(ctx context.Context, dir string) (tracked, others []string, err error) {
	seen := map[string]bool{}
	list := func(args ...string) ([]string, error) {
		out, err := git(ctx, dir, append([]string{"ls-files", "-z"}, args...)...)
		if err != nil {
			return nil, fmt.Errorf("git ls-files 失败: %w", err)
		}
		var files []string
		for _, p := range bytes.Split(out, []byte{0}) {
			// 没登记成子模块的嵌套仓库列成带尾斜杠的目录（third_party/lib/），去掉斜杠后按目录跳过
			s := strings.TrimSuffix(string(p), "/")
			if s == "" || seen[s] {
				continue // 冲突中的文件在 --cached 里按阶段重复出现
			}
			seen[s] = true
			files = append(files, s)
		}
		sort.Strings(files)
		return files, nil
	}
	if tracked, err = list("--cached"); err != nil {
		return nil, nil, err
	}
	if others, err = list("--others", "--exclude-standard"); err != nil {
		return nil, nil, err
	}
	return tracked, others, nil
}

// gitIgnored 返回 paths（相对 dir）里现在被 .gitignore 忽略的那些。已跟踪的文件不算忽略。
func gitIgnored(ctx context.Context, dir string, paths []string) (map[string]bool, error) {
	ignored := map[string]bool{}
	if len(paths) == 0 {
		return ignored, nil
	}
	cmd := execx.Command(ctx, "git", "-C", dir, "check-ignore", "-z", "--stdin")
	cmd.Env = gitEnv()
	cmd.Stdin = strings.NewReader(strings.Join(paths, "\x00") + "\x00")
	out, err := execx.Output(cmd)
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return ignored, nil // 一个都没被忽略
	}
	if err != nil {
		return nil, fmt.Errorf("git check-ignore 失败: %w", err)
	}
	for _, p := range bytes.Split(out, []byte{0}) {
		if len(p) > 0 {
			ignored[string(p)] = true
		}
	}
	return ignored, nil
}

// GitHead 返回目录当前的 commit 和是否有未提交改动，用于重跑时提示"代码已变化"。
func GitHead(ctx context.Context, dir string) (*GitInfo, bool) {
	if !gitOK(ctx, dir) {
		return nil, false
	}
	g, _ := gitInfo(ctx, dir, false) // 只要 commit 和 dirty，不跑 diff
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
