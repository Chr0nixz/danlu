package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"danlu/internal/gpu"
	"danlu/internal/scheduler"
	"danlu/internal/store"
)

// 修改排队任务、批量操作、挪动和暂缓的接口（FEATURES 3.1、3.2）：状态码、queue_pos 和操作记录
func TestQueuedJobAPI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	mon := gpu.NewMonitor(emptyProcsSource{})
	mon.Refresh(context.Background())
	s := &Server{GPUs: mon, Store: st, Mgr: scheduler.New(st, mon, stubExec{}, scheduler.Options{})}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/jobs", s.listJobs)
	mux.HandleFunc("GET /api/jobs/{id}", s.getJob)
	mux.HandleFunc("POST /api/jobs/batch", s.batchJobs)
	mux.HandleFunc("POST /api/jobs/{id}/edit", s.editJob)
	mux.HandleFunc("POST /api/jobs/{id}/move", s.moveJob)
	mux.HandleFunc("POST /api/jobs/{id}/hold", s.holdJob)
	for range 3 {
		if err := st.InsertJob(&store.Job{Name: "j", Workdir: "/w", Cmd: "python train.py --lr 1e-3", Status: store.StatusQueued}); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()

	w := call(t, mux, "POST", "/api/jobs/2/edit", `{"workdir":"`+dir+`","cmd":"python train.py --lr 3e-4","gpus":0}`)
	var job struct {
		ID       int64  `json:"id"`
		Cmd      string `json:"cmd"`
		QueuePos int    `json:"queue_pos"`
		Held     bool   `json:"held"`
		Edited   bool   `json:"edited"`
		Wait     *struct {
			Kind string `json:"kind"`
		} `json:"wait"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &job); w.Code != 200 || err != nil || job.Cmd != "python train.py --lr 3e-4" || job.QueuePos != 2 {
		t.Fatalf("修改: %d %s", w.Code, w.Body.String())
	}
	if e := lastAudit(t, st); e.Action != "job_edit" || e.Detail != "#2 j" {
		t.Fatalf("操作记录: %+v", e)
	}
	w = call(t, mux, "GET", "/api/jobs/2", "")
	if json.Unmarshal(w.Body.Bytes(), &job); !job.Edited {
		t.Fatalf("详情应标出排队期间修改过: %s", w.Body.String())
	}
	if w := call(t, mux, "POST", "/api/jobs/2/edit", `{"workdir":"/no/such","cmd":"x"}`); w.Code != 400 || !strings.Contains(w.Body.String(), `"code":"job_workdir_missing"`) {
		t.Fatalf("校验失败应 400: %d %s", w.Code, w.Body.String())
	}

	w = call(t, mux, "POST", "/api/jobs/3/move", `{"to":"top"}`)
	if json.Unmarshal(w.Body.Bytes(), &job); w.Code != 200 || job.QueuePos != 1 {
		t.Fatalf("置顶: %d %s", w.Code, w.Body.String())
	}
	if w := call(t, mux, "POST", "/api/jobs/3/move", `{"to":"left"}`); w.Code != 400 {
		t.Fatalf("不认识的移动方式应 400: %d", w.Code)
	}
	w = call(t, mux, "GET", "/api/jobs?status=queued&order=asc", "")
	var list struct {
		Jobs []struct {
			ID       int64 `json:"id"`
			QueuePos int   `json:"queue_pos"`
		} `json:"jobs"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list.Jobs) != 3 || list.Jobs[0].ID != 3 || list.Jobs[0].QueuePos != 1 || list.Jobs[2].ID != 2 {
		t.Fatalf("排队任务应按队列顺序列出: %s", w.Body.String())
	}

	if w := call(t, mux, "POST", "/api/jobs/1/hold", `{}`); w.Code != 400 {
		t.Fatalf("缺开关应 400: %d", w.Code)
	}
	w = call(t, mux, "POST", "/api/jobs/1/hold", `{"held":true}`)
	if json.Unmarshal(w.Body.Bytes(), &job); w.Code != 200 || !job.Held {
		t.Fatalf("暂缓: %d %s", w.Code, w.Body.String())
	}

	w = call(t, mux, "POST", "/api/jobs/batch", `{"ids":[1,2,3],"action":"replace","find":"1e-3","replace":"5e-4","dry_run":true}`)
	var res scheduler.BatchResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); w.Code != 200 || err != nil || len(res.Items) != 3 || !res.Items[0].Changed || res.Items[1].Changed {
		t.Fatalf("预览: %d %s", w.Code, w.Body.String())
	}
	before := lastAudit(t, st)
	if before.Action != "job_hold" {
		t.Fatalf("预览不应记操作: %+v", before)
	}
	if w := call(t, mux, "POST", "/api/jobs/batch", `{"ids":[1,2,3],"action":"replace","find":"1e-3","replace":"5e-4"}`); w.Code != 200 {
		t.Fatalf("替换: %d %s", w.Code, w.Body.String())
	}
	if e := lastAudit(t, st); e.Action != "job_batch" || e.Detail != "replace #1,#3" {
		t.Fatalf("操作记录: %+v", e)
	}

	// 已经结束的任务：整批 409，说明是哪个
	if w := call(t, mux, "POST", "/api/jobs/batch", `{"ids":[1,3],"action":"cancel"}`); w.Code != 200 {
		t.Fatalf("批量取消: %d %s", w.Code, w.Body.String())
	}
	w = call(t, mux, "POST", "/api/jobs/batch", `{"ids":[2,3],"action":"hold"}`)
	if w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), `"code":"job_not_queued"`) || !strings.Contains(w.Body.String(), `"id":3`) {
		t.Fatalf("有任务已经结束时应 409: %d %s", w.Code, w.Body.String())
	}
	if j, _ := st.GetJob(2); j.Held {
		t.Fatal("整批放弃时 #2 不应被暂缓")
	}
	for path, want := range map[string]int{
		"/api/jobs/3/edit": 409,
		"/api/jobs/3/move": 409,
		"/api/jobs/9/move": 404,
	} {
		if w := call(t, mux, "POST", path, `{"workdir":"`+dir+`","cmd":"x","to":"top"}`); w.Code != want {
			t.Fatalf("%s → %d %s，期望 %d", path, w.Code, w.Body.String(), want)
		}
	}
	if w := call(t, mux, "POST", "/api/jobs/batch", `{"ids":[2],"action":"delete"}`); w.Code != 400 {
		t.Fatalf("不认识的批量操作应 400: %d", w.Code)
	}
}
