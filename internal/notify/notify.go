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
	Enabled      bool   `json:"enabled"`
	QueueDrained bool   `json:"queue_drained"`
	LastSentAt   *int64 `json:"last_sent_at"`
	LastError    string `json:"last_error"`
}

type state struct {
	Target     string        `json:"target"`
	Instance   string        `json:"instance"`
	Cursor     int64         `json:"cursor"`
	Batch      *batch        `json:"batch,omitempty"`
	QueueBatch *batch        `json:"queue_batch,omitempty"`
	QueueSince *int64        `json:"queue_since,omitempty"`
	Summary    *queueSummary `json:"summary,omitempty"`
	Outbox     []Message     `json:"outbox,omitempty"`
	InFlight   *Message      `json:"in_flight,omitempty"`
}

func (s state) clone() state {
	s.Outbox = slices.Clone(s.Outbox)
	s.Batch, s.QueueBatch = s.Batch.clone(), s.QueueBatch.clone()
	if s.Summary != nil {
		summary := *s.Summary
		summary.Failures = slices.Clone(summary.Failures)
		s.Summary = &summary
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
	// Lang 返回推送语言（ui.notify_lang 偏好），发送时合成文案用；nil 按中文。
	Lang func() string
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
	if target == "" || !s.publisher.Config.QueueDrained {
		loaded.Batch = mergeBatches(loaded.Batch, loaded.QueueBatch)
		loaded.QueueBatch, loaded.QueueSince, loaded.Summary = nil, nil, nil
	} else if loaded.QueueSince == nil {
		cursor, err := s.st.LastEventID()
		if err != nil {
			return err
		}
		loaded.QueueSince = &cursor
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
		return s.finish(*s.completed, now)
	}
	next := s.state.clone()
	dirty, caughtUp := false, false
	// 有界读取；发送端落后时让事件留在 SQLite，避免无界内存队列。
	if len(next.Outbox) < 100 {
		// 多读一条，快速失败的结束与暂停在同一事务里，可跨分页一起消费。
		events, err := s.st.EventsAfter(next.Cursor, 101)
		if err != nil {
			return err
		}
		dirty, caughtUp = len(events) > 0, len(events) <= 100
		for i := 0; i < min(len(events), 100); i++ {
			event := events[i]
			if b := next.firstBatch(); b != nil && event.TS >= b.At+int64(s.window.Seconds()) && !next.pausesBatched(event) {
				s.flushBatch(&next)
			}
			if event.Kind == store.EventJobFinished {
				paired := i+1 < len(events) && fastPauseFor(events[i+1], event.JobID)
				item := consumeFinished(&next, event, paired)
				if paired {
					i++
					event = events[i]
					s.consume(&next, event, item)
				}
			} else {
				s.consume(&next, event, nil)
			}
			next.Cursor = event.ID
		}
	}
	if b := next.firstBatch(); caughtUp && b != nil && now.Unix() >= b.At+int64(s.window.Seconds()) {
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
	if next.Outbox[0].RetryAt > now.Unix() {
		return nil // 上次没发出去，等退避时间到
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
	s.completed = &err
	return s.finish(err, now)
}

const (
	// retryFor 是一条消息从第一次失败算起最多重试多久。跨境访问 ntfy.sh 抖动十几秒、
	// 自建服务重启几分钟都等得过去；再久的话消息已经过时，放弃并记在设置页
	retryFor = time.Hour
	retryMin = 15 * time.Second
	retryMax = 5 * time.Minute
)

// retryLater 决定发送失败的消息要不要留在发件箱里再试，返回更新过重试时间的消息。
// 重定向、4xx 这类重试也没用的，和失败超过 retryFor 的，不再重试。
// 重试沿用同一个 X-Sequence-ID：上一次其实已经送到时，手机上是替换那条通知，不会多出一条。
func retryLater(m Message, err error, now time.Time) (Message, bool) {
	var se *SendError
	if errors.As(err, &se) && se.Permanent() {
		return m, false
	}
	if m.FailingSince == 0 {
		m.FailingSince = now.Unix()
	}
	if now.Unix()-m.FailingSince >= int64(retryFor/time.Second) {
		return m, false
	}
	delay := min(retryMin<<min(m.Retries, 8), retryMax)
	if se != nil && se.RetryAfter > delay {
		delay = min(se.RetryAfter, retryFor)
	}
	m.Retries++
	m.RetryAt = now.Add(delay).Unix()
	return m, true
}

func resultValues(err error) map[string]string {
	if err != nil {
		return map[string]string{"ntfy.last_error": err.Error()}
	}
	return map[string]string{"ntfy.last_sent_at": strconv.FormatInt(time.Now().Unix(), 10), "ntfy.last_error": ""}
}

func (s *Service) finish(err error, now time.Time) error {
	next := s.state.clone()
	values := resultValues(err)
	if err != nil && next.InFlight != nil {
		// 没发出去的放回发件箱最前面，按退避时间再试；它后面的消息照原样排着
		if m, retry := retryLater(*next.InFlight, err, now); retry {
			next.Outbox = append([]Message{m}, next.Outbox...)
			values["ntfy.last_error"] = err.Error() + "，稍后重试"
			log.Printf("手机推送发送失败，%d 秒后重试: %v", m.RetryAt-now.Unix(), err)
		} else {
			log.Printf("手机推送发送失败，不再重试: %v", err)
		}
	}
	next.InFlight = nil
	if saveErr := s.save(next, values); saveErr != nil {
		return saveErr
	}
	s.completed = nil
	return nil
}

func (s *Service) Status() (Status, error) {
	status := Status{Enabled: s.publisher.Config.NTFY != "", QueueDrained: s.publisher.Config.QueueDrained}
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
	err := s.publisher.Send(ctx, Message{Title: s.hostname + " · " + s.msg("title.test", nil), Body: s.msg("test.body", nil), Click: s.baseURL + "/", Priority: 3})
	if err != nil {
		log.Printf("测试推送发送失败: %v", err)
	}
	if saveErr := s.st.SetSettings(resultValues(err)); saveErr != nil {
		return fmt.Errorf("保存测试推送结果失败: %w", saveErr)
	}
	return err
}

func (s *Service) consume(next *state, e store.Event, fastFailure *completion) {
	message := Message{ID: "danlu-" + next.Instance + "-" + strconv.FormatInt(e.ID, 10), Priority: 3, Click: s.baseURL + "/"}
	if e.JobID != nil {
		message.Click = fmt.Sprintf("%s/jobs/%d", s.baseURL, *e.JobID)
	}
	switch e.Kind {
	case store.EventQueueDrained:
		s.drain(next, e)
		return
	case store.EventQueuePaused:
		var data struct {
			Kind string `json:"kind"`
		}
		if json.Unmarshal(e.Data, &data) != nil || (data.Kind != "disk" && data.Kind != "fail_fast") {
			return
		}
		message.Title, message.Priority = s.hostname+" · "+s.msg("title.paused", nil), 4
		if data.Kind == "disk" {
			message.Body = s.msg("pause.disk", nil)
		} else if e.JobID == nil {
			return
		} else {
			if fastFailure == nil {
				// 兼容升级前游标恰好停在结束与暂停之间的待发批次。
				fastFailure = next.takeFailure(*e.JobID)
			}
			if fastFailure != nil {
				message.Body = s.msg("pause.fail_fast", map[string]any{"item": s.line(*fastFailure)})
			} else {
				message.Body = s.msg("pause.fail_fast_plain", map[string]any{"id": *e.JobID})
			}
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
			message.Title, message.Body = s.hostname+" · "+s.msg("title.gpu_recovered", nil), s.msg("gpu.recovered", nil)
		} else {
			switch data.Type {
			case "gpu_error":
				message.Title, message.Body = s.hostname+" · "+s.msg("title.gpu_error", nil), s.msg("gpu.error", nil)
			case "disk_low":
				message.Title = s.hostname + " · " + s.msg("title.disk_low", nil)
				key := "disk.low_disk"
				if data.DataDisk {
					key = "disk.low_data"
				}
				message.Body = s.msg(key, map[string]any{"free": fmt.Sprintf("%.1f", float64(data.AvailableBytes)/(1<<30))})
			case "external":
				if e.JobID == nil {
					return
				}
				message.Title, message.Body = s.hostname+" · "+s.msg("title.external", nil), s.msg("external.body", map[string]any{"id": *e.JobID, "gpus": fmt.Sprint(data.GPUs), "pid": data.PID})
				if data.OtherJob > 0 {
					message.Title = s.hostname + " · " + s.msg("title.external_job", nil)
					message.Body = s.msg("external.job_body", map[string]any{"id": *e.JobID, "gpus": fmt.Sprint(data.GPUs), "other": data.OtherJob})
				}
			case "stray":
				if e.JobID == nil || len(data.GPUs) == 0 {
					return
				}
				message.Title = s.hostname + " · " + s.msg("title.stray", nil)
				params := map[string]any{"id": *e.JobID, "gpus": joinCards(data.GPUs), "assigned": joinCards(data.Assigned)}
				if len(data.Assigned) > 0 {
					message.Body = s.msg("stray.body", params)
				} else {
					message.Body = s.msg("stray.body_none", params)
				}
				if len(data.Reserved) > 0 {
					message.Body = s.concatSentences(message.Body, s.msg("stray.reserved", map[string]any{"reserved": joinCards(data.Reserved)}))
				}
				switch {
				case data.CUDAVisible != nil && *data.CUDAVisible == "":
					message.Body = s.concatSentences(message.Body, s.msg("stray.cvd_unset", nil))
				case data.CUDAVisible != nil:
					message.Body = s.concatSentences(message.Body, s.msg("stray.cvd", map[string]any{"cvd": oneLine(*data.CUDAVisible, 100)}))
				case data.Graphics:
					message.Body = s.concatSentences(message.Body, s.msg("stray.graphics", nil))
				}
			case "strict_unavailable":
				message.Title, message.Priority = s.hostname+" · "+s.msg("title.strict_unavailable", nil), 4
				fix := "strict.fix_other"
				switch data.Reason {
				case "not_installed", "no_sudo", "protocol":
					fix = "strict.fix_" + data.Reason
				}
				message.Body = s.concatSentences(s.msg("strict.body", nil), s.msg(fix, nil))
			case "blocked", "blocked_unknown":
				if e.JobID == nil {
					return
				}
				if data.Type == "blocked_unknown" {
					message.Title, message.Body = s.hostname+" · "+s.msg("title.blocked_unknown", nil), s.msg("blocked.unknown_body", map[string]any{"id": *e.JobID})
				} else {
					message.Title, message.Body = s.hostname+" · "+s.msg("title.blocked", nil), s.msg("blocked.body", map[string]any{"id": *e.JobID})
				}
			case "silent":
				if e.JobID == nil || data.Since <= 0 {
					return
				}
				message.Title = s.hostname + " · " + s.msg("title.silent", nil)
				message.Body = s.msg("silent.body", map[string]any{"id": *e.JobID, "name": oneLine(data.Name, 200), "duration": s.humanDuration(e.TS - data.Since)})
				if data.Util != nil {
					message.Body = s.concatSentences(message.Body, s.msg("silent.util", map[string]any{"util": *data.Util}))
				}
				if data.DState != nil && *data.DState > 0 {
					message.Body = s.concatSentences(message.Body, s.msg("silent.dstate", map[string]any{"n": *data.DState}))
				}
			default:
				return
			}
		}
	case store.EventReservationReady, store.EventReservationExpiring:
		var data store.ReservationEventData
		if json.Unmarshal(e.Data, &data) != nil || data.ID <= 0 {
			return
		}
		params := map[string]any{"id": data.ID, "gpus": joinCards(data.GPUs)}
		// 有备注用 *_note 那句：两种写法的空格规则不同（中文"#3 已就绪"与"（备注）已就绪"）
		suffix := ""
		if note := oneLine(data.Note, 200); note != "" {
			params["note"], suffix = note, "_note"
		}
		if e.Kind == store.EventReservationReady {
			message.Title = s.hostname + " · " + s.msg("title.reservation_ready", nil)
			message.Body = s.msg("reservation.ready"+suffix, params)
			if data.Duration != nil {
				message.Body = s.concatSentences(message.Body, s.msg("reservation.ready_auto", map[string]any{"duration": s.roughDuration(*data.Duration)}))
			} else {
				message.Body = s.concatSentences(message.Body, s.msg("reservation.ready_manual", nil))
			}
		} else {
			// 延长了、或者推送积压到已经过了到期时间，这条提醒就不发了
			if data.EndsAt == nil || time.Now().Unix() >= *data.EndsAt {
				return
			}
			message.Title = s.hostname + " · " + s.msg("title.reservation_expiring", nil)
			params["duration"] = s.roughDuration(*data.EndsAt - e.TS)
			message.Body = s.msg("reservation.expiring"+suffix, params)
		}
	default:
		return
	}
	next.Outbox = append(next.Outbox, message)
}

func joinCards(cards []int) string {
	parts := make([]string, len(cards))
	for i, c := range cards {
		parts[i] = strconv.Itoa(c)
	}
	return strings.Join(parts, ", ")
}

// roughDuration 写成"3 小时""90 分钟"这样的整数，推送里不需要精确到秒
func (s *Service) roughDuration(seconds int64) string {
	seconds = max(0, seconds)
	switch {
	case seconds >= 3600 && seconds%3600 == 0:
		return s.msg("rough.h", map[string]any{"n": seconds / 3600})
	case seconds >= 60:
		return s.msg("rough.m", map[string]any{"n": (seconds + 30) / 60})
	default:
		return s.msg("rough.s", map[string]any{"n": seconds})
	}
}

// failureReason 按失败类别合成当前语言的原因文字；不转发日志里的私有路径等原文。
// detail 是 host_oom 三态等细分码；旧事件没有细分码时按中文原文推断。
func (s *Service) failureReason(kind, detail string, exit *int) string {
	if kind == "host_oom" {
		switch detail {
		case logs.DetailOOMWorker:
			return s.msg("fail.host_oom_worker", nil)
		case logs.DetailOOMGuess137:
			return s.msg("fail.host_oom_guess", nil)
		default:
			return s.msg("fail.host_oom", nil)
		}
	}
	if key := map[string]string{"cuda_oom": "fail.cuda_oom", "nccl": "fail.nccl", "exception": "fail.exception",
		"reboot": "fail.reboot", "killed": "fail.killed", "start": "fail.start", "signal": "fail.signal"}[kind]; key != "" {
		return s.msg(key, nil)
	}
	if exit != nil {
		return s.msg("fail.exit", map[string]any{"code": *exit})
	}
	return s.msg("fail.unknown", nil)
}

func (s *Service) humanDuration(seconds int64) string {
	seconds = max(0, seconds)
	switch {
	case seconds >= 3600:
		return s.msg("duration.hm", map[string]any{"h": seconds / 3600, "m": seconds % 3600 / 60})
	case seconds >= 60:
		return s.msg("duration.m", map[string]any{"n": seconds / 60})
	default:
		return s.msg("duration.s", map[string]any{"n": seconds})
	}
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
