package runner

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"danlu/internal/store"
)

type manifestDocument struct {
	SchemaVersion int               `json:"schema_version"`
	UpdatedAt     int64             `json:"updated_at"`
	ID            int64             `json:"id"`
	Name          string            `json:"name"`
	Server        string            `json:"server,omitempty"`
	Workdir       string            `json:"workdir"`
	CodeFrom      *int64            `json:"code_from,omitempty"`
	OutputDir     string            `json:"output_dir"`
	Command       string            `json:"command"`
	Environment   map[string]string `json:"environment"`
	CondaEnv      string            `json:"conda_env,omitempty"`
	GPUs          []int             `json:"gpus"`
	Git           manifestGit       `json:"git"`
	Snapshot      manifestSnapshot  `json:"snapshot"`
	Recipe        *store.Recipe     `json:"recipe,omitempty"`
	StartedAt     *int64            `json:"started_at,omitempty"`
	Status        string            `json:"status"`
	ExitCode      *int              `json:"exit_code,omitempty"`
	FailKind      string            `json:"fail_kind,omitempty"`
	FailReason    string            `json:"fail_reason,omitempty"`
}

type manifestGit struct {
	Commit string `json:"commit,omitempty"`
	Branch string `json:"branch,omitempty"`
	Dirty  bool   `json:"dirty"`
}

type manifestSnapshot struct {
	OK   bool   `json:"ok"`
	Note string `json:"note,omitempty"`
}

// OutputPath 生成不受任务名称或工作目录内容影响的输出路径。
func OutputPath(root, workdir string, id int64, name string) string {
	project := safePathComponent(filepath.Base(filepath.Clean(workdir)))
	if project == "" || project == "." || project == ".." {
		project = "project"
	}
	jobName := safePathComponent(name)
	if jobName == "" || jobName == "." || jobName == ".." {
		jobName = "job"
	}
	return filepath.Join(root, project, strconv.FormatInt(id, 10)+"-"+jobName)
}

func safePathComponent(value string) string {
	var b strings.Builder
	for _, r := range value {
		if r == '/' || r == '\\' || r == 0 || unicode.IsControl(r) {
			b.WriteByte('_')
		} else {
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(b.String())
}

func ManifestJSON(job *store.Job) ([]byte, error) {
	if job == nil {
		return nil, errors.New("缺少任务信息")
	}
	var server string
	if job.Recipe != nil && job.Recipe.Machine != nil {
		server = job.Recipe.Machine.Hostname
	}
	doc := manifestDocument{
		SchemaVersion: 1,
		UpdatedAt:     time.Now().UnixNano(),
		ID:            job.ID,
		Name:          job.Name,
		Server:        server,
		Workdir:       job.Workdir,
		CodeFrom:      job.CodeFrom,
		OutputDir:     job.OutputDir,
		Command:       job.Cmd,
		Environment:   job.Env,
		CondaEnv:      job.CondaEnv,
		GPUs:          job.GPUsAssigned,
		Git:           manifestGit{Commit: job.GitCommit, Branch: job.GitBranch, Dirty: job.GitDirty},
		Snapshot:      manifestSnapshot{OK: job.SnapshotOK, Note: job.SnapshotNote},
		Recipe:        job.Recipe,
		StartedAt:     job.StartedAt,
		Status:        job.Status,
		ExitCode:      job.ExitCode,
		FailKind:      job.FailKind,
		FailReason:    job.FailReason,
	}
	return json.MarshalIndent(doc, "", "  ")
}

type dirLock struct {
	mu   sync.Mutex
	refs int
}

// lockManifest 锁住一个输出目录的清单。manifestMu 只保护 manifestLocks，从不在文件 I/O 期间持有。
func (r *Runner) lockManifest(dir string) func() {
	dir = filepath.Clean(dir)
	r.manifestMu.Lock()
	if r.manifestLocks == nil {
		r.manifestLocks = map[string]*dirLock{}
	}
	l := r.manifestLocks[dir]
	if l == nil {
		l = &dirLock{}
		r.manifestLocks[dir] = l
	}
	l.refs++
	r.manifestMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		r.manifestMu.Lock()
		if l.refs--; l.refs == 0 {
			delete(r.manifestLocks, dir)
		}
		r.manifestMu.Unlock()
	}
}

func (r *Runner) WriteManifest(outputDir string, data []byte) error {
	if outputDir == "" {
		return nil
	}
	defer r.lockManifest(outputDir)()
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return err
	}
	target := filepath.Join(outputDir, "danlu.json")
	var incoming, existing manifestDocument
	if err := json.Unmarshal(data, &incoming); err != nil {
		return fmt.Errorf("编码任务清单失败: %w", err)
	}
	if old, err := os.ReadFile(target); err == nil && json.Unmarshal(old, &existing) == nil && existing.UpdatedAt >= incoming.UpdatedAt {
		return nil
	}
	tmp, err := os.CreateTemp(outputDir, "danlu.json.tmp-")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := bytes.NewReader(data).WriteTo(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, target); err != nil {
		if _, statErr := os.Stat(target); statErr != nil {
			return fmt.Errorf("更新任务清单失败: %w", err)
		}
		if removeErr := os.Remove(target); removeErr != nil {
			return fmt.Errorf("替换任务清单失败: %w", removeErr)
		}
		if err := os.Rename(tmpPath, target); err != nil {
			return fmt.Errorf("更新任务清单失败: %w", err)
		}
	}
	return nil
}
