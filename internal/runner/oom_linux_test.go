//go:build linux

package runner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOOMKillsOverride(t *testing.T) {
	p := filepath.Join(t.TempDir(), "memory.events")
	if err := os.WriteFile(p, []byte("low 3\noom 1\noom_kill 7\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DANLU_MEMORY_EVENTS", p)
	if got, ok := (&Runner{}).OOMKills(); !ok || got != 7 {
		t.Fatalf("OOMKills = %d, %t，期望 7, true", got, ok)
	}
}

func TestOOMKillsRejectsMissingCounter(t *testing.T) {
	p := filepath.Join(t.TempDir(), "memory.events")
	if err := os.WriteFile(p, []byte("low 3\noom 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DANLU_MEMORY_EVENTS", p)
	if got, ok := (&Runner{}).OOMKills(); ok {
		t.Fatalf("OOMKills = %d, true，缺少 oom_kill 时应为 false", got)
	}
}
