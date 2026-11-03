package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"danlu/internal/auth"
	"danlu/internal/config"
	"danlu/internal/store"
)

func TestTraceEndpointRequiresSessionAndFindsOutputDirectory(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := auth.SetPassword(st, "testpass123"); err != nil {
		t.Fatal(err)
	}

	root := filepath.Join(home, "outputs")
	job := &store.Job{Name: "train", Workdir: filepath.Join(home, "project"), Cmd: "python train.py", Status: store.StatusSucceeded}
	if err := st.InsertJob(job); err != nil {
		t.Fatal(err)
	}
	job.OutputDir = filepath.Join(root, "project", "1-train")
	if err := st.SaveJob(job); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Runtime.OutputRoot = root
	au := auth.New(st, auth.Options{Entrance: "abcdef", Secret: []byte("test-secret"), Username: "admin", SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	server := httptest.NewServer((&Server{Cfg: cfg, Store: st, Auth: au}).Handler())
	defer server.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	entrance, err := client.Get(server.URL + "/abcdef")
	if err != nil {
		t.Fatal(err)
	}
	entrance.Body.Close()

	path := filepath.Join(job.OutputDir, "checkpoints", "latest.pt")
	traceURL := server.URL + "/api/trace?path=" + url.QueryEscape(path)
	unauth, err := client.Get(traceURL)
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated trace status = %d", unauth.StatusCode)
	}

	login, err := client.Post(server.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"testpass123"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()
	if login.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", login.StatusCode)
	}

	resp, err := client.Get(traceURL)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("trace status = %d: %s", resp.StatusCode, body)
	}
	if got := resp.Header.Get("Cache-Control"); got != "private, no-store" {
		t.Fatalf("Cache-Control = %q", got)
	}
	var result struct {
		Path    string `json:"path"`
		Matches []struct {
			Job    store.Job `json:"job"`
			Source string    `json:"source"`
		} `json:"matches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Path != path || len(result.Matches) != 1 || result.Matches[0].Job.ID != job.ID || result.Matches[0].Source != "output_dir" {
		t.Fatalf("trace result = %+v", result)
	}
}

func TestTraceEndpointRejectsRelativePath(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := auth.SetPassword(st, "testpass123"); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	au := auth.New(st, auth.Options{Entrance: "abcdef", Secret: []byte("test-secret"), Username: "admin", SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	server := httptest.NewServer((&Server{Cfg: cfg, Store: st, Auth: au}).Handler())
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	entrance, err := client.Get(server.URL + "/abcdef")
	if err != nil {
		t.Fatal(err)
	}
	entrance.Body.Close()
	login, err := client.Post(server.URL+"/api/login", "application/json", strings.NewReader(`{"username":"admin","password":"testpass123"}`))
	if err != nil {
		t.Fatal(err)
	}
	login.Body.Close()

	resp, err := client.Get(server.URL + "/api/trace?path=relative")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("relative path status = %d", resp.StatusCode)
	}
}
