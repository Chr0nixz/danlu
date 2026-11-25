// Package notify 从持久化事件生成手机通知；网络发送不参与任务调度。
package notify

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"danlu/internal/config"
)

var (
	ErrDisabled = errors.New("手机推送尚未开启，请在服务器上运行 danlu notify setup")
	ErrBusy     = errors.New("测试推送正在发送，请稍后再试")
)

type Message struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Click    string `json:"click,omitempty"`
	Priority int    `json:"priority"`
	// 发送失败后留在发件箱里重试（见 retryLater）：已经重试的次数、下一次的时间、第一次失败的时间（Unix 秒）
	Retries      int   `json:"retries,omitempty"`
	RetryAt      int64 `json:"retry_at,omitempty"`
	FailingSince int64 `json:"failing_since,omitempty"`
}

// SendError 是一次发送（含几次快速重试）最后的失败。错误文字不带服务端的响应体：里面可能回显 topic 和令牌。
type SendError struct {
	msg        string
	Status     int           // HTTP 状态码；连不上时为 0
	RetryAfter time.Duration // 429 响应的 Retry-After
}

func (e *SendError) Error() string { return e.msg }

// Permanent 表示重试也没用：重定向和 4xx（408、429 除外）说明地址、令牌或消息本身有问题。
func (e *SendError) Permanent() bool {
	return e.Status >= 300 && e.Status < 500 && e.Status != http.StatusRequestTimeout && e.Status != http.StatusTooManyRequests
}

// retryAfter 解析 Retry-After：秒数或 HTTP 日期。
func retryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

type Publisher struct {
	Config config.Notify
	Client *http.Client
	// 默认首次发送后重试三次。测试可缩短等待，生产不改变次数。
	retryDelay time.Duration
}

func (p *Publisher) Send(ctx context.Context, message Message) error {
	if p.Config.NTFY == "" {
		return ErrDisabled
	}
	if err := p.Config.Validate(); err != nil {
		return err
	}
	client := http.Client{Timeout: 5 * time.Second}
	if p.Client != nil {
		client = *p.Client
	}
	// 不能让 topic 或鉴权令牌跟随重定向去另一个地址。
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	if message.ID == "" {
		message.ID = "danlu-" + rand.Text()
	}
	delay := p.retryDelay
	if delay == 0 {
		delay = 500 * time.Millisecond
	}
	var last *SendError
	for attempt := range 4 {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Config.NTFY, strings.NewReader(message.Body))
		if err != nil {
			return errors.New("无法创建 ntfy 请求")
		}
		req.Header.Set("Content-Type", "text/plain; charset=utf-8")
		req.Header.Set("Title", mime.BEncoding.Encode("UTF-8", message.Title))
		req.Header.Set("Priority", fmt.Sprint(message.Priority))
		req.Header.Set("X-Sequence-ID", message.ID)
		if message.Click != "" {
			req.Header.Set("Click", message.Click)
		}
		if p.Config.Token != "" {
			req.Header.Set("Authorization", "Bearer "+p.Config.Token)
		}
		resp, err := client.Do(req)
		if err != nil {
			var timeout net.Error
			if errors.As(err, &timeout) && timeout.Timeout() {
				last = &SendError{msg: "连接 ntfy 超时"}
			} else {
				last = &SendError{msg: "连接 ntfy 失败，请检查网络或证书"}
			}
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			// 服务端的响应体可能回显 token、topic 等信息，不保存也不转发。
			last = &SendError{msg: fmt.Sprintf("ntfy 返回 HTTP %d", resp.StatusCode), Status: resp.StatusCode,
				RetryAfter: retryAfter(resp.Header.Get("Retry-After"), time.Now())}
			if last.Permanent() || resp.StatusCode == http.StatusTooManyRequests {
				return last // 重试也没用；限流时马上再发只会更久，按 Retry-After 留给发件箱
			}
		}
		if attempt < 3 {
			timer := time.NewTimer(delay << attempt)
			select {
			case <-ctx.Done():
				timer.Stop()
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return last
}
