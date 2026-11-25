package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"danlu/internal/gpu"
	"danlu/internal/store"
)

// 故意给出 nil 和空的进程列表：两种情况编码出来都必须是数组，不能是 null
type emptyProcsSource struct{}

func (emptyProcsSource) Query(context.Context) ([]gpu.GPU, error) {
	return []gpu.GPU{
		{Index: 0, UUID: "GPU-x", Name: "Fake GPU", MemTotalMB: 24576},
		{Index: 1, UUID: "GPU-y", Name: "Fake GPU", MemTotalMB: 24576, Procs: []gpu.Proc{}},
	}, nil
}

// procs 和 external 约定是数组。总览页在"卡分给了任务、但卡上还没有进程"时按数组读 procs，
// 遇到 null 直接崩（Cannot read properties of null (reading 'some')），所以空也必须是 []。
func TestGpusProcsAlwaysArrays(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// 运行中任务占着 0 号卡：前端这时才会去读 procs，正是崩溃场景。
	// gpus_assigned 是运行期字段，InsertJob 不写，要 SaveJob 落库
	j := &store.Job{Name: "train", Workdir: home, Cmd: "sleep 1", Status: store.StatusRunning, GPUsAssigned: []int{0}}
	if err := st.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	mon := gpu.NewMonitor(emptyProcsSource{})
	mon.Refresh(context.Background())
	rec := httptest.NewRecorder()
	(&Server{GPUs: mon, Store: st}).gpus(rec, httptest.NewRequest(http.MethodGet, "/api/gpus", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		GPUs []map[string]any `json:"gpus"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.GPUs) != 2 {
		t.Fatalf("应有 2 张卡: %s", rec.Body.String())
	}
	for _, g := range body.GPUs {
		// null 解出来是 nil，断言不成 []any；空数组才是合法的 []any
		if _, ok := g["procs"].([]any); !ok {
			t.Fatalf("procs 应是数组（空也要是 []）: %s", rec.Body.String())
		}
		if _, ok := g["external"].([]any); !ok {
			t.Fatalf("external 应是数组（空也要是 []）: %s", rec.Body.String())
		}
	}
	if body.GPUs[0]["job"] == nil {
		t.Fatalf("0 号卡分给了运行中任务，job 不应为空: %s", rec.Body.String())
	}
}

type unknownOwnerSource struct{}

func (unknownOwnerSource) Query(context.Context) ([]gpu.GPU, error) {
	return []gpu.GPU{
		{Index: 0, Procs: []gpu.Proc{{PID: 11, OwnerUnknown: true}}},
		{Index: 1, Procs: []gpu.Proc{{PID: 22, OwnerUnknown: true}}},
	}, nil
}

// 认不出归属的进程：在任务占着的卡上多半是它正在退出的 rank，不列成外部进程（客户端据此通知）；
// 在空卡上照样列出，面板才知道这张卡为什么不能分配
func TestGpusOwnerUnknown(t *testing.T) {
	home := t.TempDir()
	st, err := store.Open(filepath.Join(home, "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	j := &store.Job{Name: "train", Workdir: home, Cmd: "sleep 1", Status: store.StatusRunning, GPUsAssigned: []int{0}}
	if err := st.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	if err := st.SaveJob(j); err != nil {
		t.Fatal(err)
	}
	mon := gpu.NewMonitor(unknownOwnerSource{})
	mon.Refresh(context.Background())
	rec := httptest.NewRecorder()
	(&Server{GPUs: mon, Store: st}).gpus(rec, httptest.NewRequest(http.MethodGet, "/api/gpus", nil))
	var body struct {
		GPUs []struct {
			External []gpu.Proc `json:"external"`
			Procs    []gpu.Proc `json:"procs"`
		} `json:"gpus"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.GPUs) != 2 {
		t.Fatalf("%v %s", err, rec.Body.String())
	}
	if len(body.GPUs[0].External) != 0 || len(body.GPUs[0].Procs) != 1 || !body.GPUs[0].Procs[0].OwnerUnknown {
		t.Fatalf("任务卡上的归属不明进程不应列成外部进程: %s", rec.Body.String())
	}
	if len(body.GPUs[1].External) != 1 || !body.GPUs[1].External[0].OwnerUnknown {
		t.Fatalf("空卡上的归属不明进程应列出: %s", rec.Body.String())
	}
}
