package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"danlu/internal/api"
	"danlu/internal/auth"
	"danlu/internal/config"
	"danlu/internal/gpu"
	"danlu/internal/localsock"
	"danlu/internal/runner"
	"danlu/internal/scheduler"
	"danlu/internal/store"
)

// 任务命令的端到端测试：真的 /api handler 挂在临时数据目录的 unix socket 上，命令在进程内执行。
// 对端 uid 由测试直接放进连接的 context（macOS 上取不到 SO_PEERCRED），Linux 上的取法由 localsock 的测试覆盖。

type twoGPUs struct{}

func (twoGPUs) Query(context.Context) ([]gpu.GPU, error) {
	return []gpu.GPU{
		{Index: 0, Name: "Fake", MemTotalMB: 81920, Procs: []gpu.Proc{}},
		{Index: 1, Name: "Fake", MemTotalMB: 81920, Procs: []gpu.Proc{}},
	}, nil
}

type localEnv struct {
	home string
	st   *store.Store
	run  *runner.Runner
}

func startLocal(t *testing.T, uid int) *localEnv {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Windows 上没有 euid")
	}
	jsonErrors = false
	// macOS 的 unix socket 路径上限是 104 字节，t.TempDir() 太长
	home, err := os.MkdirTemp("", "dl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	t.Setenv("DANLU_HOME", home)
	p := config.PathsFor(home)
	if err := p.MkdirAll(); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(p.DB)
	if err != nil {
		t.Fatal(err)
	}
	mon := gpu.NewMonitor(twoGPUs{})
	mon.Refresh(context.Background())
	run := &runner.Runner{RunRoot: p.Run, LogRoot: p.Logs, EnvRoot: p.Envs}
	au := auth.New(st, auth.Options{Entrance: "abcdef", Secret: []byte("test-secret"), Username: "admin",
		SessionTTL: time.Hour, MaxFailures: 5, Lockout: time.Minute})
	srv := &api.Server{Cfg: config.Default(), Paths: p, Store: st, GPUs: mon, Auth: au, Runner: run,
		Mgr: scheduler.New(st, mon, run, scheduler.Options{})}
	ln, err := net.Listen("unix", localsock.Path(home))
	if err != nil {
		t.Fatal(err)
	}
	hs := &http.Server{Handler: srv.LocalHandler(),
		ConnContext: func(ctx context.Context, _ net.Conn) context.Context { return auth.WithLocalPeer(ctx, uid) }}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close(); st.Close() })
	return &localEnv{home: home, st: st, run: run}
}

// run 在进程内执行一条命令，返回 stdout 和错误（退出码由 exitOf 取）。
func run(t *testing.T, fn func([]string) error, args ...string) (string, error) {
	t.Helper()
	jsonErrors = false
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stdout
	os.Stdout = w
	out := make(chan string)
	go func() { b, _ := io.ReadAll(r); out <- string(b) }()
	err = fn(args)
	w.Close()
	os.Stdout = old
	return <-out, err
}

func exitOf(err error) (int, string) {
	if err == nil {
		return 0, ""
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return ce.exit, ce.code
	}
	return exitErr, ""
}

func mustJSON(t *testing.T, s string, v any) {
	t.Helper()
	if err := json.Unmarshal([]byte(s), v); err != nil {
		t.Fatalf("输出不是 JSON: %v\n%s", err, s)
	}
}

func TestSubmitShowJobsCancelWait(t *testing.T) {
	startLocal(t, os.Geteuid())
	dir := t.TempDir()

	out, err := run(t, cmdSubmit, "--json", "-C", dir, "-g", "0", "--key", "k1", "--var", "A=1",
		"--", "python", "train.py", "--lr", "3e-4", "a b")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	var j struct {
		ID        int64             `json:"id"`
		Cmd       string            `json:"cmd"`
		Workdir   string            `json:"workdir"`
		GPUsReq   int               `json:"gpus_req"`
		SubmitKey string            `json:"submit_key"`
		Env       map[string]string `json:"env"`
		Status    string            `json:"status"`
	}
	mustJSON(t, out, &j)
	if j.Cmd != "python train.py --lr 3e-4 'a b'" || j.GPUsReq != 0 || j.SubmitKey != "k1" || j.Env["A"] != "1" || j.Workdir != dir {
		t.Fatalf("提交的任务不对: %+v", j)
	}
	// 同一个 key 再提交：不新建
	if out, err := run(t, cmdSubmit, "-C", dir, "-g", "0", "--key", "k1", "--var", "A=1", "--", "python", "train.py", "--lr", "3e-4", "a b"); err != nil || !strings.Contains(out, "已存在 #1") {
		t.Fatalf("重复提交: %q %v", out, err)
	}
	// 同一个 key、不同命令或不同环境变量：409 冲突
	for _, args := range [][]string{
		{"-C", dir, "-g", "0", "--key", "k1", "--var", "A=1", "--", "other"},
		{"-C", dir, "-g", "0", "--key", "k1", "--var", "A=2", "--", "python", "train.py", "--lr", "3e-4", "a b"},
	} {
		if _, err := run(t, cmdSubmit, args...); func() bool { _, c := exitOf(err); return c != "job_submit_key_conflict" }() {
			t.Fatalf("%v 应冲突: %v", args, err)
		}
	}
	// 不写 -g 按面板偏好（默认 1 张）；单个参数原样作为脚本
	out, err = run(t, cmdSubmit, "--json", "-C", dir, "--", "echo a && echo b")
	if err != nil {
		t.Fatal(err)
	}
	mustJSON(t, out, &j)
	if j.ID != 2 || j.GPUsReq != 1 || j.Cmd != "echo a && echo b" {
		t.Fatalf("第二个任务: %+v", j)
	}

	out, err = run(t, cmdJobs, "--json")
	var list struct {
		Total int `json:"total"`
	}
	if mustJSON(t, out, &list); err != nil || list.Total != 2 {
		t.Fatalf("jobs: %v %s", err, out)
	}
	if out, _ := run(t, cmdJobs); !strings.Contains(out, "#2") || !strings.Contains(out, "queued") {
		t.Fatalf("jobs 文字输出: %q", out)
	}
	if out, err := run(t, cmdShow, "1"); err != nil || !strings.Contains(out, "工作目录") || !strings.Contains(out, "A=1") {
		t.Fatalf("show: %v %q", err, out)
	}
	if _, err := run(t, cmdShow, "99", "--json"); func() bool { e, c := exitOf(err); return e != exitNotFound || c != "job_not_found" }() {
		t.Fatalf("不存在的任务应退出码 4: %v", err)
	}
	if !jsonErrors {
		t.Fatal("带 --json 时错误应按 JSON 输出")
	}

	// 排队中的任务等不到结束：超时 124，文字里说明还没结束
	out, err = run(t, cmdWait, "1", "--timeout", "50ms")
	if e, _ := exitOf(err); e != exitTimeout || !strings.Contains(out, "还没结束") {
		t.Fatalf("wait 超时: %d %q", e, out)
	}
	if out, err := run(t, cmdCancel, "1", "2", "99"); !strings.Contains(out, "已取消 #1") || !strings.Contains(out, "已取消 #2") {
		t.Fatalf("cancel: %q", out)
	} else if e, _ := exitOf(err); e != exitNotFound {
		t.Fatalf("取消不存在的任务应退出码 4，得到 %d", e)
	}
	out, err = run(t, cmdWait, "--json", "1")
	var waited struct {
		Jobs []struct {
			Status string `json:"status"`
		} `json:"jobs"`
	}
	mustJSON(t, out, &waited)
	if e, _ := exitOf(err); e != exitJobCancelled || len(waited.Jobs) != 1 || waited.Jobs[0].Status != "cancelled" {
		t.Fatalf("wait 取消的任务: %d %s", e, out)
	}
}

// 失败优先于取消；全部成功是 0
func TestWaitOutcomes(t *testing.T) {
	env := startLocal(t, os.Geteuid())
	dir := t.TempDir()
	finish := func(status string) int64 {
		out, err := run(t, cmdSubmit, "--json", "-C", dir, "-g", "0", "--", "true")
		if err != nil {
			t.Fatal(err)
		}
		var j struct {
			ID int64 `json:"id"`
		}
		mustJSON(t, out, &j)
		job, err := env.st.GetJob(j.ID)
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().Unix()
		job.Status, job.StartedAt, job.EndedAt = status, &now, &now
		if err := env.st.SaveJob(job); err != nil {
			t.Fatal(err)
		}
		return j.ID
	}
	ok, failed, cancelled := finish(store.StatusSucceeded), finish(store.StatusFailed), finish(store.StatusCancelled)
	for _, c := range []struct {
		ids  []int64
		want int
	}{{[]int64{ok}, 0}, {[]int64{ok, cancelled}, exitJobCancelled}, {[]int64{cancelled, failed, ok}, exitJobFailed}} {
		var args []string
		for _, id := range c.ids {
			args = append(args, itoa(id))
		}
		_, err := run(t, cmdWait, args...)
		if e, _ := exitOf(err); e != c.want {
			t.Fatalf("wait %v: 退出码 %d，应为 %d", c.ids, e, c.want)
		}
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func TestTail(t *testing.T) {
	env := startLocal(t, os.Geteuid())
	out, err := run(t, cmdSubmit, "--json", "-C", t.TempDir(), "-g", "0", "--", "true")
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, out, &j)
	log := "line1\n\x1b[31mred\x1b[0m\nstep 1/3\rstep 2/3\rstep 3/3\nlast\n"
	if err := os.WriteFile(env.run.LogPath(j.ID), []byte(log), 0o600); err != nil {
		t.Fatal(err)
	}
	id := itoa(j.ID)
	if out, err := run(t, cmdTail, id, "-n", "3"); err != nil || out != "red\nstep 3/3\nlast\n" {
		t.Fatalf("tail 应折叠进度条、去掉颜色: %q %v", out, err)
	}
	if out, _ := run(t, cmdTail, id, "--raw", "-n", "2"); out != "step 1/3\rstep 2/3\rstep 3/3\nlast\n" {
		t.Fatalf("tail --raw 应原样输出: %q", out)
	}
	// 任务结束后 -f：读完剩下的就退出，退出码同 wait
	if _, err := run(t, cmdCancel, id); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.run.LogPath(j.ID), []byte(log+"more\r 50%\r100%\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		out, err = run(t, cmdTail, id, "-n", "1", "-f")
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("tail -f 在任务结束后没有退出")
	}
	if e, _ := exitOf(err); e != exitJobCancelled || out != "100%\n" {
		t.Fatalf("tail -f: %d %q", e, out)
	}
}

func TestGpusAndAPI(t *testing.T) {
	startLocal(t, os.Geteuid())
	out, err := run(t, cmdGpus)
	if err != nil || !strings.Contains(out, "GPU 1") || !strings.Contains(out, "空闲") || !strings.Contains(out, "排队 0 个") {
		t.Fatalf("gpus: %v %q", err, out)
	}
	out, err = run(t, cmdGpus, "--json")
	var g struct {
		GPUs struct {
			GPUs []any `json:"gpus"`
		} `json:"gpus"`
		Queued    int `json:"queued"`
		QueueHead any `json:"queue_head"`
	}
	if mustJSON(t, out, &g); err != nil || len(g.GPUs.GPUs) != 2 || g.QueueHead != nil {
		t.Fatalf("gpus --json: %v %s", err, out)
	}
	if out, err := run(t, cmdAPI, "GET", "/api/me"); err != nil || !strings.Contains(out, `"api_version"`) {
		t.Fatalf("api GET: %v %q", err, out)
	}
	if out, err := run(t, cmdAPI, "get", "/api/jobs/99"); !strings.Contains(out, "job_not_found") {
		t.Fatalf("api 错误响应应原样打印: %q", out)
	} else if e, _ := exitOf(err); e != exitNotFound {
		t.Fatalf("api 404 job_not_found 应退出码 4，得到 %d", e)
	}
	for _, args := range [][]string{{"GET", "/jobs"}, {"GET", "/api/jobs/1/log/stream"}, {"FOO", "/api/me"}, {"GET"}} {
		if _, err := run(t, cmdAPI, args...); func() bool { e, _ := exitOf(err); return e != exitUsage }() {
			t.Fatalf("api %v 应是用法错误: %v", args, err)
		}
	}
}

func TestRerun(t *testing.T) {
	startLocal(t, os.Geteuid())
	dir := t.TempDir()
	if _, err := run(t, cmdSubmit, "-C", dir, "-n", "exp", "--pin", "1", "--var", "K=V", "--", "python x.py"); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, cmdRerun, "1", "--json", "--current-code")
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		Name       string            `json:"name"`
		Cmd        string            `json:"cmd"`
		GPUsPinned []int             `json:"gpus_pinned"`
		Env        map[string]string `json:"env"`
		RerunOf    *int64            `json:"rerun_of"`
		CodeFrom   *int64            `json:"code_from"`
	}
	mustJSON(t, out, &j)
	if j.Name != "exp-r2" || j.Cmd != "python x.py" || !slices.Equal(j.GPUsPinned, []int{1}) || j.Env["K"] != "V" ||
		j.RerunOf == nil || *j.RerunOf != 1 || j.CodeFrom != nil {
		t.Fatalf("重跑的任务不对: %s", out)
	}
	// 快照还没生成：服务端拒绝，码照传
	if _, err := run(t, cmdRerun, "1", "--snapshot"); func() bool { _, c := exitOf(err); return c != "job_snapshot_unavailable" }() {
		t.Fatalf("没有快照时 --snapshot 应被拒: %v", err)
	}
}

func TestSocketIdentityAndUnreachable(t *testing.T) {
	startLocal(t, os.Geteuid()+1)
	if _, err := run(t, cmdJobs); func() bool { e, c := exitOf(err); return e != exitUnreachable || c != "socket_uid_mismatch" }() {
		t.Fatalf("别的 uid 应退出码 3: %v", err)
	}
	empty, err := os.MkdirTemp("", "dl")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(empty)
	t.Setenv("DANLU_HOME", empty)
	if _, err := run(t, cmdJobs); func() bool { e, _ := exitOf(err); return e != exitUnreachable }() {
		t.Fatalf("没有服务时应退出码 3: %v", err)
	}
}

func TestParseHelpers(t *testing.T) {
	for in, want := range map[string]string{"x": "x-r2", "x-r2": "x-r3", "a-r9": "a-r10", "x-rr": "x-rr-r2", "-r3": "-r4"} {
		if got := rerunName(in); got != want {
			t.Errorf("rerunName(%q) = %q，应为 %q", in, got, want)
		}
	}
	fs := newFlags("t", "t")
	timeout := fs.Duration("timeout", 0, "")
	pos, err := parseInterleaved(fs, []string{"1", "--timeout", "6h", "2", "--", "-3"})
	if err != nil || !slices.Equal(pos, []string{"1", "2", "-3"}) || *timeout != 6*time.Hour {
		t.Fatalf("交错解析: %v %v %v", pos, *timeout, err)
	}
	if _, err := run(t, func(a []string) error { return parseFlags(newFlags("t", "t"), a) }, "-h"); !errors.Is(err, errHelpShown) {
		t.Fatalf("-h 应返回 errHelpShown: %v", err)
	}
	if e, c := exitOf(parseFlags(newFlags("t", "t"), []string{"--nope"})); e != exitUsage || c != "cli_usage" {
		t.Fatalf("未知参数应是用法错误: %d %s", e, c)
	}
	if e, c := exitOf(apiError(rawResponse{status: 404, body: []byte(`{"error":"接口不存在","code":"not_found"}`)})); e != exitErr || c != "cli_server_old" {
		t.Fatalf("接口不存在应提示服务端旧: %d %s", e, c)
	}
	if _, c := exitOf(apiError(rawResponse{status: 500, body: []byte("boom")})); c != "server_error" {
		t.Fatalf("没有码的服务端错误: %s", c)
	}
}

// 参数写错时 stdout 保持干净（只在 stderr 报错）；-h 只打印一遍用法
func TestFlagErrorsKeepStdoutClean(t *testing.T) {
	out, err := run(t, cmdSubmit, "--bogus", "--json")
	if e, c := exitOf(err); e != exitUsage || c != "cli_usage" || out != "" {
		t.Fatalf("参数错误: 退出码 %d 码 %s stdout %q", e, c, out)
	}
	out, err = run(t, cmdWait, "-h")
	if !errors.Is(err, errHelpShown) || strings.Count(out, "用法") != 1 || !strings.Contains(out, "-timeout") {
		t.Fatalf("-h 应只打印一遍用法: %v %q", err, out)
	}
	// --json 写在任务命令里（没有 --）：属于命令，不改错误格式
	fs := newFlags("submit", "x")
	fs.Bool("json", false, "")
	if err := parseFlags(fs, []string{"python", "x.py", "--json"}); err != nil || jsonErrors {
		t.Fatalf("命令里的 --json 不该切换错误格式: %v %v", err, jsonErrors)
	}
}

// 命令脚本超过上限时报错，不悄悄截断
func TestReadInputLimit(t *testing.T) {
	f := filepath.Join(t.TempDir(), "cmd.sh")
	if err := os.WriteFile(f, []byte(strings.Repeat("x", 11)), 0o600); err != nil {
		t.Fatal(err)
	}
	if b, err := readInput(f, 11, "脚本"); err != nil || len(b) != 11 {
		t.Fatalf("正好到上限应能读: %d %v", len(b), err)
	}
	if _, err := readInput(f, 10, "脚本"); func() bool { e, _ := exitOf(err); return e != exitUsage }() {
		t.Fatalf("超过上限应报用法错误: %v", err)
	}
}

// tail -f 时最后一行还没写完：不在尾部先打印半行，跟随时整行打印一次
func TestTailFollowUnfinishedLine(t *testing.T) {
	env := startLocal(t, os.Geteuid())
	out, err := run(t, cmdSubmit, "--json", "-C", t.TempDir(), "-g", "0", "--", "true")
	if err != nil {
		t.Fatal(err)
	}
	var j struct {
		ID int64 `json:"id"`
	}
	mustJSON(t, out, &j)
	id := itoa(j.ID)
	if _, err := run(t, cmdCancel, id); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env.run.LogPath(j.ID), []byte("first\nsecond\nepoch 3\r 50%\r 100%"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err = run(t, cmdTail, id, "-n", "2", "-f")
	if e, _ := exitOf(err); e != exitJobCancelled || out != "first\nsecond\n 100%\n" {
		t.Fatalf("tail -f: %d %q", e, out)
	}
	// 不跟随时最后半行照常打印
	if out, _ := run(t, cmdTail, id, "-n", "1"); out != " 100%\n" {
		t.Fatalf("tail: %q", out)
	}
}

// 暂缓、恢复、挪位置（FEATURES 3.2）：几个任务要么都改要么都不改；队首说的是第一个没有暂缓的
func TestHoldReleaseMove(t *testing.T) {
	env := startLocal(t, os.Geteuid())
	dir := t.TempDir()
	for range 3 {
		if _, err := run(t, cmdSubmit, "-C", dir, "-g", "1", "--", "python", "train.py"); err != nil {
			t.Fatal(err)
		}
	}
	hold := func(args []string) error { return cmdHold(args, true) }
	if out, err := run(t, hold, "1", "2"); err != nil || !strings.Contains(out, "已暂缓 #1") || !strings.Contains(out, "已暂缓 #2") {
		t.Fatalf("hold: %v %q", err, out)
	}
	if out, err := run(t, hold, "1"); err != nil || !strings.Contains(out, "#1 已经是暂缓的") {
		t.Fatalf("重复 hold: %v %q", err, out)
	}
	if out, err := run(t, cmdGpus); err != nil || !strings.Contains(out, "暂缓 2 个") || !strings.Contains(out, "队首 #3") {
		t.Fatalf("gpus 的队首应跳过暂缓的任务: %v %q", err, out)
	}
	out, err := run(t, cmdHoldRelease, "--json", "2")
	var res struct {
		Action string `json:"action"`
		Items  []struct {
			ID      int64 `json:"id"`
			Changed bool  `json:"changed"`
		} `json:"items"`
	}
	if mustJSON(t, out, &res); err != nil || res.Action != "release" || len(res.Items) != 1 || !res.Items[0].Changed {
		t.Fatalf("release --json: %v %s", err, out)
	}
	if out, err := run(t, cmdMove, "3", "top"); err != nil || !strings.Contains(out, "#3 现在排第 1 位") {
		t.Fatalf("move: %v %q", err, out)
	}
	if _, err := run(t, cmdMove, "3", "left"); func() bool { e, _ := exitOf(err); return e != exitUsage }() {
		t.Fatalf("无效位置应是用法错误: %v", err)
	}
	if _, err := run(t, hold, "2", "99"); func() bool { e, c := exitOf(err); return e != exitNotFound || c != "job_not_found" }() {
		t.Fatalf("不存在的任务应退出码 4: %v", err)
	}
	if j, _ := env.st.GetJob(2); j.Held {
		t.Fatal("整批失败时 #2 不应被暂缓")
	}
	if _, err := run(t, cmdCancel, "2"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, hold, "--json", "2"); func() bool { e, c := exitOf(err); return e != exitErr || c != "job_not_queued" }() {
		t.Fatalf("已结束的任务不能暂缓: %v", err)
	}
}

func cmdHoldRelease(args []string) error { return cmdHold(args, false) }
