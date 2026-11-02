package api

import (
	"errors"
	"fmt"
	"net/http"

	"danlu/internal/auth"
	"danlu/internal/store"
)

func (s *Server) starJob(w http.ResponseWriter, r *http.Request) {
	j, ok := s.jobFromPath(w, r)
	if !ok {
		return
	}
	var body struct {
		Starred *bool `json:"starred"`
	}
	if !readJSON(w, r, &body) {
		return
	}
	if body.Starred == nil {
		writeErr(w, 400, "缺少 starred（true 或 false）")
		return
	}
	updated, err := s.Store.SetStarred(j.ID, *body.Starred)
	if errors.Is(err, store.ErrPruning) {
		writeErr(w, 409, err.Error())
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s.Store.Audit(auth.ClientIP(r), "job_star", fmt.Sprintf("#%d starred=%t", j.ID, *body.Starred))
	writeJSON(w, 200, s.views([]*store.Job{updated})[0])
}

func prunedFiles(w http.ResponseWriter, j *store.Job) bool {
	if j.PrunedAt != nil {
		writeErr(w, 410, "日志和代码快照已按保留策略清理；任务记录、环境信息和训练产物仍保留")
		return true
	}
	if j.Pruning {
		writeErr(w, 409, "任务文件正在按保留策略清理，请稍后再试")
		return true
	}
	return false
}
