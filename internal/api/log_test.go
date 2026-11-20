package api

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"danlu/internal/auth"
	"danlu/internal/config"
	"danlu/internal/runner"
	"danlu/internal/store"
)

// 握手之后撤权：运行中任务的日志流要在下一次复查时断开，不能一直推到任务结束
func TestLogStreamClosesAfterRevocation(t *testing.T) {
	old := logAuthInterval
	logAuthInterval = 100 * time.Millisecond
	defer func() { logAuthInterval = old }()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := auth.SetPassword(st, "testpass123"); err != nil {
		t.Fatal(err)
	}
	j := &store.Job{Name: "train", Workdir: home, Cmd: "sleep 1000", Status: store.StatusRunning}
	if err := st.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	run := &runner.Runner{LogRoot: home}
	if err := os.WriteFile(run.LogPath(j.ID), []byte("step 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	au := auth.New(st, auth.Options{Entrance: "abcdef", Secret: []byte("test-secret"), Username: "admin", SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	srv := &Server{Cfg: config.Default(), Paths: config.PathsFor(home), Store: st, Auth: au, Runner: run}
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if resp, err := client.Get(httpSrv.URL + "/abcdef"); err != nil {
		t.Fatal(err)
	} else {
		resp.Body.Close()
	}
	login, err := client.Post(httpSrv.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"testpass123"}`))
	if err != nil || login.StatusCode != http.StatusOK {
		t.Fatalf("login: %v %v", err, login)
	}
	login.Body.Close()

	resp, err := client.Get(httpSrv.URL + "/api/jobs/" + strconv.FormatInt(j.ID, 10) + "/log/stream?offset=0")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("打开日志流: %v %v", err, resp)
	}
	defer resp.Body.Close()
	buf := make([]byte, 4096)
	if n, err := resp.Body.Read(buf); err != nil || !strings.Contains(string(buf[:n]), "step 1") {
		t.Fatalf("应先收到日志: %q %v", buf[:n], err)
	}
	if err := st.DeleteSessionsExcept(""); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, resp.Body); done <- err }()
	select {
	case <-done: // 服务器结束了响应
	case <-time.After(3 * time.Second):
		t.Fatal("撤权后日志流没有断开")
	}
}

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

// 日志下载带 Content-Length，支持 Range：手机网络断了能接着下
func TestLogDownloadSupportsRange(t *testing.T) {
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
	if err := os.WriteFile(run.LogPath(j.ID), []byte("0123456789"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := &Server{Store: st, Runner: run}
	get := func(rangeHeader string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/api/jobs/1/log/download", nil)
		r.SetPathValue("id", strconv.FormatInt(j.ID, 10))
		if rangeHeader != "" {
			r.Header.Set("Range", rangeHeader)
		}
		w := httptest.NewRecorder()
		srv.jobLogDownload(w, r)
		return w
	}
	w := get("")
	if w.Code != 200 || w.Body.String() != "0123456789" || w.Header().Get("Content-Length") != "10" ||
		!strings.HasPrefix(w.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("完整下载 = %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	w = get("bytes=4-")
	if w.Code != http.StatusPartialContent || w.Body.String() != "456789" || w.Header().Get("Content-Range") != "bytes 4-9/10" {
		t.Fatalf("续传 = %d %q %v", w.Code, w.Body.String(), w.Header())
	}
}
