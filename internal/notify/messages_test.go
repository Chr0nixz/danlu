package notify

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"danlu/internal/store"
)

// 两种语言的键集必须完全一致，占位符集合也必须一致（插值参数由调用点给出，与语言无关）。
func TestMessageCatalogParity(t *testing.T) {
	zh, en := messages["zh-CN"], messages["en"]
	for k := range zh {
		if _, ok := en[k]; !ok {
			t.Errorf("en 缺键 %s", k)
		}
	}
	for k := range en {
		if _, ok := zh[k]; !ok {
			t.Errorf("en 多键 %s", k)
		}
	}
	placeholders := func(s string) []string {
		ms := placeholderRe.FindAllString(s, -1)
		sort.Strings(ms)
		return ms
	}
	for k, zhTpl := range zh {
		enTpl, ok := en[k]
		if !ok {
			continue
		}
		zs, es := placeholders(zhTpl), placeholders(enTpl)
		if strings.Join(zs, ",") != strings.Join(es, ",") {
			t.Errorf("键 %s 占位符不一致: zh %v en %v", k, zs, es)
		}
	}
}

// 英文推送：失败批次、快速失败暂停、预约就绪都按 en 目录合成，不泄漏中文。
func TestEnglishPushes(t *testing.T) {
	st, s, r := fixture(t)
	s.Lang = func() string { return "en" }
	ts := time.Now().Unix()
	id := int64(1)
	addEvent(t, st, ts, store.EventJobFinished, &id,
		`{"status":"failed","name":"vit","fail_kind":"cuda_oom","fail_detail":"","exit_code":1,"reason":"CUDA 显存不足","duration":61}`)
	step(t, s, ts+1)
	step(t, s, ts+40)
	if len(r.requests) != 1 {
		t.Fatalf("期望一条失败推送: %+v", r.requests)
	}
	got := r.requests[0]
	if !strings.Contains(got.title, "Jobs failed") || !strings.Contains(got.body, "#1 vit: CUDA out of memory") {
		t.Fatalf("英文失败推送内容错误: %+v", got)
	}
	if strings.Contains(got.body, "显存") || strings.Contains(got.title, "任务") {
		t.Fatalf("英文推送泄漏中文: %+v", got)
	}
}

func TestEnglishHostOOMVariants(t *testing.T) {
	st, s, r := fixture(t)
	s.Lang = func() string { return "en" }
	ts := time.Now().Unix()
	sure, guess, worker := int64(1), int64(2), int64(3)
	addEvent(t, st, ts, store.EventJobFinished, &sure,
		`{"status":"failed","name":"a","fail_kind":"host_oom","fail_detail":"confirmed","exit_code":137,"reason":"主机内存不足，被系统杀掉"}`)
	addEvent(t, st, ts, store.EventJobFinished, &guess,
		`{"status":"failed","name":"b","fail_kind":"host_oom","exit_code":137,"reason":"退出码 137，可能是主机内存不足"}`)
	addEvent(t, st, ts, store.EventJobFinished, &worker,
		`{"status":"failed","name":"c","fail_kind":"host_oom","fail_detail":"worker","reason":"运行期间本机发生过 OOM kill"}`)
	step(t, s, ts+1)
	step(t, s, ts+40)
	if len(r.requests) != 1 {
		t.Fatalf("期望一条合并推送: %+v", r.requests)
	}
	body := r.requests[0].body
	if !strings.Contains(body, "#1 a: Host out of memory") ||
		!strings.Contains(body, "#2 b: Possibly host out of memory") ||
		!strings.Contains(body, "#3 c: Host out of memory (OOM kill on this host while running)") {
		t.Fatalf("host_oom 三态英文文案错误: %s", body)
	}
}

// 语言切换对未发批次生效：批次先按中文消费入队，发送前切到英文，合成时用英文。
func TestLangSwitchAppliesToPendingBatch(t *testing.T) {
	st, s, r := fixture(t)
	ts := time.Now().Unix()
	id := int64(1)
	addEvent(t, st, ts, store.EventJobFinished, &id, `{"status":"succeeded","name":"vit","notify_done":true,"duration":3600}`)
	step(t, s, ts+1) // 消费入批次，尚未到 30 秒窗口
	s.Lang = func() string { return "en" }
	step(t, s, ts+40)
	if len(r.requests) != 1 || !strings.Contains(r.requests[0].body, "#1 vit: succeeded in 1 h 0 min") {
		t.Fatalf("切换语言后未发批次应按英文合成: %+v", r.requests)
	}
}

func TestEnglishReservationAndTestPush(t *testing.T) {
	st, s, r := fixture(t)
	s.Lang = func() string { return "en" }
	ts := time.Now().Unix()
	addEvent(t, st, ts, store.EventReservationReady, nil,
		`{"id":3,"note":"调试 显存","gpus":[2,3],"duration":10800}`)
	addEvent(t, st, ts, store.EventReservationReady, nil, `{"id":4,"gpus":[1]}`)
	addEvent(t, st, ts, store.EventReservationExpiring, nil, fmt.Sprintf(`{"id":3,"note":"调试","gpus":[2,3],"ends_at":%d}`, ts+600))
	for range 5 {
		step(t, s, ts+1)
	}
	if len(r.requests) != 3 {
		t.Fatalf("期望三条预约推送: %+v", r.requests)
	}
	got := r.requests[0]
	if !strings.Contains(got.title, "GPU reservation ready") ||
		got.body != "Reservation #3 (调试 显存) is ready: GPU 2, 3. Auto-releases in 3 h." {
		t.Fatalf("英文预约推送错误: %+v", got)
	}
	if b := r.requests[1].body; b != "Reservation #4 is ready: GPU 1. Held until released manually." {
		t.Fatalf("没有备注的英文预约推送错误: %q", b)
	}
	if b := r.requests[2].body; !strings.HasPrefix(b, "GPU 2, 3 of reservation #3 (调试) will auto-release in about ") {
		t.Fatalf("英文到期提醒错误: %q", b)
	}
	if err := s.Test(t.Context()); err != nil {
		t.Fatal(err)
	}
	last := r.requests[len(r.requests)-1]
	if !strings.Contains(last.title, "Test push") || last.body != "Danlu phone push is connected." {
		t.Fatalf("英文测试推送错误: %+v", last)
	}
}
