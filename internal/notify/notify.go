package notify

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"danlu/internal/config"
	"danlu/internal/logs"
	"danlu/internal/store"
)

const stateKey = "ntfy.state"

type StateStore interface {
	GetSetting(string) (string, error)
	SetSettings(map[string]string) error
	EventsAfter(int64, int) ([]store.Event, error)
	LastEventID() (int64, error)
}

type Status struct {
	Enabled    bool   `json:"enabled"`
	LastSentAt *int64 `json:"last_sent_at"`
	LastError  string `json:"last_error"`
}

type failure struct {
	ID     int64  `json:"id"`
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

type batch struct {
	At      int64     `json:"at"`
	FirstID int64     `json:"first_id"`
	Count   int       `json:"count"`
	Items   []failure `json:"items"`
}

type state struct {
	Target   string    `json:"target"`
	Instance string    `json:"instance"`
	Cursor   int64     `json:"cursor"`
	Batch    *batch    `json:"batch,omitempty"`
	Outbox   []Message `json:"outbox,omitempty"`
	InFlight *Message  `json:"in_flight,omitempty"`
}

func (s state) clone() state {
	s.Outbox = slices.Clone(s.Outbox)
	if s.Batch != nil {
		b := *s.Batch
		b.Items = slices.Clone(b.Items)
		s.Batch = &b
	}
	return s
}

type Service struct {
	st        StateStore
	publisher Publisher
	hostname  string
	baseURL   string
	state     state // 只有 Run 写；状态接口从数据库读，不与发送争锁。
	ready     bool
	completed *error
	testMu    sync.Mutex
	window    time.Duration
	interval  time.Duration
}

func New(st StateStore, cfg config.Notify, hostname, baseURL string) *Service {
	return &Service{st: st, publisher: Publisher{Config: cfg}, hostname: oneLine(hostname, 150),
		baseURL: strings.TrimRight(baseURL, "/"), window: 30 * time.Second, interval: time.Second}
}

// Initialize 在任务恢复之前调用；第一次启用或切换 topic 时从当前游标开始，不补发历史。
func (s *Service) Initialize() error {
	raw, err := s.st.GetSetting(stateKey)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}
	var loaded state
	recoveryWarning := ""
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &loaded); err != nil {
			raw = ""
			recoveryWarning = "通知状态损坏，已从当前事件继续，未补发历史通知"
		}
	}
	target := ""
	if s.publisher.Config.NTFY != "" {
		sum := sha256.Sum256([]byte(s.publisher.Config.NTFY))
		target = hex.EncodeToString(sum[:])
	}
	values := map[string]string{}
	if raw == "" || loaded.Target != target || target == "" {
		cursor, err := s.st.LastEventID()
		if err != nil {
			return err
		}
		loaded = state{Target: target, Cursor: cursor, Instance: rand.Text()}
		values["ntfy.last_error"] = recoveryWarning
	} else if loaded.InFlight != nil {
		// HTTP 和本地事务无法原子提交。结果未知时不重发，避免重启重复响铃。
		loaded.InFlight = nil
		values["ntfy.last_error"] = "上次推送在发送中被中断，送达结果未知，已跳过重发"
	}
	if err := s.save(loaded, values); err != nil {
		return err
	}
	s.ready = true
	return nil
}

func (s *Service) save(next state, values map[string]string) error {
	data, err := json.Marshal(next)
	if err != nil {
		return err
	}
	if values == nil {
		values = map[string]string{}
	}
	values[stateKey], values["ntfy.cursor"] = string(data), strconv.FormatInt(next.Cursor, 10)
	if err := s.st.SetSettings(values); err != nil {
		return err
	}
	s.state = next
	return nil
}

func (s *Service) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	var lastError string
	for {
		if ctx.Err() != nil {
			return
		}
		var err error
		if !s.ready {
			err = s.Initialize()
		}
		if err == nil && s.publisher.Config.NTFY == "" {
			return
		}
		if err == nil {
			err = s.step(ctx, time.Now())
		}
		if err != nil && ctx.Err() == nil && lastError != err.Error() {
			log.Printf("手机推送暂不可用: %v", err)
			_ = s.st.SetSettings(map[string]string{"ntfy.last_error": "通知状态读写失败，正在重试"})
			lastError = err.Error()
		} else if err == nil {
			lastError = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Service) step(ctx context.Context, now time.Time) error {
	if s.completed != nil {
		return s.finish(*s.completed)
	}
	next := s.state.clone()
	dirty, caughtUp := false, false
	// 有界读取；发送端落后时让事件留在 SQLite，避免无界内存队列。
	if len(next.Outbox) < 100 {
		events, err := s.st.EventsAfter(next.Cursor, 100)
		if err != nil {
			return err
		}
		dirty, caughtUp = len(events) > 0, len(events) < 100
		for _, event := range events {
			// 快速失败暂停紧跟在那次失败之后写入；即使跨过窗口边界，也先和批次里的这次失败合并。
			if next.Batch != nil && event.TS >= next.Batch.At+int64(s.window.Seconds()) && !pausesBatched(next.Batch, event) {
				s.flushBatch(&next)
			}
			s.consume(&next, event)
			next.Cursor = event.ID
		}
	}
	if caughtUp && next.Batch != nil && now.Unix() >= next.Batch.At+int64(s.window.Seconds()) {
		s.flushBatch(&next)
		dirty = true
	}
	if dirty {
		if err := s.save(next, nil); err != nil {
			return err
		}
	}
	if len(next.Outbox) == 0 {
		return nil
	}
	message := next.Outbox[0]
	next.Outbox, next.InFlight = slices.Clone(next.Outbox[1:]), &message
	// 先持久化发送认领；若回执或收尾写库丢失，重启不会再次发送。
	if err := s.save(next, nil); err != nil {
		return err
	}
	err := s.publisher.Send(ctx, message)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		log.Printf("手机推送发送失败（已重试 3 次）: %v", err)
	}
	s.completed = &err
	return s.finish(err)
}

func resultValues(err error) map[string]string {
	if err != nil {
		return map[string]string{"ntfy.last_error": err.Error()}
	}
	return map[string]string{"ntfy.last_sent_at": strconv.FormatInt(time.Now().Unix(), 10), "ntfy.last_error": ""}
}

func (s *Service) finish(err error) error {
	next := s.state.clone()
	next.InFlight = nil
	if saveErr := s.save(next, resultValues(err)); saveErr != nil {
		return saveErr
	}
	s.completed = nil
	return nil
}

func (s *Service) Status() (Status, error) {
	status := Status{Enabled: s.publisher.Config.NTFY != ""}
	last, err := s.st.GetSetting("ntfy.last_sent_at")
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return status, err
	}
	if last != "" {
		ts, err := strconv.ParseInt(last, 10, 64)
		if err != nil {
			return status, errors.New("推送发送时间无效")
		}
		status.LastSentAt = &ts
	}
	status.LastError, err = s.st.GetSetting("ntfy.last_error")
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return status, err
}

// Test 独立于事件消费，不移动游标，也不等待后台通知发送。
func (s *Service) Test(ctx context.Context) error {
	if s.publisher.Config.NTFY == "" {
		return ErrDisabled
	}
	if !s.testMu.TryLock() {
		return ErrBusy
	}
	defer s.testMu.Unlock()
	err := s.publisher.Send(ctx, Message{Title: s.hostname + " · 测试推送", Body: "丹炉手机推送已连通。", Click: s.baseURL + "/", Priority: 3})
	if err != nil {
		log.Printf("测试推送发送失败: %v", err)
	}
	if saveErr := s.st.SetSettings(resultValues(err)); saveErr != nil {
		return fmt.Errorf("保存测试推送结果失败: %w", saveErr)
	}
	return err
}

func (s *Service) consume(next *state, e store.Event) {
	if e.Kind == store.EventJobFinished {
		var data struct {
			Status   string `json:"status"`
			Name     string `json:"name"`
			FailKind string `json:"fail_kind"`
			ExitCode *int   `json:"exit_code"`
			Reason   string `json:"reason"`
		}
		if json.Unmarshal(e.Data, &data) != nil || data.Status != store.StatusFailed || e.JobID == nil {
			return
		}
		if next.Batch == nil {
			next.Batch = &batch{At: e.TS, FirstID: e.ID}
		}
		next.Batch.Count++
		if len(next.Batch.Items) < 8 {
			reason := failureReason(data.FailKind, data.ExitCode)
			// 原因文字只用来和固定文案比较，不转发：只凭退出码推测的内存不足要说"可能"。
			if data.FailKind == "host_oom" && data.Reason == logs.ReasonMaybeHostOOM {
				reason = "可能是主机内存不足"
			}
			next.Batch.Items = append(next.Batch.Items, failure{ID: *e.JobID, Name: oneLine(data.Name, 200), Reason: reason})
		}
		return
	}
	message := Message{ID: "danlu-" + next.Instance + "-" + strconv.FormatInt(e.ID, 10), Priority: 3, Click: s.baseURL + "/"}
	if e.JobID != nil {
		message.Click = fmt.Sprintf("%s/jobs/%d", s.baseURL, *e.JobID)
	}
	switch e.Kind {
	case store.EventQueuePaused:
		var data struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(e.Data, &data) != nil || (data.Kind != "disk" && data.Kind != "fail_fast") {
			return
		}
		message.Title, message.Priority = s.hostname+" · 队列暂停", 4
		if data.Kind == "disk" {
			message.Body = "数据分区空间不足，已暂停启动新任务。"
		} else if e.JobID == nil {
			return
		} else if item, ok := takeFailure(next, *e.JobID); ok {
			// 失败原因随暂停一起发出，这次失败不再进入合并批次，手机只响一次。
			message.Body = fmt.Sprintf("#%d %s：%s。任务启动后很快失败，已暂停启动后续任务，请检查后继续队列。", item.ID, item.Name, item.Reason)
		} else {
			message.Body = fmt.Sprintf("任务 #%d 快速失败，已暂停启动后续任务，请检查后继续队列。", *e.JobID)
		}
	case store.EventAlertRaised, store.EventAlertCleared:
		var data store.AlertData
		if json.Unmarshal(e.Data, &data) != nil {
			return
		}
		if e.Kind == store.EventAlertCleared {
			if data.Type != "gpu_error" {
				return
			}
			message.Title, message.Body = s.hostname+" · GPU 已恢复", "nvidia-smi 已恢复，GPU 数据可以正常读取。"
		} else {
			switch data.Type {
			case "gpu_error":
				message.Title, message.Body = s.hostname+" · GPU 数据不可用", "nvidia-smi 已连续出错超过 60 秒，请检查 GPU 或驱动。"
			case "disk_low":
				message.Title = s.hostname + " · 磁盘余量不足"
				message.Body = fmt.Sprintf("监测到的%s分区仅剩 %.1f GiB 可用空间。", map[bool]string{true: "数据", false: "磁盘"}[data.DataDisk], float64(data.AvailableBytes)/(1<<30))
			case "external":
				if e.JobID == nil {
					return
				}
				message.Title, message.Body = s.hostname+" · 训练 GPU 被外部占用", fmt.Sprintf("任务 #%d 使用的 GPU %v 出现外部进程 PID %d。", *e.JobID, data.GPUs, data.PID)
			case "blocked":
				if e.JobID == nil {
					return
				}
				message.Title, message.Body = s.hostname+" · 队首等待外部进程", fmt.Sprintf("任务 #%d 正在等待外部进程释放 GPU。", *e.JobID)
			default:
				return
			}
		}
	default:
		return
	}
	next.Outbox = append(next.Outbox, message)
}

// pausesBatched 判断事件是否为批次中某次失败触发的快速失败暂停。
func pausesBatched(b *batch, e store.Event) bool {
	if e.Kind != store.EventQueuePaused || e.JobID == nil {
		return false
	}
	var data struct {
		Kind string `json:"kind"`
	}
	if json.Unmarshal(e.Data, &data) != nil || data.Kind != "fail_fast" {
		return false
	}
	return slices.ContainsFunc(b.Items, func(f failure) bool { return f.ID == *e.JobID })
}

// takeFailure 把任务从待发的失败批次中取出；批次空了就不再发送。
func takeFailure(next *state, id int64) (failure, bool) {
	b := next.Batch
	if b == nil {
		return failure{}, false
	}
	i := slices.IndexFunc(b.Items, func(f failure) bool { return f.ID == id })
	if i < 0 {
		return failure{}, false
	}
	item := b.Items[i]
	b.Items = slices.Delete(b.Items, i, i+1)
	b.Count--
	if b.Count == 0 {
		next.Batch = nil
	}
	return item, true
}

func (s *Service) flushBatch(next *state) {
	b := next.Batch
	var lines []string
	for _, item := range b.Items {
		lines = append(lines, fmt.Sprintf("#%d %s：%s", item.ID, item.Name, item.Reason))
	}
	if b.Count > len(b.Items) {
		lines = append(lines, fmt.Sprintf("另有 %d 个任务失败，请打开面板查看。", b.Count-len(b.Items)))
	}
	click := s.baseURL + "/jobs?status=failed"
	if b.Count == 1 {
		click = fmt.Sprintf("%s/jobs/%d", s.baseURL, b.Items[0].ID)
	}
	next.Outbox = append(next.Outbox, Message{ID: "danlu-" + next.Instance + "-" + strconv.FormatInt(b.FirstID, 10),
		Title: s.hostname + " · 任务失败", Body: strings.Join(lines, "\n"), Click: click, Priority: 3})
	next.Batch = nil
}

func failureReason(kind string, exit *int) string {
	if reason := map[string]string{"cuda_oom": "CUDA 显存不足", "host_oom": "主机内存不足", "nccl": "NCCL 通信超时或出错",
		"exception": "Python 异常", "reboot": "服务器重启，任务中断", "killed": "进程被外部终止", "start": "任务启动失败", "signal": "进程被信号终止"}[kind]; reason != "" {
		return reason
	}
	if exit != nil {
		return fmt.Sprintf("退出码 %d", *exit)
	}
	return "任务异常结束"
}

func oneLine(value string, maxBytes int) string {
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, value)
	if len(value) > maxBytes {
		value = value[:maxBytes]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return strings.TrimSpace(value)
}
