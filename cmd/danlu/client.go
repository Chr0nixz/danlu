package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"runtime"
	"strings"
	"time"

	"danlu/internal/config"
	"danlu/internal/localsock"
)

// apiClient 是本机命令行入口上的 HTTP 客户端（FEATURES 5.1）：经数据目录里的 unix socket
// 调用正在运行的 danlu 的 /api，请求和响应格式与 /api 完全相同。不直接读写数据库。
type apiClient struct {
	sock string
	http *http.Client
}

const requestTimeout = 30 * time.Second

func newAPIClient() *apiClient { return newAPIClientAt(localsock.Path(config.Home())) }

func newAPIClientAt(sock string) *apiClient {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", sock)
		},
		MaxIdleConns:    2,
		IdleConnTimeout: 30 * time.Second,
	}
	return &apiClient{sock: sock, http: &http.Client{Transport: tr}}
}

// rawResponse 是一次请求的原始结果：状态码和响应体。
type rawResponse struct {
	status int
	body   []byte
}

// open 发请求并返回响应，调用方负责关闭 Body（日志流用它）。连不上时返回 exitUnreachable 的错误。
func (c *apiClient) open(ctx context.Context, method, path string, body []byte) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		rd = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://danlu"+path, rd)
	if err != nil {
		return nil, err
	}
	if method != http.MethodGet && method != http.MethodHead {
		// 写操作必须是 JSON（服务端的 CSRF 规则对 socket 也一样）
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, c.unreachable(err)
	}
	return resp, nil
}

func (c *apiClient) unreachable(err error) error {
	if runtime.GOOS != "linux" {
		return &cliError{exit: exitUnreachable, code: "cli_unsupported",
			msg: "任务命令只在 Linux 服务器上可用：它们经数据目录里的 danlu.sock 连接本机正在运行的 danlu"}
	}
	cause := err
	if u := errors.Unwrap(err); u != nil {
		cause = u // 去掉 url.Error 里重复的请求地址
	}
	return &cliError{exit: exitUnreachable, code: "cli_unreachable",
		msg: fmt.Sprintf("连不上 danlu 服务（%s）：服务没有运行，或者 DANLU_HOME 指向了别的数据目录。用 danlu doctor 检查（%v）", c.sock, cause)}
}

// maxResponse 是 raw 读响应体的上限；JSON 接口远到不了，超过就报错，不悄悄截断。
const maxResponse = 64 << 20

// raw 发一次请求并读完响应体，最多等 requestTimeout。服务在这段时间里一直不响应（比如刚启动、
// 还在接管任务），按连不上处理，wait 和 tail -f 会接着重试；调用方自己的 ctx 到期则原样返回。
func (c *apiClient) raw(ctx context.Context, method, path string, body []byte) (rawResponse, error) {
	rctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	resp, err := c.open(rctx, method, path, body)
	if err == nil {
		defer resp.Body.Close()
		var b []byte
		if b, err = io.ReadAll(io.LimitReader(resp.Body, maxResponse+1)); err == nil {
			if len(b) > maxResponse {
				return rawResponse{}, fmt.Errorf("响应超过 %d MiB", maxResponse>>20)
			}
			return rawResponse{status: resp.StatusCode, body: b}, nil
		}
	}
	if ctx.Err() != nil {
		return rawResponse{}, ctx.Err()
	}
	var ce *cliError
	if errors.As(err, &ce) {
		return rawResponse{}, err
	}
	if rctx.Err() != nil {
		return rawResponse{}, &cliError{exit: exitUnreachable, code: "cli_unreachable",
			msg: fmt.Sprintf("danlu 服务 %d 秒没有响应（%s）", int(requestTimeout.Seconds()), c.sock)}
	}
	return rawResponse{}, c.unreachable(err)
}

// do 发请求，2xx 时把响应体解码进 out（out 为 nil 时丢弃），返回状态码；
// 4xx/5xx 转成带退出码和机器码的错误（apiError）。
func (c *apiClient) do(ctx context.Context, method, path string, in, out any) (int, error) {
	var body []byte
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return 0, err
		}
		body = b
	} else if method != http.MethodGet {
		body = []byte("{}")
	}
	r, err := c.raw(ctx, method, path, body)
	if err != nil {
		return 0, err
	}
	if r.status >= 400 {
		return r.status, apiError(r)
	}
	if out != nil {
		if err := json.Unmarshal(r.body, out); err != nil {
			return r.status, fmt.Errorf("服务端响应不是预期的 JSON: %w", err)
		}
	}
	return r.status, nil
}

// apiError 把错误响应转成 cliError：码沿用服务端的；任务不存在退出码 4，身份不符 3，
// 接口不存在多半是服务端还是旧版本。
func apiError(r rawResponse) error {
	var b struct {
		Error  string         `json:"error"`
		Code   string         `json:"code"`
		Params map[string]any `json:"params"`
	}
	if json.Unmarshal(r.body, &b) != nil || b.Error == "" {
		b.Error = strings.TrimSpace(string(r.body))
		if b.Error == "" {
			b.Error = http.StatusText(r.status)
		}
	}
	e := &cliError{exit: exitErr, code: b.Code, msg: b.Error, params: b.Params}
	switch {
	case b.Code == "job_not_found":
		e.exit = exitNotFound
	case b.Code == "socket_uid_mismatch":
		e.exit = exitUnreachable
	case b.Code == "not_found":
		e.code = "cli_server_old"
		e.msg = "正在运行的 danlu 没有这个接口，多半还是旧版本：程序已经换成新版就执行 danlu restart，否则先升级服务"
	case e.code == "":
		e.code = "server_error"
	}
	return e
}

// withRestartRetry 在连不上服务时重试最多 60 秒：danlu restart、升级时 socket 会消失几秒，
// wait 和 tail -f 不该因此失败。其他错误立即返回。
func withRestartRetry(ctx context.Context, fn func() error) error {
	deadline := time.Now().Add(60 * time.Second)
	for {
		err := fn()
		var ce *cliError
		if err == nil || !errors.As(err, &ce) || ce.code != "cli_unreachable" || time.Now().After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}
