// Package api 是 danlu 的 HTTP 接口和前端静态文件服务。
package api

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"danlu/internal/auth"
	"danlu/internal/config"
	"danlu/internal/execx"
	"danlu/internal/gpu"
	"danlu/internal/logs"
	"danlu/internal/notify"
	"danlu/internal/qrutil"
	"danlu/internal/runner"
	"danlu/internal/scheduler"
	"danlu/internal/snapshot"
	"danlu/internal/store"
	"danlu/internal/system"
	"danlu/internal/terminal"
	"danlu/internal/tlsutil"
)

// APIVersion 是接口的版本号。接口有不兼容的改动时加一，桌面客户端据此提示哪台服务器需要升级。
const APIVersion = 1

type Server struct {
	Cfg      *config.Config
	Paths    config.Paths
	Store    *store.Store
	Mgr      *scheduler.Manager
	GPUs     *gpu.Monitor
	System   *system.Monitor
	Disks    *system.DiskMonitor
	Notify   *notify.Service
	Terminal *terminal.Manager
	Auth     *auth.Auth
	Runner   *runner.Runner
	Static   fs.FS
	Version  string

	conda condaCache
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/me", s.me)

	mux.HandleFunc("GET /api/gpus", s.gpus)
	mux.HandleFunc("GET /api/queue", s.queue)
	mux.HandleFunc("POST /api/queue/pause", s.pauseQueue)
	mux.HandleFunc("POST /api/queue/resume", s.resumeQueue)
	mux.HandleFunc("GET /api/system", s.systemMetrics)
	mux.HandleFunc("GET /api/conda-envs", s.condaEnvs)
	mux.HandleFunc("GET /api/fs/dirs", s.dirs)
	mux.HandleFunc("GET /api/fs/git", s.gitHead)
	mux.HandleFunc("GET /api/workdirs/recent", s.recentWorkdirs)

	mux.HandleFunc("GET /api/jobs", s.listJobs)
	mux.HandleFunc("POST /api/jobs", s.submitJob)
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	mux.HandleFunc("GET /api/jobs/{id}/env", s.jobEnv)
	mux.HandleFunc("POST /api/jobs/{id}/cancel", s.cancelJob)
	mux.HandleFunc("POST /api/jobs/{id}/star", s.starJob)
	mux.HandleFunc("GET /api/jobs/{id}/log", s.jobLog)
	mux.HandleFunc("GET /api/jobs/{id}/log/stream", s.jobLogStream)
	mux.HandleFunc("GET /api/jobs/{id}/log/download", s.jobLogDownload)
	mux.HandleFunc("GET /api/jobs/{id}/snapshot", s.snapshotMeta)
	mux.HandleFunc("GET /api/jobs/{id}/snapshot/file", s.snapshotFile)
	mux.HandleFunc("GET /api/jobs/{id}/snapshot/diff", s.snapshotDiff)
	mux.HandleFunc("GET /api/jobs/{id}/snapshot/download", s.snapshotDownload)

	mux.HandleFunc("GET /api/settings", s.settings)
	mux.HandleFunc("GET /api/settings/ca.crt", s.caCert)
	mux.HandleFunc("GET /api/settings/qr.svg", s.settingsQR)
	mux.HandleFunc("POST /api/settings/password", s.changePassword)
	mux.HandleFunc("POST /api/notify/test", s.notifyTest)
	mux.HandleFunc("GET /api/terminal", s.listTerminals)
	mux.HandleFunc("POST /api/terminal", s.openTerminal)
	mux.HandleFunc("GET /api/terminal/{id}/ws", s.terminalWS)
	mux.HandleFunc("POST /api/terminal/{id}/close", s.closeTerminal)
	mux.HandleFunc("GET /api/audit", s.audit)
	mux.HandleFunc("GET /api/tokens", s.listTokens)
	mux.HandleFunc("POST /api/tokens/{id}/revoke", s.revokeToken)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { writeErr(w, 404, "接口不存在") })
	mux.Handle("/", s.spa())
	return securityHeaders(s.Auth.Middleware(mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		// 点 W&B 链接等外链时不带上面板地址
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// ---------- 工具函数 ----------

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, 400, "请求格式错误: "+err.Error())
		return false
	}
	return true
}

func (s *Server) jobFromPath(w http.ResponseWriter, r *http.Request) (*store.Job, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "任务 id 无效")
		return nil, false
	}
	j, err := s.Store.GetJob(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "任务不存在")
		return nil, false
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return nil, false
	}
	return j, true
}

func attachment(w http.ResponseWriter, name, contentType string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, name))
}

// sseWriteTimeout 是日志 SSE 每次写的期限，每写一次续期一次。
const sseWriteTimeout = 30 * time.Second

// noWriteTimeout 清掉服务器的写超时：下载大日志和快照时，手机网络慢也能下完。
func noWriteTimeout(w http.ResponseWriter) {
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
}

// ---------- 登录 ----------

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct{ Username, Password string }
	if !readJSON(w, r, &body) {
		return
	}
	wait, err := s.Auth.Login(w, r, body.Username, body.Password)
	switch {
	case errors.Is(err, auth.ErrLocked):
		mins := int(wait.Minutes()) + 1
		writeErr(w, 429, fmt.Sprintf("尝试次数过多，请 %d 分钟后再试", mins))
	case errors.Is(err, auth.ErrBadCredentials):
		writeErr(w, 401, err.Error())
	case err != nil:
		writeErr(w, 500, err.Error())
	default:
		writeJSON(w, 200, map[string]any{"ok": true})
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.Auth.Logout(w, r)
	s.Store.Audit(auth.ClientIP(r), "logout", "")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) me(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	writeJSON(w, 200, map[string]any{"username": s.Auth.Username(), "version": s.Version, "api_version": APIVersion, "hostname": host})
}

// ---------- GPU ----------

type jobRef struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type gpuView struct {
	gpu.GPU
	Job      *jobRef    `json:"job"`
	External []gpu.Proc `json:"external"`
}

func (s *Server) gpus(w http.ResponseWriter, r *http.Request) {
	gpus, at, err := s.GPUs.Latest()
	running, _ := s.Store.JobsByStatus(store.StatusRunning)
	owner := map[int]*jobRef{}
	assigned := map[int64][]int{}
	for _, j := range running {
		assigned[j.ID] = j.GPUsAssigned
		for _, g := range j.GPUsAssigned {
			owner[g] = &jobRef{ID: j.ID, Name: j.Name}
		}
	}
	views := make([]gpuView, 0, len(gpus))
	for _, g := range gpus {
		v := gpuView{GPU: g, Job: owner[g.Index], External: []gpu.Proc{}}
		for _, p := range g.Procs {
			if own, ok := assigned[p.JobID]; !ok || !slices.Contains(own, g.Index) {
				v.External = append(v.External, p)
			}
		}
		views = append(views, v)
	}
	resp := map[string]any{"gpus": views, "updated_at": at.Unix()}
	if err != nil {
		resp["error"] = err.Error()
		if since := s.GPUs.ErrorSince(); !since.IsZero() {
			resp["error_since"] = since.Unix()
		}
	}
	writeJSON(w, 200, resp)
}

func (s *Server) systemMetrics(w http.ResponseWriter, r *http.Request) {
	samples, at, err := s.System.Latest()
	resp := map[string]any{"samples": samples, "updated_at": at.Unix()}
	if s.Disks != nil {
		disks, diskAt, diskErr := s.Disks.Latest()
		if disks == nil {
			disks = []system.Disk{}
		}
		resp["disks"], resp["disks_updated_at"] = disks, diskAt.Unix()
		if diskErr != nil {
			resp["disks_error"] = diskErr.Error()
		}
	}
	if err != nil {
		resp["error"] = err.Error()
	}
	writeJSON(w, 200, resp)
}

func (s *Server) queue(w http.ResponseWriter, r *http.Request) {
	state, err := s.Mgr.QueueState()
	if err != nil {
		writeErr(w, 500, "读取队列状态失败: "+err.Error())
		return
	}
	writeJSON(w, 200, state)
}

func (s *Server) pauseQueue(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !readJSON(w, r, &body) {
		return
	}
	state, changed, err := s.Mgr.PauseQueue("manual", "手动暂停", nil)
	if err != nil {
		writeErr(w, 500, "暂停队列失败: "+err.Error())
		return
	}
	if changed {
		s.Store.Audit(auth.ClientIP(r), "queue_pause", state.Reason)
	}
	writeJSON(w, 200, state)
}

func (s *Server) resumeQueue(w http.ResponseWriter, r *http.Request) {
	var body struct{}
	if !readJSON(w, r, &body) {
		return
	}
	state, changed, err := s.Mgr.ResumeQueue()
	if err != nil {
		if scheduler.IsValidation(err) {
			writeErr(w, 409, err.Error())
			return
		}
		writeErr(w, 500, "恢复队列失败: "+err.Error())
		return
	}
	if changed {
		s.Store.Audit(auth.ClientIP(r), "queue_resume", "")
	}
	writeJSON(w, 200, state)
}

// ---------- conda 环境 ----------

type condaEnv struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type condaCache struct {
	mu   sync.Mutex
	at   time.Time
	envs []condaEnv
	err  string
}

func (s *Server) condaEnvs(w http.ResponseWriter, r *http.Request) {
	base := s.Cfg.Runtime.CondaBase
	if base == "" {
		writeJSON(w, 200, map[string]any{"available": false, "envs": []condaEnv{}, "error": "没有配置 conda（config.toml 的 runtime.conda_base）"})
		return
	}
	c := &s.conda
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.at) > 5*time.Minute || r.URL.Query().Get("refresh") == "1" {
		c.envs, c.err = listCondaEnvs(base)
		c.at = time.Now()
	}
	writeJSON(w, 200, map[string]any{"available": true, "base": base, "envs": c.envs, "error": c.err})
}

func listCondaEnvs(base string) ([]condaEnv, string) {
	bin := filepath.Join(base, "bin", "conda")
	if _, err := os.Stat(bin); err != nil {
		bin = filepath.Join(base, "condabin", "conda")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, err := execx.Output(execx.Command(ctx, bin, "env", "list", "--json"))
	if err != nil {
		return []condaEnv{}, "执行 conda env list 失败: " + err.Error()
	}
	var res struct{ Envs []string }
	if err := json.Unmarshal(out, &res); err != nil {
		return []condaEnv{}, "解析 conda 输出失败"
	}
	envs := []condaEnv{}
	for _, p := range res.Envs {
		name := p
		switch {
		case filepath.Clean(p) == filepath.Clean(base):
			name = "base"
		case filepath.Dir(p) == filepath.Join(base, "envs"):
			name = filepath.Base(p)
		}
		envs = append(envs, condaEnv{Name: name, Path: p})
	}
	return envs, ""
}

// ---------- 目录补全、git 状态、最近目录 ----------

func (s *Server) dirs(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" || !strings.HasPrefix(p, "/") {
		home, _ := os.UserHomeDir()
		p = home + "/"
	}
	dir, prefix := p, ""
	if !strings.HasSuffix(p, "/") {
		dir, prefix = path.Dir(p), path.Base(p)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		writeJSON(w, 200, map[string]any{"dirs": []string{}})
		return
	}
	out := []string{}
	for _, e := range ents {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || (strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".")) {
			continue
		}
		full := path.Join(dir, name)
		if fi, err := os.Stat(full); err != nil || !fi.IsDir() {
			continue
		}
		out = append(out, full)
		if len(out) >= 50 {
			break
		}
	}
	writeJSON(w, 200, map[string]any{"dirs": out})
}

func (s *Server) gitHead(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	g, ok := snapshot.GitHead(ctx, r.URL.Query().Get("path"))
	if !ok {
		writeJSON(w, 200, map[string]any{"is_git": false})
		return
	}
	writeJSON(w, 200, map[string]any{"is_git": true, "commit": g.Commit, "branch": g.Branch, "dirty": g.Dirty})
}

func (s *Server) recentWorkdirs(w http.ResponseWriter, r *http.Request) {
	dirs, err := s.Store.RecentWorkdirs(10)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"dirs": dirs})
}

// ---------- 任务 ----------

type jobView struct {
	*store.Job
	LastLine        string `json:"last_line,omitempty"`
	ErrorLine       string `json:"error_line,omitempty"`
	TracebackOffset *int64 `json:"traceback_offset,omitempty"`
	QueuePos        int    `json:"queue_pos,omitempty"`
	WaitReason      string `json:"wait_reason,omitempty"`
}

func (s *Server) views(jobs []*store.Job) []jobView {
	pos := map[int64]int{}
	var waitReasons map[int64]string
	if s.Mgr != nil {
		waitReasons = s.Mgr.WaitReasons()
	}
	if slices.ContainsFunc(jobs, func(j *store.Job) bool { return j.Status == store.StatusQueued }) {
		queued, _ := s.Store.JobsByStatus(store.StatusQueued)
		for i, q := range queued {
			pos[q.ID] = i + 1
		}
	}
	out := make([]jobView, len(jobs))
	for i, j := range jobs {
		v := jobView{Job: j, QueuePos: pos[j.ID], TracebackOffset: j.TracebackOffset, WaitReason: waitReasons[j.ID]}
		// 结束的任务也显示最后一行，通常是最终的指标
		if j.StartedAt != nil {
			v.LastLine = logs.LastLine(s.Runner.LogPath(j.ID))
		}
		// 失败的任务再找一条报错行：最后一行常常是 torchrun 的汇总或空白分隔线，不是原因
		if j.Status == store.StatusFailed && j.StartedAt != nil {
			v.ErrorLine = logs.ErrorLine(s.Runner.LogPath(j.ID))
		}
		out[i] = v
	}
	return out
}

func (s *Server) listJobs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := store.JobFilter{Query: strings.TrimSpace(q.Get("q")), Starred: q.Get("starred") == "true" || q.Get("starred") == "1"}
	if st := q.Get("status"); st != "" {
		f.Statuses = strings.Split(st, ",")
	}
	f.EndedAfter, _ = strconv.ParseInt(q.Get("ended_after"), 10, 64)
	// order=ended：只要已结束的，按结束时间倒序，用 before / before_id 游标翻页
	f.ByEnded = q.Get("order") == "ended"
	f.Before, _ = strconv.ParseInt(q.Get("before"), 10, 64)
	f.BeforeID, _ = strconv.ParseInt(q.Get("before_id"), 10, 64)
	size, _ := strconv.Atoi(q.Get("size"))
	if size <= 0 || size > 200 {
		size = 20
	}
	page, _ := strconv.Atoi(q.Get("page"))
	page = max(page, 1)
	f.Limit, f.Offset = size, (page-1)*size
	jobs, total, err := s.Store.ListJobs(f)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": s.views(jobs), "total": total, "page": page, "size": size})
}

func (s *Server) submitJob(w http.ResponseWriter, r *http.Request) {
	var req scheduler.SubmitReq
	if !readJSON(w, r, &req) {
		return
	}
	j, err := s.Mgr.Submit(req)
	if scheduler.IsValidation(err) {
		writeErr(w, 400, err.Error())
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.Store.Audit(auth.ClientIP(r), "submit", fmt.Sprintf("#%d %s", j.ID, j.Name))
	writeJSON(w, 201, s.views([]*store.Job{j})[0])
}

func (s *Server) getJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, s.views([]*store.Job{j})[0])
}

func (s *Server) jobEnv(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	if j.Recipe == nil || len(j.Recipe.EnvHash) != 64 {
		writeErr(w, 404, "这个任务没有环境包记录")
		return
	}
	if _, err := hex.DecodeString(j.Recipe.EnvHash); err != nil {
		writeErr(w, 500, "任务的环境包哈希无效")
		return
	}
	f, err := os.Open(filepath.Join(s.Paths.Envs, j.Recipe.EnvHash+".txt"))
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, 404, "环境包文件不存在")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer f.Close()
	noWriteTimeout(w)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="env-%d.txt"`, j.ID))
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = io.Copy(w, f)
}

func (s *Server) cancelJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	j, err := s.Mgr.Cancel(j.ID)
	if scheduler.IsValidation(err) {
		writeErr(w, 400, err.Error())
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.Store.Audit(auth.ClientIP(r), "cancel", fmt.Sprintf("#%d %s", j.ID, j.Name))
	writeJSON(w, 200, s.views([]*store.Job{j})[0])
}

// ---------- 日志 ----------

func (s *Server) jobLog(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	if prunedFiles(w, j) {
		return
	}
	q := r.URL.Query()
	p := s.Runner.LogPath(j.ID)
	var (
		data        []byte
		start, next int64
		size        int64
		err         error
	)
	if t, _ := strconv.ParseInt(q.Get("tail"), 10, 64); t > 0 {
		data, start, size, err = logs.Tail(p, min(t, 4<<20))
		next = start + int64(len(data))
	} else {
		off, _ := strconv.ParseInt(q.Get("offset"), 10, 64)
		limit, _ := strconv.ParseInt(q.Get("limit"), 10, 64)
		if limit <= 0 || limit > 4<<20 {
			limit = 256 << 10
		}
		data, next, size, err = logs.ReadRange(p, off, limit)
		start = next - int64(len(data))
	}
	if errors.Is(err, os.ErrNotExist) {
		writeJSON(w, 200, map[string]any{"data": "", "start": 0, "next": 0, "size": 0})
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"data": string(data), "start": start, "next": next, "size": size})
}

// jobLogStream 用 SSE 推送日志增量，任务结束且读完后发送 end 事件。
func (s *Server) jobLogStream(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	if prunedFiles(w, j) {
		return
	}
	rc := http.NewResponseController(w)
	off, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(200)
	// 服务器的 WriteTimeout 管整个响应，长连接要每次写之前续期：
	// 客户端不读数据时，这个处理函数最多卡 sseWriteTimeout 就退出
	send := func(format string, a ...any) bool {
		_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
		if _, err := fmt.Fprintf(w, format, a...); err != nil {
			return false
		}
		return rc.Flush() == nil
	}
	_ = rc.SetWriteDeadline(time.Now().Add(sseWriteTimeout))
	if rc.Flush() != nil {
		return
	}

	p := s.Runner.LogPath(j.ID)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	lastBeat := time.Now()
	for {
		data, next, size, rerr := logs.ReadRange(p, off, 64<<10)
		if rerr == nil && size < off {
			off = 0 // 日志被截断过，从头开始
			continue
		}
		if len(data) > 0 {
			// o: 这段内容的起始字节偏移，n: 下一段的起始偏移（前端据此去重和续传）
			b, _ := json.Marshal(map[string]any{"o": next - int64(len(data)), "n": next, "t": string(data)})
			if !send("data: %s\n\n", b) {
				return
			}
			off = next
			if next < size {
				continue
			}
		} else if cur, err := s.Store.GetJob(j.ID); err == nil && cur.Finished() && (rerr != nil || off >= size) {
			send("event: end\ndata: {\"status\":%q}\n\n", cur.Status)
			return
		}
		if time.Since(lastBeat) > 15*time.Second {
			if !send(": ping\n\n") {
				return
			}
			lastBeat = time.Now()
		}
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
		}
	}
}

func (s *Server) jobLogDownload(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	if prunedFiles(w, j) {
		return
	}
	f, err := os.Open(s.Runner.LogPath(j.ID))
	if err != nil {
		writeErr(w, 404, "还没有日志")
		return
	}
	defer f.Close()
	attachment(w, fmt.Sprintf("danlu-%d.log", j.ID), "text/plain; charset=utf-8")
	noWriteTimeout(w)
	io.Copy(w, f)
}

// ---------- 代码快照 ----------

func (s *Server) snapshotMeta(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	if prunedFiles(w, j) {
		return
	}
	m, err := snapshot.LoadMeta(s.Paths.Snapshots, j.ID)
	if err != nil {
		msg := "这个任务还没有代码快照（任务启动时才生成）"
		if j.SnapshotNote != "" {
			msg = j.SnapshotNote
		}
		writeErr(w, 404, msg)
		return
	}
	diff, _ := snapshot.Diff(s.Paths.Snapshots, j.ID)
	writeJSON(w, 200, map[string]any{"meta": m, "has_diff": len(diff) > 0, "note": j.SnapshotNote})
}

func (s *Server) snapshotFile(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	if prunedFiles(w, j) {
		return
	}
	fc, err := snapshot.ReadFile(s.Paths.Snapshots, j.ID, r.URL.Query().Get("path"))
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, 404, "快照里没有这个文件")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, fc)
}

func (s *Server) snapshotDiff(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	if prunedFiles(w, j) {
		return
	}
	diff, err := snapshot.Diff(s.Paths.Snapshots, j.ID)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"diff": string(diff)})
}

func (s *Server) snapshotDownload(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	if prunedFiles(w, j) {
		return
	}
	f, err := os.Open(snapshot.ArchivePath(s.Paths.Snapshots, j.ID))
	if err != nil {
		writeErr(w, 404, "没有代码快照")
		return
	}
	defer f.Close()
	attachment(w, fmt.Sprintf("danlu-%d-code.tar.gz", j.ID), "application/gzip")
	noWriteTimeout(w)
	io.Copy(w, f)
}

// ---------- 设置 ----------

func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"username": s.Auth.Username(), "version": s.Version, "tls_mode": s.Cfg.TLS.Mode,
		"public_ip": s.Cfg.Server.PublicIP, "port": s.Cfg.Server.Port, "data_dir": s.Paths.Home,
		"conda_base": s.Cfg.Runtime.CondaBase, "ca_available": false,
		"terminal_enabled": s.Cfg.Terminal.Enabled,
	}
	if s.Cfg.TLS.Mode == config.TLSLocalCA {
		if fp, err := tlsutil.Fingerprint(tlsutil.FilesIn(s.Paths.TLS).CACert); err == nil {
			resp["ca_available"], resp["ca_fingerprint"] = true, fp
		}
	}
	if s.Notify != nil {
		status, err := s.Notify.Status()
		if err != nil {
			writeErr(w, 500, "读取推送状态失败")
			return
		}
		resp["notify"] = status
	}
	writeJSON(w, 200, resp)
}

func (s *Server) caCert(w http.ResponseWriter, r *http.Request) {
	b, err := os.ReadFile(tlsutil.FilesIn(s.Paths.TLS).CACert)
	if err != nil || s.Cfg.TLS.Mode != config.TLSLocalCA {
		writeErr(w, 404, "当前没有使用本地 CA")
		return
	}
	attachment(w, "danlu-ca.crt", "application/x-x509-ca-cert")
	w.Write(b)
}

func (s *Server) settingsQR(w http.ResponseWriter, r *http.Request) {
	svg, err := qrutil.SVG(s.Cfg.URL())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "生成接入二维码失败")
		return
	}
	w.Header().Set("Content-Type", "image/svg+xml; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(svg)
}

func (s *Server) changePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if !s.Auth.CheckPassword(body.Old) {
		writeErr(w, 400, "当前密码不正确")
		return
	}
	if len(body.New) < 8 {
		writeErr(w, 400, "新密码至少 8 位")
		return
	}
	// 只写数据库，不碰 config.toml：配置文件只有命令行会写
	if err := auth.SetPassword(s.Store, body.New); err != nil {
		writeErr(w, 500, "保存密码失败: "+err.Error())
		return
	}
	_ = s.Store.DeleteSessionsExcept(s.Auth.CurrentSessionHash(r))
	s.Store.Audit(auth.ClientIP(r), "password_change", "其他会话已注销")
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	page = max(page, 1)
	entries, err := s.Store.ListAudit(50, (page-1)*50)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"entries": entries})
}

// ---------- 设备令牌 ----------

// 令牌只能在服务器上用 danlu token create 创建：能 SSH 登录才说明是本人。面板这里只能查看和吊销。
func (s *Server) listTokens(w http.ResponseWriter, r *http.Request) {
	toks, err := s.Store.ListDeviceTokens()
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"tokens": toks})
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "令牌 id 无效")
		return
	}
	name, err := s.Store.RevokeDeviceToken(id)
	if errors.Is(err, store.ErrNotFound) {
		writeErr(w, 404, "令牌不存在")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.Store.Audit(auth.ClientIP(r), "token_revoke", fmt.Sprintf("#%d %s", id, name))
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---------- 前端 ----------

func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.Static)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if p != "" && p != "index.html" {
			if fi, err := fs.Stat(s.Static, p); err == nil && !fi.IsDir() {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		// 前端路由（/jobs/3 等）都返回 index.html
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, s.Static, "index.html")
	})
}
