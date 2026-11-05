//go:build linux

package runner

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestCapturePackagesUsesConfiguredConda(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	conda := filepath.Join(bin, "conda")
	if err := os.WriteFile(conda, []byte("#!/bin/sh\nprintf 'numpy==1.2\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{CondaBase: base}
	got, err := r.CapturePackages(context.Background(), Spec{CondaEnv: "train"}, []string{"PATH=/bin"})
	if err != nil || string(got) != "numpy==1.2\n" {
		t.Fatalf("CapturePackages = %q, %v", got, err)
	}
}

// 路径形式的环境（不在 <conda_base>/envs 下）要用 -p，按名字的环境用 -n
func TestCapturePackagesPathEnvUsesPrefixFlag(t *testing.T) {
	base := t.TempDir()
	bin := filepath.Join(base, "bin")
	if err := os.MkdirAll(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	// 假 conda 把收到的参数原样打印出来
	if err := os.WriteFile(filepath.Join(bin, "conda"), []byte("#!/bin/sh\necho \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	r := &Runner{CondaBase: base}
	for env, want := range map[string]string{
		"/home/u/.conda/envs/x": "list -p /home/u/.conda/envs/x --export\n",
		"train":                 "list -n train --export\n",
	} {
		got, err := r.CapturePackages(context.Background(), Spec{CondaEnv: env}, []string{"PATH=/bin"})
		if err != nil || string(got) != want {
			t.Fatalf("环境 %s：CapturePackages = %q, %v，应为 %q", env, got, err, want)
		}
	}
}

// 采集包清单的进程不能带 DANLU_RUN_DIR，否则任务收尾时会被当成残留进程清理掉
func TestCapturePackagesHasNoJobMarker(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "python3"), []byte("#!/bin/sh\necho \"marker=${DANLU_RUN_DIR:-none} id=$DANLU_JOB_ID\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := &Runner{RunRoot: "/data/run"}
	got, err := r.CapturePackages(context.Background(), Spec{JobID: 3}, []string{"PATH=" + bin})
	if err != nil || string(got) != "marker=none id=3\n" {
		t.Fatalf("CapturePackages = %q, %v", got, err)
	}
}

func TestCapturePackagesUsesTaskPython(t *testing.T) {
	bin := t.TempDir()
	python := filepath.Join(bin, "python3")
	if err := os.WriteFile(python, []byte("#!/bin/sh\nprintf 'torch==2.5\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	r := &Runner{}
	got, err := r.CapturePackages(context.Background(), Spec{}, []string{"PATH=" + bin})
	if err != nil || string(got) != "torch==2.5\n" {
		t.Fatalf("CapturePackages = %q, %v", got, err)
	}
}
