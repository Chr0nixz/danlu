package runner

import (
	"slices"
	"strings"
	"testing"
)

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
