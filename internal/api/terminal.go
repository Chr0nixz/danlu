package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"danlu/internal/auth"
	"danlu/internal/config"
	"danlu/internal/snapshot"
	"danlu/internal/store"
	"danlu/internal/terminal"

	"github.com/coder/websocket"
)

func (s *Server) terminalManager(w http.ResponseWriter) *terminal.Manager {
	if !s.Cfg.Terminal.Enabled {
		writeErr(w, http.StatusNotFound, "接口不存在")
		return nil
	}
	if s.Terminal == nil {
		writeErr(w, http.StatusServiceUnavailable, "终端服务不可用")
		return nil
	}
	return s.Terminal
}

func (s *Server) listTerminals(w http.ResponseWriter, r *http.Request) {
	m := s.terminalManager(w)
	if m == nil {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": m.List()})
}

func (s *Server) openTerminal(w http.ResponseWriter, r *http.Request) {
	m := s.terminalManager(w)
	if m == nil {
		return
	}
	var body struct {
		JobID *int64 `json:"job_id"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	home, err := os.UserHomeDir()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "读取用户主目录失败")
		return
	}
	opt := terminal.OpenOptions{Workdir: home, CondaBase: s.Cfg.Runtime.CondaBase}
	if body.JobID != nil {
		job, err := s.Store.GetJob(*body.JobID)
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, http.StatusNotFound, "任务不存在")
			return
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "读取任务失败")
			return
		}
		opt.Workdir, opt.CondaEnv = job.Workdir, job.CondaEnv
		if job.CodeFrom != nil {
			if prunedFiles(w, job) {
				return
			}
			opt.Workdir = snapshot.RunCodeDir(s.Paths.Run, job.ID)
			if info, err := os.Stat(opt.Workdir); err != nil || !info.IsDir() {
				writeErr(w, http.StatusConflict, "这个任务的快照运行目录还不可用")
				return
			}
		}
	}
	if opt.CondaBase != "" {
		opt.CondaBase, err = config.ResolvePath(opt.CondaBase)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, "conda 路径无效")
			return
		}
	}
	info, err := m.Open(opt)
	if errors.Is(err, terminal.ErrLimit) {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	if errors.Is(err, terminal.ErrUnsupported) {
		writeErr(w, http.StatusNotImplemented, err.Error())
		return
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	s.Store.Audit(auth.ClientIP(r), "terminal_open", info.Workdir)
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) closeTerminal(w http.ResponseWriter, r *http.Request) {
	m := s.terminalManager(w)
	if m == nil {
		return
	}
	info, ok := m.CloseSession(r.PathValue("id"))
	if !ok {
		writeErr(w, http.StatusNotFound, "终端会话不存在")
		return
	}
	s.Store.Audit(auth.ClientIP(r), "terminal_close", info.Workdir)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

const (
	// 服务器定时 ping，发现已经断掉的连接（手机锁屏、换网络时常见），尽快让出会话
	terminalPingInterval = 20 * time.Second
	terminalPingTimeout  = 10 * time.Second
	terminalWriteTimeout = 10 * time.Second
	// 会话被另一个页面接走时的关闭码，面板据此不再自动重连
	terminalReplaced websocket.StatusCode = 4001
)

// terminalWS 把终端输出以二进制帧发给浏览器。?offset= 是浏览器已经收到的输出末尾，重连时只补发之后的部分；
// 每段输出之前，如果起点和浏览器算的不一致（首次连接或缓冲已覆盖），先发文本帧 {"type":"offset","offset":N}。
func (s *Server) terminalWS(w http.ResponseWriter, r *http.Request) {
	m := s.terminalManager(w)
	if m == nil {
		return
	}
	id := r.PathValue("id")
	if !m.Exists(id) {
		writeErr(w, http.StatusNotFound, terminal.ErrNotFound.Error())
		return
	}
	offset, _ := strconv.ParseUint(r.URL.Query().Get("offset"), 10, 64)

	// WebSocket 会长期占用连接，清除 HTTP 请求的读写期限。Origin 校验保留 coder/websocket 默认的同源策略。
	rc := http.NewResponseController(w)
	if err := rc.SetReadDeadline(time.Time{}); err != nil {
		writeErr(w, http.StatusInternalServerError, "无法建立终端连接")
		return
	}
	if err := rc.SetWriteDeadline(time.Time{}); err != nil {
		writeErr(w, http.StatusInternalServerError, "无法建立终端连接")
		return
	}
	// 握手成功后才接上会话：新连接会顶掉旧连接，握手失败的请求不能把正在用的连接挤掉
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	closeCode, closeReason := websocket.StatusNormalClosure, ""
	defer func() { _ = conn.Close(closeCode, closeReason) }()
	attachment, err := m.Attach(id, offset)
	if err != nil {
		closeReason = "session ended"
		return
	}
	defer attachment.Detach()
	conn.SetReadLimit(64 << 10)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	write := func(typ websocket.MessageType, data []byte) error {
		wctx, wcancel := context.WithTimeout(ctx, terminalWriteTimeout)
		defer wcancel()
		return conn.Write(wctx, typ, data)
	}

	go func() {
		ticker := time.NewTicker(terminalPingInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			pctx, pcancel := context.WithTimeout(ctx, terminalPingTimeout)
			err := conn.Ping(pctx)
			pcancel()
			if err != nil {
				cancel()
				return
			}
		}
	}()

	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			switch typ {
			case websocket.MessageBinary:
				if err := attachment.Write(data); err != nil {
					cancel()
					return
				}
			case websocket.MessageText:
				var msg struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" {
					_ = attachment.Resize(msg.Cols, msg.Rows)
				}
			}
		}
	}()

	cursor := attachment.Start
	if cursor != offset {
		if err := write(websocket.MessageText, offsetMessage(cursor)); err != nil {
			return
		}
	}
	for {
		data, from, err := attachment.Next(ctx, cursor)
		switch {
		case errors.Is(err, io.EOF):
			closeReason = "session ended"
			return
		case errors.Is(err, terminal.ErrReplaced):
			closeCode, closeReason = terminalReplaced, "replaced"
			return
		case err != nil:
			return
		}
		if from != cursor {
			if err := write(websocket.MessageText, offsetMessage(from)); err != nil {
				return
			}
		}
		if err := write(websocket.MessageBinary, data); err != nil {
			return
		}
		cursor = from + uint64(len(data))
	}
}

func offsetMessage(offset uint64) []byte {
	b, _ := json.Marshal(map[string]any{"type": "offset", "offset": offset})
	return b
}
