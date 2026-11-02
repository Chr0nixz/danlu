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
	var last error
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
				last = errors.New("连接 ntfy 超时")
			} else {
				last = errors.New("连接 ntfy 失败，请检查网络或证书")
			}
		} else {
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 8192))
			resp.Body.Close()
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil
			}
			// 服务端的响应体可能回显 token、topic 等信息，不保存也不转发。
			last = fmt.Errorf("ntfy 返回 HTTP %d", resp.StatusCode)
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
