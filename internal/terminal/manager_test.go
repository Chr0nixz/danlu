package terminal

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// pipeProcess 用管道代替 PTY：往 out 写的内容就是 shell 的输出。
type pipeProcess struct {
	r   *io.PipeReader
	out *io.PipeWriter
}

func newPipeProcess() *pipeProcess {
	r, w := io.Pipe()
	return &pipeProcess{r: r, out: w}
}

func (p *pipeProcess) Read(b []byte) (int, error)  { return p.r.Read(b) }
func (p *pipeProcess) Write(b []byte) (int, error) { return len(b), nil }
func (p *pipeProcess) Resize(int, int) error       { return nil }
func (p *pipeProcess) Close() error {
	_ = p.out.Close()
	return p.r.Close()
}

func addPipeSession(t *testing.T, m *Manager) (string, *pipeProcess) {
	t.Helper()
	p := newPipeProcess()
	s := newSession("s1", "/tmp", p, m.now())
	m.mu.Lock()
	m.sessions[s.info.ID] = s
	m.workers.Add(1)
	m.mu.Unlock()
	go m.readOutput(s)
	return s.info.ID, p
}

func writeOutput(t *testing.T, m *Manager, p *pipeProcess, id, text string) {
	t.Helper()
	if _, err := p.out.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		m.mu.Lock()
		s := m.sessions[id]
		m.mu.Unlock()
		s.mu.Lock()
		n := len(s.output.data)
		s.mu.Unlock()
		if n > 0 && string(s.output.data[max(0, n-len(text)):]) == text {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("output %q was not buffered", text)
}

func next(t *testing.T, a *Attachment, offset uint64) (string, uint64, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	data, from, err := a.Next(ctx, offset)
	return string(data), from, err
}

func TestAttachResumesFromOffset(t *testing.T) {
	m := NewManager()
	defer m.Close()
	id, p := addPipeSession(t, m)
	writeOutput(t, m, p, id, "hello world")

	a, err := m.Attach(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if got, from, err := next(t, a, a.Start); err != nil || got != "hello world" || from != 0 {
		t.Fatalf("first attach = %q from %d, %v", got, from, err)
	}
	a.Detach()

	b, err := m.Attach(id, 6)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Detach()
	if got, from, err := next(t, b, b.Start); err != nil || got != "world" || from != 6 {
		t.Fatalf("resume from 6 = %q from %d, %v", got, from, err)
	}
	// 等待中的连接被新输出唤醒，只拿到新增的部分
	done := make(chan string, 1)
	go func() {
		got, _, _ := next(t, b, 11)
		done <- got
	}()
	time.Sleep(20 * time.Millisecond)
	writeOutput(t, m, p, id, "!")
	if got := <-done; got != "!" {
		t.Fatalf("woken read = %q, want %q", got, "!")
	}
	if b2, err := m.Attach(id, 1<<40); err != nil || b2.Start != 12 {
		t.Fatalf("offset past end should clamp to end: %+v, %v", b2, err)
	}
}

func TestOffsetBeforeBufferStartReportsGap(t *testing.T) {
	var b outputBuffer
	b.append(make([]byte, BufferSize))
	b.append([]byte("tail"))
	data, from := b.readAfter(0, 8)
	if from != 4 || len(data) != 8 {
		t.Fatalf("readAfter(0) = %d bytes from %d, want 8 from 4", len(data), from)
	}
	data, from = b.readAfter(b.end()-4, 8)
	if string(data) != "tail" || from != b.end()-4 {
		t.Fatalf("readAfter(end-4) = %q from %d", data, from)
	}
}

func TestNewAttachmentReplacesOld(t *testing.T) {
	m := NewManager()
	defer m.Close()
	id, _ := addPipeSession(t, m)
	first, err := m.Attach(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan error, 1)
	go func() {
		_, _, err := next(t, first, 0)
		waiting <- err
	}()
	second, err := m.Attach(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-waiting; !errors.Is(err, ErrReplaced) {
		t.Fatalf("old attachment error = %v, want ErrReplaced", err)
	}
	if err := first.Write([]byte("x")); !errors.Is(err, ErrReplaced) {
		t.Fatalf("old attachment write error = %v, want ErrReplaced", err)
	}
	first.Detach()
	if got := m.List(); len(got) != 1 || !got[0].Attached {
		t.Fatalf("detaching the replaced attachment must not detach the new one: %+v", got)
	}
	second.Detach()
	if got := m.List(); got[0].Attached {
		t.Fatalf("session still attached after detach: %+v", got)
	}
}

func TestShellExitDeliversLastOutput(t *testing.T) {
	m := NewManager()
	defer m.Close()
	id, p := addPipeSession(t, m)
	a, err := m.Attach(id, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Detach()
	if _, err := p.out.Write([]byte("logout\n")); err != nil {
		t.Fatal(err)
	}
	_ = p.out.Close()
	got, from, err := next(t, a, 0)
	if err != nil || got != "logout\n" {
		t.Fatalf("last output = %q, %v", got, err)
	}
	if _, _, err := next(t, a, from+uint64(len(got))); !errors.Is(err, io.EOF) {
		t.Fatalf("after shell exit error = %v, want EOF", err)
	}
}

func TestCondaEnvNameOrAbsolutePath(t *testing.T) {
	for _, env := range []string{"", "torch", "py3.10-cu121_dev", "/home/u/.conda/envs/torch", "/data/my envs/x"} {
		if !validCondaEnv(env) {
			t.Errorf("validCondaEnv(%q) = false", env)
		}
	}
	for _, env := range []string{"../bad", "a/b", "a b", "$(id)", "/x\ny"} {
		if validCondaEnv(env) {
			t.Errorf("validCondaEnv(%q) = true", env)
		}
	}
	for env, want := range map[string]string{
		"torch":                     "torch",
		"/home/u/.conda/envs/torch": "/home/u/.conda/envs/torch",
		"/data/my envs/x":           "'/data/my envs/x'",
		"/data/it's":                `'/data/it'\''s'`,
	} {
		if got := shellWord(env); got != want {
			t.Errorf("shellWord(%q) = %q, want %q", env, got, want)
		}
	}
}
