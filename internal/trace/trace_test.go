package trace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"danlu/internal/store"
)

func TestSearchByOutputDirManifestAndGuess(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	root := filepath.Join(t.TempDir(), "outputs")
	workdir := filepath.Join(t.TempDir(), "project")
	job := &store.Job{Name: "train", Workdir: workdir + string(filepath.Separator), Cmd: "python train.py --checkpoint checkpoints/latest.pt", Status: store.StatusSucceeded}
	if err := st.InsertJob(job); err != nil {
		t.Fatal(err)
	}
	job.OutputDir = filepath.Join(root, "project", "1-train")
	if err := st.SaveJob(job); err != nil {
		t.Fatal(err)
	}

	manifestDir := filepath.Join(t.TempDir(), "copied-output")
	if err := os.MkdirAll(filepath.Join(manifestDir, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest, _ := json.Marshal(map[string]any{"id": job.ID})
	if err := os.WriteFile(filepath.Join(manifestDir, "danlu.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		path   string
		source Source
	}{
		{name: "output directory", path: filepath.Join(job.OutputDir, "checkpoints", "latest.pt"), source: SourceOutputDir},
		{name: "manifest ancestor", path: filepath.Join(manifestDir, "nested", "latest.pt"), source: SourceManifest},
		{name: "command guess", path: filepath.Join(workdir, "checkpoints", "latest.pt"), source: SourceGuess},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Search(st, root, tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Matches) != 1 || result.Matches[0].Job.ID != job.ID || result.Matches[0].Source != tt.source {
				t.Fatalf("matches = %+v, want job %d from %s", result.Matches, job.ID, tt.source)
			}
		})
	}

	// 输出目录和清单同时命中时，按最直接的来源报告一次。
	if err := os.MkdirAll(job.OutputDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(job.OutputDir, "danlu.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := Search(st, root, filepath.Join(job.OutputDir, "danlu.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Source != SourceOutputDir {
		t.Fatalf("duplicate matches were not merged by priority: %+v", result.Matches)
	}

	result, err = Search(st, root, filepath.Join(root, "another-project", "1-train", "latest.pt"))
	if err != nil || len(result.Matches) != 0 {
		t.Fatalf("其他项目目录借用了任务编号: %+v %v", result, err)
	}

	// 最近的清单若已指向不存在任务，应继续向父目录查找。
	fallbackDir := filepath.Join(t.TempDir(), "outer", "inner")
	if err := os.MkdirAll(fallbackDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(fallbackDir), "danlu.json"), manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fallbackDir, "danlu.json"), []byte(`{"id":999999}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err = Search(st, root, filepath.Join(fallbackDir, "latest.pt"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 1 || result.Matches[0].Job.ID != job.ID || result.Matches[0].Source != SourceManifest {
		t.Fatalf("parent manifest fallback failed: %+v", result.Matches)
	}
}

func TestSearchValidatesAbsolutePathAndReturnsEmptyMatches(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	if _, err := Search(st, t.TempDir(), "relative/path"); err == nil {
		t.Fatal("relative path was accepted")
	}
	result, err := Search(st, t.TempDir(), filepath.Join(t.TempDir(), "unmatched.pt"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Matches == nil || len(result.Matches) != 0 {
		t.Fatalf("empty result should encode as an empty array: %+v", result)
	}
}
