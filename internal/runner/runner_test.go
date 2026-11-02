package runner

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"danlu/internal/store"
)

func TestLaunched(t *testing.T) {
	r := &Runner{RunRoot: t.TempDir()}
	if _, ok := r.Launched(1); ok {
		t.Fatal("没有 pid 文件时不应视为启动过")
	}
	if err := os.MkdirAll(r.RunDir(1), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, data := range []string{"4242\n", "损坏的文件"} {
		if err := os.WriteFile(filepath.Join(r.RunDir(1), "pid"), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		pid, ok := r.Launched(1)
		if !ok || (data == "4242\n" && pid != 4242) || (data != "4242\n" && pid != 0) {
			t.Fatalf("Launched(%q) = %d %t", data, pid, ok)
		}
	}
}

func TestBootIDOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "boot_id")
	if err := os.WriteFile(p, []byte("test-boot-id\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DANLU_BOOT_ID_FILE", p)
	if got := (&Runner{}).BootID(); got != "test-boot-id" {
		t.Fatalf("BootID = %q，期望 test-boot-id", got)
	}
}

func TestStorePackagesDeduplicatesByHash(t *testing.T) {
	r := &Runner{EnvRoot: t.TempDir()}
	packages := []byte("numpy==1.2\ntorch==2.5\n")
	wantBytes := sha256.Sum256(packages)
	want := hex.EncodeToString(wantBytes[:])
	for range 2 {
		if got, err := r.StorePackages(packages); err != nil || got != want {
			t.Fatalf("StorePackages = %q, %v，期望 %q", got, err, want)
		}
	}
	files, err := os.ReadDir(r.EnvRoot)
	if err != nil || len(files) != 1 || files[0].Name() != want+".txt" {
		t.Fatalf("相同环境应只保存一份: %v %v", files, err)
	}
	got, err := os.ReadFile(filepath.Join(r.EnvRoot, want+".txt"))
	if err != nil || string(got) != string(packages) {
		t.Fatalf("包清单未原样保存: %q %v", got, err)
	}
}

func TestOutputPathKeepsTaskNameInsideProject(t *testing.T) {
	got := OutputPath(filepath.Join(t.TempDir(), "outputs"), "/work/project", 42, "../train\\test")
	if filepath.Base(got) != "42-.._train_test" || filepath.Base(filepath.Dir(got)) != "project" {
		t.Fatalf("OutputPath escaped or changed project grouping: %q", got)
	}
}

func TestBuildEnvInjectsOutputDirectory(t *testing.T) {
	r := &Runner{}
	env := r.BuildEnv([]string{"DANLU_OUTPUT_DIR=/wrong", "PATH=/bin"}, Spec{JobID: 7, OutputDir: "/outputs/project/7-run"})
	values := map[string]string{}
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok {
			values[key] = value
		}
	}
	if values["DANLU_OUTPUT_DIR"] != "/outputs/project/7-run" {
		t.Fatalf("DANLU_OUTPUT_DIR = %q", values["DANLU_OUTPUT_DIR"])
	}
}

func TestManifestUpdatesFinalState(t *testing.T) {
	root := t.TempDir()
	output := filepath.Join(root, "project", "8-run")
	started := int64(123)
	job := &store.Job{ID: 8, Name: "run", Workdir: "/work/project", Cmd: "python train.py",
		Env: map[string]string{"MODE": "train"}, OutputDir: output, StartedAt: &started, Status: "running",
		Recipe: &store.Recipe{EnvHash: "abc", Machine: &store.RecipeMachine{Hostname: "node-a", DriverVersion: "560.94",
			CUDAVersion: "12.6", GPUs: []store.RecipeGPU{{Index: 0, UUID: "GPU-abc", Name: "A100"}}}}}
	r := &Runner{}
	write := func() {
		t.Helper()
		data, err := ManifestJSON(job)
		if err != nil {
			t.Fatal(err)
		}
		if err := r.WriteManifest(output, data); err != nil {
			t.Fatal(err)
		}
	}
	write()
	code := 0
	job.Status, job.ExitCode = "succeeded", &code
	write()
	data, err := os.ReadFile(filepath.Join(output, "danlu.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["status"] != "succeeded" || got["server"] != "node-a" || got["output_dir"] != output {
		t.Fatalf("任务清单没有更新为最终状态: %s", data)
	}
	recipe := got["recipe"].(map[string]any)
	if recipe["env_hash"] != "abc" {
		t.Fatalf("任务清单缺少环境哈希: %s", data)
	}
}

func TestBuildEnv(t *testing.T) {
	r := &Runner{RunRoot: "/run", LogRoot: "/logs", CondaBase: "/conda"}
	base := []string{"PATH=/usr/bin", "CUDA_VISIBLE_DEVICES=7", "WANDB_TAGS=exp", "DANLU_JOB_ID=99"}
	env := r.BuildEnv(base, Spec{JobID: 12, Name: "x", GPUs: []int{0, 2}, Port: 29555,
		Env: map[string]string{"LR": "0.1", "CUDA_VISIBLE_DEVICES": "5"}})
	get := func(k string) string {
		for _, kv := range env {
			if v, ok := strings.CutPrefix(kv, k+"="); ok {
				return v
			}
		}
		return "<unset>"
	}
	checks := map[string]string{
		"PATH": "/usr/bin", "LR": "0.1", "CUDA_VISIBLE_DEVICES": "0,2", "CUDA_DEVICE_ORDER": "PCI_BUS_ID",
		"PET_MASTER_PORT": "29555", "PYTHONUNBUFFERED": "1", "DANLU_JOB_ID": "12", "WANDB_TAGS": "exp,danlu-12",
	}
	for k, want := range checks {
		if got := get(k); got != want {
			t.Errorf("%s = %q，期望 %q", k, got, want)
		}
	}
	n := 0
	for _, kv := range env {
		if strings.HasPrefix(kv, "CUDA_VISIBLE_DEVICES=") {
			n++
		}
	}
	if n != 1 {
		t.Errorf("CUDA_VISIBLE_DEVICES 出现了 %d 次", n)
	}
	if !slices.Contains(env, "CUDA_VISIBLE_DEVICES=0,2") {
		t.Error("用户和系统的 CUDA_VISIBLE_DEVICES 都应被覆盖")
	}
}

func TestBuildEnvBashrcPrecedence(t *testing.T) {
	r := &Runner{RunRoot: "/run", LogRoot: "/logs", CondaBase: "/conda"}
	env := r.BuildEnv([]string{"PATH=/service", "TOKEN=service", "PATH=/bashrc", "TOKEN=bashrc"}, Spec{
		JobID: 1, Name: "x", Workdir: "/work", Cmd: "true", Env: map[string]string{"TOKEN": "task"},
	})
	got := map[string]string{}
	for _, kv := range env {
		k, v, ok := strings.Cut(kv, "=")
		if ok {
			got[k] = v
		}
	}
	if got["PATH"] != "/bashrc" || got["TOKEN"] != "task" {
		t.Fatalf("PATH=%q TOKEN=%q，期望 bashrc 覆盖服务环境、任务变量覆盖 bashrc", got["PATH"], got["TOKEN"])
	}
}

func TestParseBashEnvironment(t *testing.T) {
	output := append([]byte(".bashrc startup output\n"+bashEnvMarker), []byte("PATH=/custom/bin\x00TOKEN=a=b\x00MULTILINE=one\ntwo\x00")...)
	env, err := parseBashEnvironment(output)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"PATH=/custom/bin", "TOKEN=a=b", "MULTILINE=one\ntwo"}
	if !slices.Equal(env, want) {
		t.Fatalf("parseBashEnvironment = %#v，期望 %#v", env, want)
	}
}
