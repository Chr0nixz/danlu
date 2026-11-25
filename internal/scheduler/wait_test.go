package scheduler

import (
	"encoding/json"
	"testing"
)

// Text() 是旧客户端看到的 wait_reason，必须和改成机器码之前逐字一致
func TestWaitTextMatchesLegacy(t *testing.T) {
	cases := []struct {
		w    Wait
		want string
	}{
		{Wait{Kind: WaitAhead}, "等待前面的任务"},
		{Wait{Kind: WaitScheduling}, "等待调度"},
		{Wait{Kind: WaitPaused, Detail: "手动暂停"}, "队列已暂停：手动暂停"},
		{Wait{Kind: WaitGPUs, Need: 2, Free: 0, Blocker: &WaitBlocker{Kind: BlockExternal, GPU: 3, PID: 900003}},
			"等待 2 张空闲卡（当前 0 张可用）；GPU 3 有外部进程（PID 900003）"},
		{Wait{Kind: WaitPinned, Pinned: []int{0, 2}, Blocker: &WaitBlocker{Kind: BlockReserved, GPU: 2, ReservationID: 4}},
			"等待指定 GPU [0 2]；GPU 2 已被预约 #4 占用"},
		{Wait{Kind: WaitGPUs, Need: 1, Free: 0, Blocker: &WaitBlocker{Kind: BlockJobProc, GPU: 1, JobID: 7, PID: 42}},
			"等待 1 张空闲卡（当前 0 张可用）；GPU 1 有任务 #7 的进程（PID 42），这张卡没有分给它"},
	}
	for _, c := range cases {
		if got := c.w.Text(); got != c.want {
			t.Errorf("%+v: got %q want %q", c.w, got, c.want)
		}
	}
	// 中文诊断原文不进 JSON，面板只拿机器码
	b, _ := json.Marshal(Wait{Kind: WaitStrict, Detail: "没有安装 root 助手"})
	if string(b) != `{"kind":"strict"}` {
		t.Fatalf("JSON 不应带中文详情: %s", b)
	}
}
