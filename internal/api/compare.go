package api

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"danlu/internal/snapshot"
	"danlu/internal/store"

	"mvdan.cc/sh/v3/syntax"
)

type valueChange struct {
	Name string  `json:"name"`
	A    *string `json:"a"`
	B    *string `json:"b"`
}

type fileChange struct {
	Path string `json:"path"`
	Kind string `json:"kind"`
}

type codeComparison struct {
	Files   []fileChange `json:"files"`
	Error   string       `json:"error,omitempty"`
	Partial bool         `json:"partial,omitempty"`
}

type comparisonCache struct {
	mu    sync.Mutex
	pairs map[[2]int64]codeComparison
}

func (s *Server) compareJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	aID, aErr := strconv.ParseInt(q.Get("a"), 10, 64)
	bID, bErr := strconv.ParseInt(q.Get("b"), 10, 64)
	if aErr != nil || bErr != nil || aID <= 0 || bID <= 0 || aID == bID {
		writeErr(w, http.StatusBadRequest, "请选择两个不同的任务")
		return
	}
	get := func(id int64) (*store.Job, bool) {
		j, err := s.Store.GetJob(id)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, fmt.Sprintf("任务 #%d 不存在", id))
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err.Error())
		}
		return j, err == nil
	}
	a, ok := get(aID)
	if !ok {
		return
	}
	b, ok := get(bID)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	code := s.compareCode(ctx, a, b)
	packages, packageError := s.comparePackages(a, b)
	w.Header().Set("Cache-Control", "private, no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"a": a, "b": b,
		"command_a": commandTokens(a.Cmd), "command_b": commandTokens(b.Cmd),
		"env": changes(a.Env, b.Env), "code": code,
		"packages": packages, "packages_error": packageError,
	})
}

var commandGap = regexp.MustCompile(`\s+|[^\s;&|<>()]+|[;&|<>()]+`)

// commandTokens 保留空白和符号，引用、转义、变量展开按 Bash 的词边界分组。
// 语法有误的命令仍可比较原文，解析时不执行任何命令。
func commandTokens(command string) []string {
	tree, err := syntax.NewParser().Parse(strings.NewReader(command), "")
	if err != nil {
		return []string{command}
	}
	type span struct{ start, end int }
	var words []span
	syntax.Walk(tree, func(node syntax.Node) bool {
		word, ok := node.(*syntax.Word)
		if !ok {
			return true
		}
		words = append(words, span{int(word.Pos().Offset()), int(word.End().Offset())})
		return false
	})
	slices.SortFunc(words, func(a, b span) int { return a.start - b.start })
	tokens := make([]string, 0, len(words)*2)
	at := 0
	for _, word := range words {
		if word.start < at || word.end <= word.start || word.end > len(command) {
			continue
		}
		tokens = append(tokens, commandGap.FindAllString(command[at:word.start], -1)...)
		tokens = append(tokens, command[word.start:word.end])
		at = word.end
	}
	return append(tokens, commandGap.FindAllString(command[at:], -1)...)
}

func changes(a, b map[string]string) []valueChange {
	out := []valueChange{}
	for name, before := range a {
		after, exists := b[name]
		if !exists {
			out = append(out, valueChange{Name: name, A: &before})
		} else if before != after {
			out = append(out, valueChange{Name: name, A: &before, B: &after})
		}
	}
	for name, after := range b {
		if _, exists := a[name]; !exists {
			out = append(out, valueChange{Name: name, B: &after})
		}
	}
	slices.SortFunc(out, func(a, b valueChange) int { return strings.Compare(a.Name, b.Name) })
	return out
}

func snapshotUnavailable(j *store.Job) string {
	switch {
	case j.Pruning:
		return "快照正在清理"
	case j.PrunedAt != nil:
		return "快照已清理"
	case !j.SnapshotOK:
		return "没有代码快照"
	default:
		return ""
	}
}

func (s *Server) compareCode(ctx context.Context, a, b *store.Job) codeComparison {
	key := [2]int64{a.ID, b.ID}
	for _, job := range []*store.Job{a, b} {
		if reason := snapshotUnavailable(job); reason != "" {
			return codeComparison{Error: fmt.Sprintf("任务 #%d %s", job.ID, reason)}
		}
		if _, err := os.Stat(snapshot.ArchivePath(s.Paths.Snapshots, job.ID)); err != nil {
			return codeComparison{Error: fmt.Sprintf("无法读取任务 #%d 的快照包", job.ID)}
		}
	}
	cacheable := (a.PID > 0 || a.Finished()) && (b.PID > 0 || b.Finished())
	if cacheable {
		s.comparisons.mu.Lock()
		result, ok := s.comparisons.pairs[key]
		s.comparisons.mu.Unlock()
		if ok {
			return result
		}
	}
	partial := false
	for _, job := range []*store.Job{a, b} {
		meta, err := snapshot.LoadMeta(s.Paths.Snapshots, job.ID)
		if err != nil {
			return codeComparison{Error: fmt.Sprintf("无法读取任务 #%d 的快照清单", job.ID)}
		}
		partial = partial || meta.Truncated || meta.SkipCount > 0
	}
	before, err := snapshot.FileHashes(ctx, s.Paths.Snapshots, a.ID)
	if err != nil {
		return codeComparison{Error: fmt.Sprintf("无法读取任务 #%d 的快照内容", a.ID)}
	}
	after, err := snapshot.FileHashes(ctx, s.Paths.Snapshots, b.ID)
	if err != nil {
		return codeComparison{Error: fmt.Sprintf("无法读取任务 #%d 的快照内容", b.ID)}
	}
	result := codeComparison{Files: []fileChange{}, Partial: partial}
	for path, hash := range before {
		if other, ok := after[path]; !ok {
			result.Files = append(result.Files, fileChange{path, "removed"})
		} else if hash != other {
			result.Files = append(result.Files, fileChange{path, "changed"})
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			result.Files = append(result.Files, fileChange{path, "added"})
		}
	}
	slices.SortFunc(result.Files, func(a, b fileChange) int { return strings.Compare(a.Path, b.Path) })
	if cacheable {
		s.comparisons.mu.Lock()
		if s.comparisons.pairs == nil || len(s.comparisons.pairs) >= 16 {
			s.comparisons.pairs = make(map[[2]int64]codeComparison)
		}
		s.comparisons.pairs[key] = result
		s.comparisons.mu.Unlock()
	}
	return result
}

func (s *Server) comparePackages(a, b *store.Job) ([]valueChange, string) {
	var lists [2]map[string]string
	for i, j := range []*store.Job{a, b} {
		if j.Recipe != nil && j.Recipe.Pending {
			return nil, fmt.Sprintf("任务 #%d 的环境包清单仍在采集", j.ID)
		}
		if j.Recipe == nil || j.Recipe.EnvHash == "" {
			return nil, fmt.Sprintf("任务 #%d 没有环境包记录", j.ID)
		}
		hash := j.Recipe.EnvHash
		if _, err := hex.DecodeString(hash); len(hash) != 64 || err != nil {
			return nil, fmt.Sprintf("任务 #%d 的环境包哈希无效", j.ID)
		}
		if i == 1 && hash == a.Recipe.EnvHash && (j.CondaEnv == "") == (a.CondaEnv == "") {
			return []valueChange{}, ""
		}
		data, err := os.ReadFile(filepath.Join(s.Paths.Envs, hash+".txt"))
		if err != nil {
			return nil, fmt.Sprintf("无法读取任务 #%d 的环境包清单", j.ID)
		}
		lists[i] = packageVersions(string(data), j.CondaEnv != "")
	}
	return changes(lists[0], lists[1]), ""
}

var packageSeparator = regexp.MustCompile(`[-_.]+`)

func packageVersions(data string, conda bool) map[string]string {
	packages := map[string]string{}
	for line := range strings.SplitSeq(data, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var name, version string
		var ok bool
		if conda {
			name, version, ok = strings.Cut(line, "=")
		} else if name, version, ok = strings.Cut(line, "=="); !ok {
			name, version, ok = strings.Cut(line, " @ ")
		}
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			// 例如 pip freeze 的本地 editable 路径。
			packages[line] = ""
			continue
		}
		name = packageSeparator.ReplaceAllString(strings.ToLower(name), "-")
		packages[name] = version
	}
	return packages
}
