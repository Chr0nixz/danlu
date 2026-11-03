//go:build linux

package terminal

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestShellInputAndReconnectBuffer(t *testing.T) {
	m := NewManager()
	defer m.Close()
	info, err := m.Open(OpenOptions{Workdir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.Attach(info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Write([]byte("printf '\\x74erminal-output-marker\\n'; sleep 1\n")); err != nil {
		t.Fatal(err)
	}
	output, cursor := readUntil(t, a, a.Start, "terminal-output-marker")
	if !strings.Contains(output, "terminal-output-marker") {
		t.Fatalf("terminal output missing marker: %q", output)
	}
	a.Detach()
	if err := a.Write([]byte("printf '\\x62uffered-output-marker\\n'; sleep 1\n")); err != nil {
		t.Fatal(err)
	}
	waitForBuffer(t, m, info.ID, "buffered-output-marker")
	b, err := m.Attach(info.ID, cursor)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Detach()
	resumed, _ := readUntil(t, b, b.Start, "buffered-output-marker")
	if strings.Contains(resumed, "terminal-output-marker") {
		t.Fatalf("reconnect with offset replayed output the client already had: %q", resumed)
	}
}

func TestSessionLimitAndCondaValidation(t *testing.T) {
	m := NewManager()
	defer m.Close()
	dir := t.TempDir()
	for i := 0; i < MaxSessions; i++ {
		if _, err := m.Open(OpenOptions{Workdir: dir}); err != nil {
			t.Fatalf("open session %d: %v", i, err)
		}
	}
	if _, err := m.Open(OpenOptions{Workdir: dir}); !errors.Is(err, ErrLimit) {
		t.Fatalf("fourth session error = %v, want ErrLimit", err)
	}
	if _, err := m.Open(OpenOptions{Workdir: dir, CondaEnv: "../bad"}); err == nil {
		t.Fatal("invalid conda environment name was accepted")
	}
}

func TestCondaEnvPathIsActivated(t *testing.T) {
	m := NewManager()
	defer m.Close()
	base := t.TempDir()
	// 假的 conda.sh：conda activate 只把参数打印出来
	if err := os.MkdirAll(filepath.Join(base, "etc", "profile.d"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := "conda() { printf 'activated:%s\\n' \"$2\"; }\n"
	if err := os.WriteFile(filepath.Join(base, "etc", "profile.d", "conda.sh"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	env := filepath.Join(t.TempDir(), "my envs", "torch")
	info, err := m.Open(OpenOptions{Workdir: t.TempDir(), CondaEnv: env, CondaBase: base})
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.Attach(info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Detach()
	if output, _ := readUntil(t, a, a.Start, "activated:"+env); !strings.Contains(output, "activated:"+env) {
		t.Fatalf("conda env path was not activated: %q", output)
	}
}

func TestDetachedSessionsExpire(t *testing.T) {
	clock := atomic.Int64{}
	clock.Store(time.Now().UnixNano())
	m := newManager(func() time.Time { return time.Unix(0, clock.Load()) })
	defer m.Close()
	info, err := m.Open(OpenOptions{Workdir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	a, err := m.Attach(info.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	a.Detach()
	clock.Add(int64(SessionLifetime))
	m.reapExpired()
	if got := m.List(); len(got) != 0 {
		t.Fatalf("expired session remains: %+v", got)
	}
}

func readUntil(t *testing.T, a *Attachment, cursor uint64, target string) (string, uint64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var output strings.Builder
	for !strings.Contains(output.String(), target) {
		chunk, from, err := a.Next(ctx, cursor)
		if err != nil {
			t.Fatalf("read terminal output: %v (got %q)", err, output.String())
		}
		output.Write(chunk)
		cursor = from + uint64(len(chunk))
	}
	return output.String(), cursor
}

func waitForBuffer(t *testing.T, m *Manager, id, target string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		s := m.sessions[id]
		var output string
		if s != nil {
			s.mu.Lock()
			output = string(s.output.data)
			s.mu.Unlock()
		}
		m.mu.Unlock()
		if strings.Contains(output, target) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("terminal buffer did not contain %q", target)
}
