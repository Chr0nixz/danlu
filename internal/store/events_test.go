package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"testing"
)

func TestAlertEventsRestoreAndSettingsAtomic(t *testing.T) {
	s := openTest(t)
	first := &Event{Kind: EventAlertRaised, Key: "gpu_error", DedupeKey: "first"}
	if err := s.AppendEvents(first, first); err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 {
		t.Fatal("写入后应返回持久化 id")
	}
	latest, err := s.LatestAlerts()
	if err != nil || latest["gpu_error"].Kind != EventAlertRaised {
		t.Fatalf("恢复活动告警错误: %+v %v", latest, err)
	}
	if err := s.AppendEvents(&Event{Kind: EventAlertCleared, Key: "gpu_error"}); err != nil {
		t.Fatal(err)
	}
	latest, err = s.LatestAlerts()
	last, lastErr := s.LastEventID()
	if err != nil || lastErr != nil || latest["gpu_error"].Kind != EventAlertCleared || last != latest["gpu_error"].ID {
		t.Fatal("恢复记录或尾游标不正确")
	}
	if err := s.SetSettings(map[string]string{"a": "1", "": "bad"}); err == nil {
		t.Fatal("无效键应使整次更新回滚")
	}
	if _, err := s.GetSetting("a"); !errors.Is(err, ErrNotFound) {
		t.Fatal("事务失败留下部分状态")
	}
}

func TestTransitionAtomicAndIdempotent(t *testing.T) {
	s := openTest(t)
	j := &Job{Name: "j", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	if err := s.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	started := &Event{Kind: EventJobStarted, JobID: &j.ID, Data: json.RawMessage(`{"pid":42}`)}
	finished := &Event{Kind: EventJobFinished, JobID: &j.ID, Data: json.RawMessage(`{"status":"succeeded"}`)}
	j.Status, j.PID, j.Notes = StatusSucceeded, 42, "已清理 1 个残留进程"
	// 故意让第二条事件写入失败，任务和第一条事件也必须回滚。
	if _, err := s.db.Exec(`CREATE TRIGGER reject_finished BEFORE INSERT ON events
		WHEN NEW.kind='job_finished' BEGIN SELECT RAISE(ABORT, '模拟事件写入失败'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(j, started, finished); err == nil {
		t.Fatal("事件写入失败应使整个事务失败")
	}
	got, err := s.GetJob(j.ID)
	if err != nil || got.Status != StatusQueued || got.PID != 0 || got.Notes != "" {
		t.Fatalf("事务失败后任务发生变化: %+v %v", got, err)
	}
	events, err := s.EventsAfter(0, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("事务失败后留下了事件: %+v %v", events, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_finished"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Transition(j, started, finished); err != nil {
			t.Fatal(err)
		}
	}
	events, err = s.EventsAfter(0, 1)
	if err != nil || len(events) != 1 || events[0].Kind != EventJobStarted || events[0].TS == 0 ||
		string(events[0].Data) != `{"pid":42}` || events[0].JobID == nil || *events[0].JobID != j.ID {
		t.Fatalf("第一条事件不正确: %+v %v", events, err)
	}
	events, err = s.EventsAfter(events[0].ID, 100)
	if err != nil || len(events) != 1 || events[0].Kind != EventJobFinished {
		t.Fatalf("按游标读取时重复或漏掉事件: %+v %v", events, err)
	}
	got, err = s.GetJob(j.ID)
	if err != nil || got.Status != StatusSucceeded || got.PID != 42 || got.Notes != j.Notes {
		t.Fatalf("重试后任务未保存: %+v %v", got, err)
	}
}

func TestTransitionRejectsMissingJob(t *testing.T) {
	s := openTest(t)
	j := &Job{ID: 100, Status: StatusFailed}
	e := &Event{Kind: EventJobFinished, JobID: &j.ID}
	if err := s.Transition(j, e); !errors.Is(err, ErrNotFound) {
		t.Fatalf("不存在的任务应返回 ErrNotFound: %v", err)
	}
	events, err := s.EventsAfter(0, 100)
	if err != nil || len(events) != 0 {
		t.Fatalf("不存在的任务不应留下事件: %+v %v", events, err)
	}
}

func TestJobMetadataAndBatchRange(t *testing.T) {
	s := openTest(t)
	at := int64(100)
	first := &Job{Name: "j", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	if err := s.InsertJob(first); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetMemo(first.ID, "基线\n第二行"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetNotifyDone(first.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveJob(first); err != nil {
		t.Fatal(err)
	}
	first.Status, first.EndedAt = StatusSucceeded, &at
	finish := &Event{Kind: EventJobFinished, JobID: &first.ID, TS: at}
	drain := &Event{Kind: EventQueueDrained, TS: at}
	if err := s.Transition(first, finish, drain); err != nil {
		t.Fatal(err)
	}
	second := &Job{Name: "next", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	if err := s.InsertJob(second); err != nil {
		t.Fatal(err)
	}
	second.Status, second.EndedAt = StatusCancelled, &at
	if err := s.Transition(second, &Event{Kind: EventJobFinished, JobID: &second.ID, TS: at}, &Event{Kind: EventQueueDrained, TS: at}); err != nil {
		t.Fatal(err)
	}
	jobs, total, err := s.ListJobs(JobFilter{Query: "基线"})
	if err != nil || total != 1 || jobs[0].Memo != "基线\n第二行" || !jobs[0].NotifyDone {
		t.Fatalf("任务的可编辑字段丢失: %v, %v", jobs, err)
	}
	jobs, total, err = s.ListJobs(JobFilter{FinishedFromEvent: finish.ID, FinishedToEvent: drain.ID})
	if err != nil || total != 1 || jobs[0].ID != first.ID {
		t.Fatalf("同一秒结束的下一批混入查询结果: %v, %v", jobs, err)
	}
	var data struct {
		On bool `json:"notify_done"`
	}
	if err := json.Unmarshal(finish.Data, &data); err != nil || !data.On {
		t.Fatalf("结束事件未使用事务中的开关: %s, %v", finish.Data, err)
	}
}

func TestTransitionWithSettingAtomic(t *testing.T) {
	s := openTest(t)
	j := &Job{Name: "j", Workdir: "/w", Cmd: "x", Status: StatusRunning}
	if err := s.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	finished := &Event{Kind: EventJobFinished, JobID: &j.ID}
	paused := &Event{Kind: EventQueuePaused, Key: "queue", JobID: &j.ID}
	j.Status = StatusFailed
	if _, err := s.db.Exec(`CREATE TRIGGER reject_auto_pause BEFORE INSERT ON events
		WHEN NEW.kind='queue_paused' BEGIN SELECT RAISE(ABORT, '模拟暂停事件写入失败'); END;`); err != nil {
		t.Fatal(err)
	}
	if err := s.TransitionWithSetting(j, "queue.state", `{"paused":true}`, finished, paused); err == nil {
		t.Fatal("自动暂停事件写入失败应使任务和队列一起回滚")
	}
	if got, err := s.GetJob(j.ID); err != nil || got.Status != StatusRunning {
		t.Fatalf("失败后任务状态改变: %+v, %v", got, err)
	}
	if _, err := s.GetSetting("queue.state"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("失败后留下了队列状态: %v", err)
	}
	if events, err := s.EventsAfter(0, 100); err != nil || len(events) != 0 {
		t.Fatalf("失败后留下了事件: %+v, %v", events, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_auto_pause"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.TransitionWithSetting(j, "queue.state", `{"paused":true}`, finished, paused); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := s.GetJob(j.ID); err != nil || got.Status != StatusFailed {
		t.Fatalf("重试后任务未保存: %+v, %v", got, err)
	}
	if value, err := s.GetSetting("queue.state"); err != nil || value != `{"paused":true}` {
		t.Fatalf("重试后队列未保存: %q, %v", value, err)
	}
	if events, err := s.EventsAfter(0, 100); err != nil || len(events) != 2 || events[0].Kind != EventJobFinished || events[1].Kind != EventQueuePaused {
		t.Fatalf("重试后事件重复或缺失: %+v, %v", events, err)
	}
}

func TestTransitionRejectsInvalidEvent(t *testing.T) {
	s := openTest(t)
	j := &Job{Name: "j", Workdir: "/w", Cmd: "x", Status: StatusQueued}
	if err := s.InsertJob(j); err != nil {
		t.Fatal(err)
	}
	j.Status = StatusRunning
	other := j.ID + 1
	for _, e := range []*Event{
		nil,
		{Kind: EventJobStarted, JobID: &other},
		{Kind: EventJobStarted, JobID: &j.ID, Data: json.RawMessage(`{`)},
	} {
		if err := s.Transition(j, e); err == nil {
			t.Fatal("不合法的事件应使写入失败")
		}
		if got, err := s.GetJob(j.ID); err != nil || got.Status != StatusQueued {
			t.Fatalf("非法事件留下了状态变化: %+v %v", got, err)
		}
	}
}

func TestEventsAfterSystemEvent(t *testing.T) {
	s := openTest(t)
	if _, err := s.db.Exec(`INSERT INTO events (ts, kind, key, job_id, data, dedupe_key)
		VALUES (1, 'alert_raised', 'gpu_error', NULL, '{}', 'alert:gpu_error:1')`); err != nil {
		t.Fatal(err)
	}
	events, err := s.EventsAfter(0, 10)
	if err != nil || len(events) != 1 || events[0].JobID != nil || events[0].Kind != "alert_raised" {
		t.Fatalf("读取 job_id 为空的事件失败: %+v %v", events, err)
	}
}

func TestSettingEventAtomicAndRead(t *testing.T) {
	s := openTest(t)
	if _, err := s.GetSetting("queue.state"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("缺少运行状态时应返回 ErrNotFound: %v", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_queue_pause BEFORE INSERT ON events
		WHEN NEW.kind='queue_paused' BEGIN SELECT RAISE(ABORT, '模拟事件写入失败'); END;`); err != nil {
		t.Fatal(err)
	}
	paused := &Event{Kind: EventQueuePaused, Key: "queue", Data: json.RawMessage(`{"paused":true}`)}
	if err := s.SetSettingWithEvent("queue.state", `{"paused":true}`, paused); err == nil {
		t.Fatal("事件写入失败应使运行状态一起回滚")
	}
	if _, err := s.GetSetting("queue.state"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("事务失败后留下运行状态: %v", err)
	}
	if events, err := s.EventsAfter(0, 10); err != nil || len(events) != 0 {
		t.Fatalf("事务失败后留下事件: %+v %v", events, err)
	}
	if _, err := s.db.Exec("DROP TRIGGER reject_queue_pause"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSettingWithEvent("queue.state", `{"paused":true}`, paused); err != nil {
		t.Fatal(err)
	}
	if value, err := s.GetSetting("queue.state"); err != nil || value != `{"paused":true}` {
		t.Fatalf("GetSetting = %q, %v", value, err)
	}
	events, err := s.EventsAfter(0, 10)
	if err != nil || len(events) != 1 || events[0].Kind != EventQueuePaused || events[0].JobID != nil {
		t.Fatalf("队列系统事件读取错误: %+v %v", events, err)
	}
}

func TestNotesMigration(t *testing.T) {
	full := slices.Clone(migrations)
	withMigrations(t, full[:5])
	path := filepath.Join(t.TempDir(), "old.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		status, reason, wantReason, wantNotes string
	}{
		{StatusSucceeded, "已清理 2 个残留进程", "", "已清理 2 个残留进程"},
		{StatusCancelled, "已清理 1 个残留进程", "", "已清理 1 个残留进程"},
		{StatusFailed, "退出码 3；已清理 4 个残留进程", "退出码 3", "已清理 4 个残留进程"},
		{StatusFailed, "退出码 7", "退出码 7", ""},
	}
	for i, r := range rows {
		if _, err := s.db.Exec("INSERT INTO jobs(name,workdir,cmd,status,created_at,fail_reason) VALUES (?,'/w','x',?,0,?)",
			fmt.Sprintf("j%d", i), r.status, r.reason); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	migrations = full
	s = openAt(t, path)
	for i, r := range rows {
		j, err := s.GetJob(int64(i + 1))
		if err != nil || j.FailReason != r.wantReason || j.Notes != r.wantNotes {
			t.Fatalf("旧任务的原因和备注迁移不正确: %+v %v", j, err)
		}
	}
}
