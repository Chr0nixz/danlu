package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"danlu/internal/config"
	"danlu/internal/store"
)

type received struct {
	body, title, click, priority, auth, sequence string
}

type recorder struct {
	mu       sync.Mutex
	requests []received
	status   int
}

func (r *recorder) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	body, _ := io.ReadAll(req.Body)
	title, _ := new(mime.WordDecoder).DecodeHeader(req.Header.Get("Title"))
	r.mu.Lock()
	r.requests = append(r.requests, received{string(body), title, req.Header.Get("Click"), req.Header.Get("Priority"), req.Header.Get("Authorization"), req.Header.Get("X-Sequence-ID")})
	status := r.status
	r.mu.Unlock()
	if status == 0 {
		status = 200
	}
	w.WriteHeader(status)
	_, _ = w.Write([]byte("回显敏感 topic 与 token，不应进入错误"))
}

func fixture(t *testing.T) (*store.Store, *Service, *recorder) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "danlu.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	r := &recorder{}
	server := httptest.NewServer(r)
	t.Cleanup(server.Close)
	s := New(st, config.Notify{NTFY: server.URL + "/secret-topic", Token: "secret-token"}, "测试主机", "https://203.0.113.1:45678")
	s.publisher.retryDelay = time.Millisecond
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	return st, s, r
}

func addEvent(t *testing.T, st *store.Store, ts int64, kind string, id *int64, data string) {
	t.Helper()
	if err := st.AppendEvents(&store.Event{TS: ts, Kind: kind, JobID: id, Data: json.RawMessage(data)}); err != nil {
		t.Fatal(err)
	}
}

func step(t *testing.T, s *Service, ts int64) {
	t.Helper()
	if err := s.step(context.Background(), time.Unix(ts, 0)); err != nil {
		t.Fatal(err)
	}
}

func TestQueueSummaryMergesPendingNoticesAcrossRestart(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	previous := int64(1)
	addEvent(t, st, ts, store.EventJobFinished, &previous, `{"status":"succeeded","name":"旧任务"}`)
	s.publisher.Config.QueueDrained = true
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	a, b := int64(2), int64(3)
	addEvent(t, st, ts+1, store.EventJobFinished, &a, `{"status":"succeeded","name":"试验成功","notify_done":true,"duration":60}`)
	step(t, s, ts+2)
	s = New(st, s.publisher.Config, s.hostname, s.baseURL)
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	addEvent(t, st, ts+5, store.EventJobFinished, &b, `{"status":"failed","name":"试验失败","fail_kind":"cuda_oom","reason":"私有路径"}`)
	addEvent(t, st, ts+5, store.EventQueueDrained, nil, `{}`)
	step(t, s, ts+5)
	step(t, s, ts+40)
	if len(r.requests) != 1 {
		t.Fatalf("同批待发的通知应并入一条汇总: %+v", r.requests)
	}
	got := r.requests[0]
	if !strings.Contains(got.title, "队列已跑完") || !strings.Contains(got.body, "2 个任务，1 成功，1 失败") ||
		!strings.Contains(got.body, "CUDA 显存不足") || strings.Contains(got.body, "私有路径") ||
		!strings.HasSuffix(got.click, "/jobs?finished_from_event=2&finished_to_event=4") {
		t.Fatalf("汇总内容或筛选范围错误: %+v", got)
	}
}

func TestSingletonBatchesStillCoalesceIndividualNotifications(t *testing.T) {
	st, s, r := fixture(t)
	s.publisher.Config.QueueDrained = true
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Unix()
	a, b := int64(2), int64(3)
	addEvent(t, st, ts, store.EventJobFinished, &a, `{"status":"succeeded","name":"成功","notify_done":true}`)
	addEvent(t, st, ts, store.EventQueueDrained, nil, `{}`)
	addEvent(t, st, ts+3, store.EventJobFinished, &b, `{"status":"failed","name":"失败","exit_code":2}`)
	addEvent(t, st, ts+3, store.EventQueueDrained, nil, `{}`)
	step(t, s, ts+31)
	if len(r.requests) != 1 || !strings.Contains(r.requests[0].title, "任务结束") ||
		!strings.Contains(r.requests[0].body, "#2") || !strings.Contains(r.requests[0].body, "#3") {
		t.Fatalf("单任务结批不应触发汇总，同窗口仍应合并: %+v", r.requests)
	}
}

func TestFastPauseAcrossEventPageAndLaterSummary(t *testing.T) {
	st, s, r := fixture(t)
	s.publisher.Config.QueueDrained = true
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Unix()
	events := make([]*store.Event, 99)
	for i := range events {
		events[i] = &store.Event{TS: ts, Kind: store.EventJobStarted}
	}
	if err := st.AppendEvents(events...); err != nil {
		t.Fatal(err)
	}
	fast, other := int64(4), int64(5)
	addEvent(t, st, ts, store.EventJobFinished, &fast, `{"status":"failed","name":"快速失败","exit_code":3}`)
	addEvent(t, st, ts+1, store.EventQueuePaused, &fast, `{"kind":"fail_fast"}`)
	step(t, s, ts+40)
	step(t, s, ts+41)
	if len(r.requests) != 1 || !strings.Contains(r.requests[0].title, "队列暂停") ||
		!strings.Contains(r.requests[0].body, "退出码 3") {
		t.Fatalf("跨页的快速失败应只发暂停: %+v", r.requests)
	}
	addEvent(t, st, ts+42, store.EventQueueResumed, nil, `{}`)
	addEvent(t, st, ts+43, store.EventJobFinished, &other, `{"status":"cancelled","name":"后续任务","notify_done":true}`)
	addEvent(t, st, ts+43, store.EventQueueDrained, nil, `{}`)
	step(t, s, ts+43)
	step(t, s, ts+80)
	if len(r.requests) != 2 || !strings.Contains(r.requests[1].body, "2 个任务，0 成功，1 失败，1 取消") {
		t.Fatalf("继续后汇总应包含快速失败和取消: %+v", r.requests)
	}
}

func TestFailuresBatchAcrossRestartAndPrivacy(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	id := int64(42)
	addEvent(t, st, ts, store.EventJobFinished, &id, `{"status":"failed","name":"vit-lr3e-4","fail_kind":"cuda_oom","reason":"/secret/path 安全入口abcdef","error_line":"SECRET_LOG"}`)
	id++
	addEvent(t, st, ts+10, store.EventJobFinished, &id, `{"status":"failed","name":"训练二","fail_kind":"exception","exit_code":1}`)
	id++
	addEvent(t, st, ts+15, store.EventJobFinished, &id, `{"status":"cancelled"}`)
	step(t, s, ts+20)
	if len(r.requests) != 0 || s.state.Batch.Count != 2 {
		t.Fatal("30 秒内应合并、等待发送")
	}
	// 批次和游标一起持久化，重启不丢掉窗口，也不会提前发送。
	s = New(st, s.publisher.Config, s.hostname, s.baseURL)
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	step(t, s, ts+29)
	if len(r.requests) != 0 {
		t.Fatal("重启后提前发送")
	}
	step(t, s, ts+30)
	if len(r.requests) != 1 {
		t.Fatalf("应只合并发送一条: %+v", r.requests)
	}
	got := r.requests[0]
	if !strings.Contains(got.body, "#42 vit-lr3e-4：CUDA 显存不足") || !strings.Contains(got.body, "#43 训练二：Python 异常") ||
		strings.Contains(got.body, "SECRET") || strings.Contains(got.body, "/secret") || strings.Contains(got.body, "abcdef") ||
		got.title != "测试主机 · 任务失败" || got.click != s.baseURL+"/jobs?status=failed" || got.auth != "Bearer secret-token" || got.priority != "3" {
		t.Fatalf("推送内容或隐私边界错误: %+v", got)
	}
	s = New(st, s.publisher.Config, s.hostname, s.baseURL)
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	step(t, s, ts+100)
	if len(r.requests) != 1 {
		t.Fatal("重启后重复推送")
	}
	status, err := s.Status()
	if err != nil || status.LastSentAt == nil || status.LastError != "" || !status.Enabled {
		t.Fatalf("状态错误: %+v %v", status, err)
	}
}

func TestQueueAndAlertMessages(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	id := int64(7)
	for _, e := range []struct{ kind, data string }{
		{store.EventQueuePaused, `{"kind":"manual","reason":"SECRET"}`},
		{store.EventQueuePaused, `{"kind":"fail_fast","reason":"SECRET /path"}`},
		{store.EventAlertRaised, `{"type":"gpu_error","message":"SECRET /driver/path"}`},
		{store.EventAlertCleared, `{"type":"gpu_error"}`},
		{store.EventAlertRaised, `{"type":"external","gpus":[0,1],"pid":1234}`},
		{store.EventAlertRaised, `{"type":"disk_low","available_bytes":1073741824,"data_disk":true}`},
		{store.EventAlertCleared, `{"type":"disk_low"}`},
		{store.EventAlertRaised, `{"type":"blocked"}`},
	} {
		addEvent(t, st, ts, e.kind, &id, e.data)
	}
	for range 10 {
		step(t, s, ts)
	}
	if len(r.requests) != 6 {
		t.Fatalf("通知事件缺失或手动暂停/普通恢复被发送: %+v", r.requests)
	}
	if r.requests[0].priority != "4" || r.requests[0].click != s.baseURL+"/jobs/7" {
		t.Fatal("队列暂停优先级或链接错误")
	}
	for _, req := range r.requests {
		if strings.Contains(req.body, "SECRET") || strings.Contains(req.body, "/path") {
			t.Fatalf("泄露原始文案: %+v", req)
		}
	}
}

func TestFailFastPauseCarriesFailureInOnePush(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	id := int64(7)
	addEvent(t, st, ts, store.EventJobFinished, &id, `{"status":"failed","name":"lr-typo","fail_kind":"exception"}`)
	addEvent(t, st, ts, store.EventQueuePaused, &id, `{"kind":"fail_fast","reason":"SECRET /path"}`)
	for i := range int64(40) {
		step(t, s, ts+i)
	}
	if len(r.requests) != 1 || s.state.Batch != nil {
		t.Fatalf("快速失败应只推送一条: %+v", r.requests)
	}
	got := r.requests[0]
	if got.title != "测试主机 · 队列暂停" || got.priority != "4" || got.click != s.baseURL+"/jobs/7" ||
		!strings.Contains(got.body, "#7 lr-typo：Python 异常") || strings.Contains(got.body, "SECRET") {
		t.Fatalf("暂停推送应带上失败原因且不含原始文案: %+v", got)
	}
}

func TestFailFastPauseLeavesOtherFailuresBatched(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	other, fast := int64(1), int64(2)
	addEvent(t, st, ts, store.EventJobFinished, &other, `{"status":"failed","name":"a","exit_code":2}`)
	// 暂停事件恰好落在窗口边界之后，也不能先把这次失败随批次发出去。
	addEvent(t, st, ts+29, store.EventJobFinished, &fast, `{"status":"failed","name":"b","fail_kind":"cuda_oom"}`)
	addEvent(t, st, ts+30, store.EventQueuePaused, &fast, `{"kind":"fail_fast"}`)
	for range 3 {
		step(t, s, ts+30)
	}
	if len(r.requests) != 2 {
		t.Fatalf("应发出暂停和其余失败各一条: %+v", r.requests)
	}
	pause, batch := r.requests[0], r.requests[1]
	if pause.priority != "4" || !strings.Contains(pause.body, "#2 b：CUDA 显存不足") {
		t.Fatalf("暂停推送错误: %+v", pause)
	}
	if !strings.Contains(batch.body, "#1 a：退出码 2") || strings.Contains(batch.body, "#2") || batch.click != s.baseURL+"/jobs/1" {
		t.Fatalf("快速失败的任务不应重复出现在失败批次里: %+v", batch)
	}
}

func TestHostOOMPushKeepsUncertainty(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	sure, guess := int64(1), int64(2)
	addEvent(t, st, ts, store.EventJobFinished, &sure, `{"status":"failed","name":"a","fail_kind":"host_oom","exit_code":137,"reason":"主机内存不足，被系统杀掉"}`)
	addEvent(t, st, ts, store.EventJobFinished, &guess, `{"status":"failed","name":"b","fail_kind":"host_oom","exit_code":137,"reason":"退出码 137，可能是主机内存不足"}`)
	for range 3 {
		step(t, s, ts+30)
	}
	if len(r.requests) != 1 || !strings.Contains(r.requests[0].body, "#1 a：主机内存不足") ||
		!strings.Contains(r.requests[0].body, "#2 b：可能是主机内存不足") {
		t.Fatalf("推测的内存不足应在推送中说明: %+v", r.requests)
	}
}

type failingStore struct {
	StateStore
	fail       bool
	failFinish bool
}

func (f *failingStore) SetSettings(values map[string]string) error {
	if f.fail || (f.failFinish && values["ntfy.last_sent_at"] != "") {
		return errors.New("模拟磁盘写入失败")
	}
	return f.StateStore.SetSettings(values)
}

func TestPersistenceFailureNeverResendsDeliveredMessage(t *testing.T) {
	st, s, r := fixture(t)
	f := &failingStore{StateStore: st, fail: true}
	s.st = f
	ts := time.Now().Unix()
	addEvent(t, st, ts, store.EventQueuePaused, nil, `{"kind":"disk"}`)
	if err := s.step(context.Background(), time.Unix(ts, 0)); err == nil || len(r.requests) != 0 {
		t.Fatal("认领写库失败不能发送")
	}
	f.fail, f.failFinish = false, true
	if err := s.step(context.Background(), time.Unix(ts, 0)); err == nil || len(r.requests) != 1 {
		t.Fatal("应在发送后模拟收尾写入失败")
	}
	f.failFinish = false
	step(t, s, ts+1)
	if len(r.requests) != 1 {
		t.Fatal("收尾重试重复发了 HTTP 请求")
	}
}

func TestRestartSkipsUnknownDeliveryAndKeepsRemainingOutbox(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	for range 2 {
		addEvent(t, st, ts, store.EventQueuePaused, nil, `{"kind":"disk"}`)
	}
	f := &failingStore{StateStore: st, failFinish: true}
	s.st = f
	if err := s.step(context.Background(), time.Unix(ts, 0)); err == nil {
		t.Fatal("应模拟收尾写入失败")
	}
	s = New(st, s.publisher.Config, s.hostname, s.baseURL)
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	status, err := s.Status()
	if err != nil || !strings.Contains(status.LastError, "结果未知") {
		t.Fatalf("应明确报告未确认的发送: %+v %v", status, err)
	}
	step(t, s, ts)
	if len(r.requests) != 2 || r.requests[0].sequence == r.requests[1].sequence {
		t.Fatal("重启重发了旧消息或丢掉后续消息")
	}
}

func TestFirstEnableAndTopicChangeSkipHistory(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	id := int64(1)
	addEvent(t, st, ts, store.EventJobFinished, &id, `{"status":"failed"}`)
	s = New(st, config.Notify{}, s.hostname, s.baseURL)
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	addEvent(t, st, ts, store.EventJobFinished, &id, `{"status":"failed"}`)
	s = New(st, config.Notify{NTFY: "http://127.0.0.1:1/new-topic"}, s.hostname, s.baseURL)
	if err := s.Initialize(); err != nil {
		t.Fatal(err)
	}
	step(t, s, ts+100)
	if s.state.Batch != nil || len(r.requests) != 0 {
		t.Fatal("启用通知不应补发关闭期间的历史事件")
	}
}

func TestPublisherRetriesAndRedactsErrors(t *testing.T) {
	r := &recorder{status: 503}
	server := httptest.NewServer(r)
	defer server.Close()
	p := Publisher{Config: config.Notify{NTFY: server.URL + "/secret-topic", Token: "secret-token"}, retryDelay: time.Millisecond}
	err := p.Send(context.Background(), Message{Title: "测试", Body: "内容", Priority: 3})
	if err == nil || err.Error() != "ntfy 返回 HTTP 503" || len(r.requests) != 4 {
		t.Fatalf("重试次数或错误处理不正确: %v %+v", err, r.requests)
	}
	for _, req := range r.requests {
		if req.sequence == "" || req.sequence != r.requests[0].sequence {
			t.Fatal("重试应沿用相同序列标识")
		}
	}
	redirected := 0
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected++ }))
	defer other.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) { http.Redirect(w, req, other.URL+"/foreign", 307) }))
	defer redirect.Close()
	p.Config.NTFY = redirect.URL + "/secret-topic"
	if err := p.Send(context.Background(), Message{Body: "内容"}); err == nil || redirected != 0 {
		t.Fatal("不应跟随重定向泄露消息或鉴权")
	}
}

func TestManualTestDoesNotConsumeEvents(t *testing.T) {
	_, s, r := fixture(t)
	cursor := s.state.Cursor
	if err := s.Test(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.requests) != 1 || s.state.Cursor != cursor {
		t.Fatal("测试推送不应影响事件游标")
	}
	s.testMu.Lock()
	if err := s.Test(context.Background()); !errors.Is(err, ErrBusy) {
		t.Fatal("并发测试应立即返回忙碌")
	}
	s.testMu.Unlock()
	s.publisher.Config.NTFY = ""
	if err := s.Test(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatal("未配置推送应返回明确错误")
	}
}
