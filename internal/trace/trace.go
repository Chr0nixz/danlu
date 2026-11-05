// Package trace 从训练产物路径反查对应任务。
package trace

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"danlu/internal/store"
)

const maxPathLength = 4096

var (
	ErrPathRequired = errors.New("路径不能为空")
	ErrPathTooLong  = errors.New("路径无效或太长")
	ErrPathNotAbs   = errors.New("路径必须是服务器上的绝对路径")
)

type Source string

const (
	SourceOutputDir Source = "output_dir"
	// SourceRunCode：快照重跑在 <数据目录>/run/<id>/code/ 里执行，相对路径写出的产物落在这里
	SourceRunCode  Source = "run_code"
	SourceManifest Source = "danlu_json"
	SourceGuess    Source = "guess"
)

type Match struct {
	Job    *store.Job `json:"job"`
	Source Source     `json:"source"`
}

type Result struct {
	Path    string  `json:"path"`
	Matches []Match `json:"matches"`
}

type JobStore interface {
	GetJob(int64) (*store.Job, error)
	JobsByWorkdirs([]string) ([]*store.Job, error)
}

// Search 按输出目录、快照重跑目录、danlu.json、命令中的路径片段依次查找。
// runRoot 是 <数据目录>/run，为空时不查快照重跑目录。
func Search(st JobStore, outputRoot, runRoot, input string) (Result, error) {
	if input == "" {
		return Result{}, ErrPathRequired
	}
	if len(input) > maxPathLength || strings.IndexByte(input, 0) >= 0 {
		return Result{}, ErrPathTooLong
	}
	if !filepath.IsAbs(input) {
		return Result{}, ErrPathNotAbs
	}
	input = filepath.Clean(input)
	root, err := filepath.Abs(outputRoot)
	if err != nil {
		return Result{}, fmt.Errorf("解析输出目录失败: %w", err)
	}
	root = filepath.Clean(root)

	byID := make(map[int64]Match)
	add := func(job *store.Job, source Source) {
		if job == nil {
			return
		}
		old, ok := byID[job.ID]
		if !ok || sourcePriority(source) < sourcePriority(old.Source) {
			byID[job.ID] = Match{Job: job, Source: source}
		}
	}

	if rel, err := filepath.Rel(root, input); err == nil && rel != "." && !isOutside(rel) {
		parts := strings.Split(rel, string(filepath.Separator))
		if len(parts) >= 2 {
			idPart, _, hasName := strings.Cut(parts[1], "-")
			if id, err := strconv.ParseInt(idPart, 10, 64); err == nil && id > 0 && hasName {
				job, err := st.GetJob(id)
				if err == nil {
					if filepath.Clean(job.OutputDir) == filepath.Join(root, parts[0], parts[1]) {
						add(job, SourceOutputDir)
					}
				} else if !errors.Is(err, store.ErrNotFound) {
					return Result{}, err
				}
			}
		}
	}

	if job, err := runCodeJob(st, runRoot, input); err != nil {
		return Result{}, err
	} else {
		add(job, SourceRunCode)
	}

	if job, err := manifestJob(st, input); err != nil {
		return Result{}, err
	} else {
		add(job, SourceManifest)
	}

	jobs, err := st.JobsByWorkdirs(pathAncestors(input))
	if err != nil {
		return Result{}, err
	}
	for _, job := range jobs {
		rel, err := filepath.Rel(filepath.Clean(job.Workdir), input)
		if err != nil || isOutside(rel) || rel == "." {
			continue
		}
		for _, component := range strings.Split(rel, string(filepath.Separator)) {
			if component != "." && component != ".." && strings.Contains(job.Cmd, component) {
				add(job, SourceGuess)
				break
			}
		}
	}

	matches := make([]Match, 0, len(byID))
	for _, match := range byID {
		matches = append(matches, match)
	}
	sort.Slice(matches, func(i, j int) bool {
		pi, pj := sourcePriority(matches[i].Source), sourcePriority(matches[j].Source)
		if pi != pj {
			return pi < pj
		}
		return matches[i].Job.ID > matches[j].Job.ID
	})
	return Result{Path: input, Matches: matches}, nil
}

// runCodeJob 识别 <runRoot>/<id>/code[/…]，对应用快照重跑的任务 <id>。
func runCodeJob(st JobStore, runRoot, input string) (*store.Job, error) {
	if runRoot == "" {
		return nil, nil
	}
	root, err := filepath.Abs(runRoot)
	if err != nil {
		return nil, nil
	}
	rel, err := filepath.Rel(filepath.Clean(root), input)
	if err != nil || isOutside(rel) {
		return nil, nil
	}
	parts := strings.Split(rel, string(filepath.Separator))
	if len(parts) < 2 || parts[1] != "code" {
		return nil, nil
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		return nil, nil
	}
	job, err := st.GetJob(id)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if job.CodeFrom == nil {
		return nil, nil // 只有快照重跑在 code/ 里执行
	}
	return job, nil
}

func sourcePriority(source Source) int {
	switch source {
	case SourceOutputDir, SourceRunCode:
		return 0
	case SourceManifest:
		return 1
	default:
		return 2
	}
}

func isOutside(rel string) bool {
	return rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func pathAncestors(path string) []string {
	var paths []string
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		paths = append(paths, current)
		parent := filepath.Dir(current)
		if parent != current {
			paths = append(paths, current+string(filepath.Separator))
		}
		if parent == current {
			break
		}
	}
	return paths
}

func manifestJob(st JobStore, input string) (*store.Job, error) {
	for current := input; ; current = filepath.Dir(current) {
		id, ok := readManifestID(filepath.Join(current, "danlu.json"))
		if ok {
			job, err := st.GetJob(id)
			if !errors.Is(err, store.ErrNotFound) {
				return job, err
			}
		}
		if filepath.Dir(current) == current {
			return nil, nil
		}
	}
}

func readManifestID(path string) (int64, bool) {
	const maxManifestSize = 1 << 20
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxManifestSize {
		return 0, false
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, false
	}
	defer file.Close()
	var manifest struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(file, maxManifestSize)).Decode(&manifest); err != nil || manifest.ID <= 0 {
		return 0, false
	}
	return manifest.ID, true
}
