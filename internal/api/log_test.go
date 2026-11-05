package api

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"danlu/internal/runner"
	"danlu/internal/store"
)

// 已结束任务的日志末尾只有半个字符：SSE 要把它发出去（变成 U+FFFD）再发 end，不能一直显示"实时"
func TestLogStreamEndsAfterTruncatedCharacter(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	j := &store.Job{Name: "train", Workdir: home, Cmd: "true", Status: store.StatusSucceeded}
	if err := st.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	run := &runner.Runner{LogRoot: home}
	if err := os.WriteFile(run.LogPath(j.ID), []byte("done\n训"[:7]), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Store: st, Runner: run}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, "GET", "/api/jobs/1/log/stream?offset=5", nil)
	r.SetPathValue("id", strconv.FormatInt(j.ID, 10))
	w := httptest.NewRecorder()
	srv.jobLogStream(w, r)
	body := w.Body.String()
	if ctx.Err() != nil || !strings.Contains(body, `"t":"�"`) || !strings.Contains(body, `"n":7`) || !strings.HasSuffix(body, "event: end\ndata: {\"status\":\"succeeded\"}\n\n") {
		t.Fatalf("应该发出残缺字符再结束: %q", body)
	}
}
