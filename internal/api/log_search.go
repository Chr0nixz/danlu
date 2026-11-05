package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"danlu/internal/logs"
)

// 卡住的文件读取最多占两个搜索槽，HTTP 请求仍按时返回已有结果。
var logSearchSlots = make(chan struct{}, 2)

func (s *Server) jobLogSearch(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobFromPath(w, r)
	if !ok || prunedFiles(w, job) {
		return
	}
	q := r.URL.Query()
	word := strings.TrimSpace(q.Get("q"))
	if word == "" || utf8.RuneCountInString(word) > 256 || strings.ContainsAny(word, "\r\n") {
		writeErr(w, 400, "请输入 1–256 个字的单行关键词")
		return
	}
	limit := 200
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil || n <= 0 {
			writeErr(w, 400, "日志搜索条数必须大于 0")
			return
		}
		limit = min(limit, n)
	}
	select {
	case logSearchSlots <- struct{}{}:
	default:
		writeErr(w, http.StatusTooManyRequests, "日志搜索正在进行，请稍后重试")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	type outcome struct {
		result logs.SearchResult
		err    error
	}
	done := make(chan outcome, 1)
	var progress atomic.Pointer[logs.SearchResult]
	progress.Store(&logs.SearchResult{Matches: []logs.SearchMatch{}})
	path := s.Runner.LogPath(job.ID)
	go func() {
		defer func() { <-logSearchSlots }()
		result, err := logs.Search(ctx, path, word, limit, 1<<30, job.Finished(), func(partial logs.SearchResult) {
			progress.Store(&partial)
		})
		done <- outcome{result, err}
	}()
	w.Header().Set("Cache-Control", "private, no-store")
	select {
	case finished := <-done:
		if finished.err != nil {
			writeErr(w, 500, finished.err.Error())
			return
		}
		writeJSON(w, 200, finished.result)
	case <-ctx.Done():
		if r.Context().Err() != nil {
			return
		}
		partial := *progress.Load()
		partial.Truncated = true
		writeJSON(w, 200, partial)
	}
}
