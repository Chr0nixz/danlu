//go:build linux

package api

import (
	"context"
	"encoding/json"
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
	"danlu/internal/store"
	"danlu/internal/terminal"

	"github.com/coder/websocket"
)

func TestTerminalHTTPWebSocketLifecycle(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := auth.SetPassword(st, "testpass123"); err != nil {
		t.Fatal(err)
	}
	m := terminal.NewManager()
	defer m.Close()
	au := auth.New(st, auth.Options{Entrance: "abcdef", Secret: []byte("test-secret"), Username: "admin", SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	cfg := config.Default()
	cfg.Terminal.Enabled = true
	srv := &Server{Cfg: cfg, Paths: config.PathsFor(home), Store: st, Auth: au, Terminal: m}
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(httpSrv.URL + "/abcdef")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("entrance status = %d", resp.StatusCode)
	}
	if status := apiStatus(t, client, httpSrv.URL, http.MethodPost, "/api/login", `{"username":"admin","password":"testpass123"}`); status != http.StatusOK {
		t.Fatalf("login status = %d", status)
	}

	if status := apiStatus(t, client, httpSrv.URL, http.MethodGet, "/api/terminal", ""); status != http.StatusOK {
		t.Fatalf("list status = %d", status)
	}
	created := apiRequest(t, client, httpSrv.URL, http.MethodPost, "/api/terminal", `{}`)
	if created.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(created.Body)
		created.Body.Close()
		t.Fatalf("open status = %d: %s", created.StatusCode, body)
	}
	var info terminal.SessionInfo
	if err := json.NewDecoder(created.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	created.Body.Close()
	if info.ID == "" || info.Workdir == "" {
		t.Fatalf("incomplete session response: %+v", info)
	}
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/api/terminal/" + info.ID + "/ws"

	badOrigin := http.Header{"Origin": []string{"https://attacker.example"}}
	if conn, _, err := websocket.Dial(context.Background(), wsURL, &websocket.DialOptions{HTTPClient: client, HTTPHeader: badOrigin}); err == nil {
		_ = conn.Close(websocket.StatusNormalClosure, "")
		t.Fatal("cross-origin WebSocket was accepted")
	}

	conn := dialTerminal(t, client, wsURL)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf '\\x74erminal-ws-marker\\n'; sleep 1\n")); err != nil {
		t.Fatal(err)
	}
	got, received := readWSUntil(t, ctx, conn, 0, "terminal-ws-marker")
	if !strings.Contains(got, "terminal-ws-marker") {
		t.Fatalf("terminal output missing marker: %q", got)
	}
	_ = conn.Write(ctx, websocket.MessageBinary, []byte("printf '\\x62uffered-ws-marker\\n'; sleep 1\n"))
	_ = conn.Close(websocket.StatusNormalClosure, "test reconnect")
	waitTerminalDetached(t, client, httpSrv.URL, info.ID)
	waitManagerOutput(t, m, info.ID, "buffered-ws-marker")

	// 带上已收到的偏移重连：只补发断开期间的输出，不重复之前的
	conn = dialTerminal(t, client, wsURL+"?offset="+strconv.FormatUint(received, 10))
	if got, _ := readWSUntil(t, ctx, conn, received, "buffered-ws-marker"); strings.Contains(got, "terminal-ws-marker") {
		t.Fatalf("reconnected WebSocket replayed output it already sent: %q", got)
	}

	// 另一个连接接走会话，旧连接收到 4001 关闭
	other := dialTerminal(t, client, wsURL+"?offset="+strconv.FormatUint(received, 10))
	for {
		if _, _, err := conn.Read(ctx); err != nil {
			if websocket.CloseStatus(err) != terminalReplaced {
				t.Fatalf("replaced connection closed with %v, want %d", err, terminalReplaced)
			}
			break
		}
	}
	_ = other.Close(websocket.StatusNormalClosure, "test complete")
	waitTerminalDetached(t, client, httpSrv.URL, info.ID)

	for i := 0; i < terminal.MaxSessions-1; i++ {
		resp := apiRequest(t, client, httpSrv.URL, http.MethodPost, "/api/terminal", `{}`)
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("open extra session status = %d", resp.StatusCode)
		}
		resp.Body.Close()
	}
	if status := apiStatus(t, client, httpSrv.URL, http.MethodPost, "/api/terminal", `{}`); status != http.StatusConflict {
		t.Fatalf("fourth session status = %d, want 409", status)
	}

	if status := apiStatus(t, client, httpSrv.URL, http.MethodPost, "/api/terminal/"+info.ID+"/close", `{}`); status != http.StatusOK {
		t.Fatalf("close status = %d", status)
	}
	cfg.Terminal.Enabled = false
	if status := apiStatus(t, client, httpSrv.URL, http.MethodGet, "/api/terminal", ""); status != http.StatusNotFound {
		t.Fatalf("disabled list status = %d", status)
	}
	if status := apiStatus(t, client, httpSrv.URL, http.MethodPost, "/api/terminal", `{}`); status != http.StatusNotFound {
		t.Fatalf("disabled open status = %d", status)
	}
}

// 握手之后撤权：保持着的终端连接不能再执行命令，也不能继续收输出
func TestTerminalRevokedCredentialsCloseConnection(t *testing.T) {
	old := terminalAuthInterval
	terminalAuthInterval = 100 * time.Millisecond
	defer func() { terminalAuthInterval = old }()
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := auth.SetPassword(st, "testpass123"); err != nil {
		t.Fatal(err)
	}
	m := terminal.NewManager()
	defer m.Close()
	au := auth.New(st, auth.Options{Entrance: "abcdef", Secret: []byte("test-secret"), Username: "admin", SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	cfg := config.Default()
	cfg.Terminal.Enabled = true
	srv := &Server{Cfg: cfg, Paths: config.PathsFor(home), Store: st, Auth: au, Terminal: m}
	httpSrv := httptest.NewServer(srv.Handler())
	defer httpSrv.Close()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := client.Get(httpSrv.URL + "/abcdef")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if status := apiStatus(t, client, httpSrv.URL, http.MethodPost, "/api/login", `{"username":"admin","password":"testpass123"}`); status != http.StatusOK {
		t.Fatalf("login status = %d", status)
	}
	open := func() string {
		created := apiRequest(t, client, httpSrv.URL, http.MethodPost, "/api/terminal", `{}`)
		defer created.Body.Close()
		var info terminal.SessionInfo
		if err := json.NewDecoder(created.Body).Decode(&info); err != nil || info.ID == "" {
			t.Fatalf("open terminal: %v %+v", err, info)
		}
		return "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/api/terminal/" + info.ID + "/ws"
	}
	waitClosed := func(ctx context.Context, conn *websocket.Conn) {
		t.Helper()
		for {
			if _, _, err := conn.Read(ctx); err != nil {
				if websocket.CloseStatus(err) != terminalUnauthorized {
					t.Fatalf("revoked connection closed with %v, want %d", err, terminalUnauthorized)
				}
				return
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 会话被注销（danlu passwd 会删掉所有会话）后，下一条输入不执行
	conn := dialTerminal(t, client, open())
	if err := conn.Write(ctx, websocket.MessageBinary, []byte("printf '\\x72eady-marker\\n'\n")); err != nil {
		t.Fatal(err)
	}
	readWSUntil(t, ctx, conn, 0, "ready-marker")
	if err := st.DeleteSessionsExcept(""); err != nil {
		t.Fatal(err)
	}
	created := filepath.Join(home, "revoked-input")
	_ = conn.Write(ctx, websocket.MessageBinary, []byte("touch "+created+"\n"))
	waitClosed(ctx, conn)
	time.Sleep(300 * time.Millisecond)
	if _, err := os.Stat(created); !os.IsNotExist(err) {
		t.Fatalf("撤权后的输入仍被执行: %v", err)
	}

	// 设备令牌被吊销后，没有输入的连接也在下一次复查时断开
	if status := apiStatus(t, client, httpSrv.URL, http.MethodPost, "/api/login", `{"username":"admin","password":"testpass123"}`); status != http.StatusOK {
		t.Fatalf("relogin status = %d", status)
	}
	wsURL := open()
	id, tok, err := auth.NewDeviceToken(st, "test")
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{HTTPHeader: http.Header{"Authorization": []string{"Bearer " + tok}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RevokeDeviceToken(id); err != nil {
		t.Fatal(err)
	}
	waitClosed(ctx, device)
}

func apiRequest(t *testing.T, client *http.Client, base, method, path, body string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func apiStatus(t *testing.T, client *http.Client, base, method, path, body string) int {
	t.Helper()
	resp := apiRequest(t, client, base, method, path, body)
	resp.Body.Close()
	return resp.StatusCode
}

func dialTerminal(t *testing.T, client *http.Client, url string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, url, &websocket.DialOptions{HTTPClient: client})
	if err != nil {
		t.Fatal(err)
	}
	return conn
}

// readWSUntil 像面板一样记下收到的输出末尾：offset 消息重置起点，二进制帧往后累加。
func readWSUntil(t *testing.T, ctx context.Context, conn *websocket.Conn, received uint64, marker string) (string, uint64) {
	t.Helper()
	var output strings.Builder
	for !strings.Contains(output.String(), marker) {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if typ == websocket.MessageBinary {
			output.Write(data)
			received += uint64(len(data))
			continue
		}
		var msg struct {
			Type   string `json:"type"`
			Offset uint64 `json:"offset"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.Type == "offset" {
			received = msg.Offset
		}
	}
	return output.String(), received
}

func waitTerminalDetached(t *testing.T, client *http.Client, base, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp := apiRequest(t, client, base, http.MethodGet, "/api/terminal", "")
		var body struct {
			Sessions []terminal.SessionInfo `json:"sessions"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		for _, session := range body.Sessions {
			if session.ID == id && !session.Attached {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("terminal WebSocket did not detach")
}

func waitManagerOutput(t *testing.T, m *terminal.Manager, id, marker string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, info := range m.List() {
			if info.ID != id {
				continue
			}
			att, err := m.Attach(id, 0)
			if err != nil {
				continue
			}
			var data strings.Builder
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			for cursor := att.Start; ; {
				chunk, from, err := att.Next(ctx, cursor)
				if err != nil {
					break
				}
				data.Write(chunk)
				cursor = from + uint64(len(chunk))
			}
			cancel()
			att.Detach()
			if strings.Contains(data.String(), marker) {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("terminal manager did not buffer output")
}
