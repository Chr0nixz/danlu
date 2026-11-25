package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
		{store.EventAlertRaised, `{"type":"blocked_unknown"}`},
		{store.EventAlertRaised, `{"type":"external","gpus":[3],"pid":1325028,"other_job":56}`},
	} {
		addEvent(t, st, ts, e.kind, &id, e.data)
	}
	for range 10 {
		step(t, s, ts)
	}
	if len(r.requests) != 8 {
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
	if !strings.Contains(r.requests[6].title, "未知显存") || !strings.Contains(r.requests[6].body, "GPU 显存释放") {
		t.Fatalf("未知显存阻塞通知文案错误: %+v", r.requests[6])
	}
	if !strings.Contains(r.requests[7].body, "任务 #56 的进程") || strings.Contains(r.requests[7].body, "外部进程") {
		t.Fatalf("别的任务跑过来的进程不应写成外部进程: %+v", r.requests[7])
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

func TestReservationMessages(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	future, past := ts+600, ts-1
	for _, data := range []string{
		`{"id":3,"gpus":[2,3],"note":"调试\n显存","duration":10800}`,
		`{"id":4,"gpus":[1]}`,
	} {
		addEvent(t, st, ts, store.EventReservationReady, nil, data)
	}
	addEvent(t, st, ts, store.EventReservationExpiring, nil, fmt.Sprintf(`{"id":3,"gpus":[2,3],"ends_at":%d}`, future))
	// 积压到已经过了到期时间的提醒不再发；结束事件不推送
	addEvent(t, st, ts-900, store.EventReservationExpiring, nil, fmt.Sprintf(`{"id":5,"gpus":[0],"ends_at":%d}`, past))
	addEvent(t, st, ts, store.EventReservationEnded, nil, `{"id":3,"gpus":[2,3],"reason":"手动释放"}`)
	for range 10 {
		step(t, s, ts)
	}
	if len(r.requests) != 3 {
		t.Fatalf("预约推送数量错误: %+v", r.requests)
	}
	ready, untimed, expiring := r.requests[0], r.requests[1], r.requests[2]
	if !strings.Contains(ready.title, "GPU 预约就绪") || ready.body != "预约 #3（调试 显存）已就绪：GPU 2, 3。3 小时后自动释放。" || ready.click != s.baseURL+"/" {
		t.Fatalf("就绪推送: %+v", ready)
	}
	if untimed.body != "预约 #4 已就绪：GPU 1。手动释放前一直保留。" {
		t.Fatalf("不定时的就绪推送: %+v", untimed)
	}
	if !strings.Contains(expiring.title, "即将到期") || expiring.body != "预约 #3 的 GPU 2, 3 约 10 分钟后自动释放，需要的话在面板上延长。" {
		t.Fatalf("到期提醒: %+v", expiring)
	}
}

func TestStrayMessages(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	id := int64(82)
	addEvent(t, st, ts, store.EventAlertRaised, &id, `{"type":"stray","gpus":[0,1],"pid":2398153,"assigned":[2,3,4,5],"reserved":[0,1],"cuda_visible":"0,1,2,3"}`)
	addEvent(t, st, ts, store.EventAlertRaised, &id, `{"type":"stray","gpus":[0],"pid":7,"assigned":[2],"graphics":true}`)
	addEvent(t, st, ts, store.EventAlertRaised, &id, `{"type":"stray","gpus":[3],"pid":8,"cuda_visible":""}`)
	addEvent(t, st, ts, store.EventAlertCleared, &id, `{"type":"stray","gpus":[0,1],"pid":2398153}`)
	for range 10 {
		step(t, s, ts)
	}
	if len(r.requests) != 3 {
		t.Fatalf("越界推送数量错误（清除不推送）: %+v", r.requests)
	}
	cvd, graphics, unset := r.requests[0], r.requests[1], r.requests[2]
	if !strings.Contains(cvd.title, "任务用了没分给它的 GPU") || cvd.click != s.baseURL+"/jobs/82" ||
		cvd.body != "任务 #82 的进程在 GPU 0, 1 上，分给它的是 GPU 2, 3, 4, 5。其中 GPU 0, 1 已被预约。进程的 CUDA_VISIBLE_DEVICES 指向 GPU 0,1,2,3，启动器、框架或脚本改写了它（如 deepspeed --num_gpus、accelerate 的 gpu_ids、按 nvidia-smi/NVML 的编号选卡、脚本里的 export）。" {
		t.Fatalf("选卡变量被改写: %+v", cvd)
	}
	if !strings.HasSuffix(graphics.body, "这些是图形渲染进程（EGL/OpenGL），不受 CUDA_VISIBLE_DEVICES 限制，要在程序里指定渲染用的卡。") {
		t.Fatalf("图形进程: %+v", graphics)
	}
	if unset.body != "任务 #82 没分到 GPU，进程却在 GPU 3 上。进程没有 CUDA_VISIBLE_DEVICES，启动器或脚本把它去掉了。" {
		t.Fatalf("选卡变量被去掉: %+v", unset)
	}
}

func TestStrictUnavailableMessage(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	addEvent(t, st, ts, store.EventAlertRaised, nil, `{"type":"strict_unavailable","reason":"no_sudo"}`)
	addEvent(t, st, ts, store.EventAlertRaised, nil, `{"type":"strict_unavailable","reason":"failed"}`)
	addEvent(t, st, ts, store.EventAlertCleared, nil, `{"type":"strict_unavailable","reason":"failed"}`)
	for range 10 {
		step(t, s, ts)
	}
	if len(r.requests) != 2 {
		t.Fatalf("严格限卡推送数量错误（恢复不推送）: %+v", r.requests)
	}
	if !strings.Contains(r.requests[0].title, "严格限卡不可用") ||
		r.requests[0].body != "严格限卡开着，但 root 助手不可用，排队的任务暂停启动。sudo 没有放行助手：在服务器上重新运行 sudo danlu strict install，或在面板设置里关掉严格限卡。" ||
		r.requests[0].click != s.baseURL+"/" {
		t.Fatalf("没放行 sudo 的推送: %+v", r.requests[0])
	}
	if !strings.HasSuffix(r.requests[1].body, "打开面板「设置 → 调度」查看原因。") {
		t.Fatalf("其他原因不转发原始错误，只指路: %+v", r.requests[1])
	}
}

// ntfy 暂时连不上时消息留在发件箱里按退避重试，沿用同一个序列标识；恢复后送达并清掉错误
func TestFailedPushIsRetriedLater(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	addEvent(t, st, ts, store.EventQueuePaused, nil, `{"kind":"disk"}`)
	r.status = http.StatusServiceUnavailable
	step(t, s, ts)
	if len(r.requests) != 4 || len(s.state.Outbox) != 1 || s.state.Outbox[0].RetryAt <= ts {
		t.Fatalf("失败的消息应留在发件箱里等重试: %d 次请求，%+v", len(r.requests), s.state.Outbox)
	}
	if status, _ := s.Status(); !strings.Contains(status.LastError, "稍后重试") {
		t.Fatalf("设置页应说明会重试: %+v", status)
	}
	step(t, s, ts+1)
	if len(r.requests) != 4 {
		t.Fatal("退避时间没到不应重试")
	}
	r.status = 200
	step(t, s, ts+int64(retryMin/time.Second))
	if len(r.requests) != 5 || len(s.state.Outbox) != 0 || r.requests[4].sequence != r.requests[0].sequence {
		t.Fatalf("恢复后应送达，沿用同一个序列标识: %+v %+v", r.requests, s.state.Outbox)
	}
	if status, _ := s.Status(); status.LastError != "" || status.LastSentAt == nil {
		t.Fatalf("送达后应清掉错误: %+v", status)
	}
}

// 重试也没用的（4xx：topic、令牌错了）不留；失败超过 retryFor 的放弃
func TestPushRetryGivesUp(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	addEvent(t, st, ts, store.EventQueuePaused, nil, `{"kind":"disk"}`)
	r.status = http.StatusUnauthorized
	step(t, s, ts)
	if len(r.requests) != 1 || len(s.state.Outbox) != 0 {
		t.Fatalf("401 不应重试: %d 次请求，%+v", len(r.requests), s.state.Outbox)
	}
	now := time.Unix(ts, 0)
	if _, retry := retryLater(Message{FailingSince: ts - int64(retryFor/time.Second)}, errors.New("连接 ntfy 超时"), now); retry {
		t.Fatal("失败超过 retryFor 应放弃")
	}
	m, retry := retryLater(Message{}, &SendError{msg: "ntfy 返回 HTTP 429", Status: 429, RetryAfter: 10 * time.Minute}, now)
	if !retry || m.RetryAt != ts+600 || m.FailingSince != ts || m.Retries != 1 {
		t.Fatalf("429 应按 Retry-After 再试: %+v", m)
	}
	m, _ = retryLater(Message{Retries: 20, FailingSince: ts}, errors.New("连接 ntfy 超时"), now)
	if m.RetryAt != ts+int64(retryMax/time.Second) {
		t.Fatalf("退避应有上限: %+v", m)
	}
}

// 限流时不连发四次，马上交回发件箱按 Retry-After 等
func TestPublisherStopsOnRateLimit(t *testing.T) {
	var n int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n++
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	p := Publisher{Config: config.Notify{NTFY: server.URL + "/topic"}, retryDelay: time.Millisecond}
	err := p.Send(context.Background(), Message{Body: "内容"})
	var se *SendError
	if !errors.As(err, &se) || se.RetryAfter != 2*time.Minute || n != 1 {
		t.Fatalf("429 应只发一次并带上 Retry-After: %v（%d 次）", err, n)
	}
}
