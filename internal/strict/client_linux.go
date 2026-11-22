//go:build linux

package strict

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"danlu/internal/execx"
)

// Installed 只看助手文件在不在，不调 sudo：设置页据此显示"未安装"和安装命令。
func Installed() bool {
	_, err := os.Stat(HelperPath)
	return err == nil
}

func sudoPath() string {
	for _, p := range []string{"/usr/bin/sudo", "/bin/sudo"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return "sudo"
}

// call 以 sudo -n 调助手，把 stdout 上的 JSON 解到 v。助手什么都没说就失败时，多半是 sudo 拒绝了。
func call(ctx context.Context, sub string, stdin []byte, v any) error {
	if !Installed() {
		return &Error{ReasonNotInstalled, "没有 " + HelperPath}
	}
	cmd := execx.Command(ctx, sudoPath(), "-n", HelperPath, sub)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	err := cmd.Run()
	execx.KillGroup(cmd)
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if len(out) == 0 {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" && err != nil {
			detail = err.Error()
		}
		if strings.Contains(detail, "sudo") {
			return &Error{ReasonNoSudo, clip([]byte(detail))}
		}
		return &Error{ReasonFailed, clip([]byte(detail))}
	}
	if jerr := json.Unmarshal(out, v); jerr != nil {
		return &Error{ReasonFailed, "助手的回答无法解析：" + clip(out)}
	}
	return nil
}

// Check 以 sudo -n 跑一次助手的 check。
func Check(ctx context.Context) Status {
	now := time.Now().Unix()
	var res CheckResult
	if err := call(ctx, "check", nil, &res); err != nil {
		reason, detail := reasonOf(err)
		return Status{Reason: reason, Detail: detail, CheckedAt: now}
	}
	st := Status{HelperVersion: res.Version, CheckedAt: now}
	switch {
	case res.Protocol != Protocol:
		st.Reason, st.Detail = ReasonProtocol, fmt.Sprintf("助手协议 %d，danlu 需要 %d", res.Protocol, Protocol)
	case res.Error != "":
		st.Reason, st.Detail = ReasonFailed, res.Error
	case res.Systemd > 0 && res.Systemd < 240:
		st.Reason, st.Detail = ReasonFailed, fmt.Sprintf("systemd %d 太旧，至少要 240", res.Systemd)
	default:
		st.OK = true
	}
	return st
}

// Start 请助手把任务作为临时服务启动。
func Start(ctx context.Context, req StartRequest) (StartResult, error) {
	req.V = Protocol
	b, err := json.Marshal(req)
	if err != nil {
		return StartResult{}, err
	}
	var res StartResult
	if err := call(ctx, "start", b, &res); err != nil {
		return res, err
	}
	if res.Error != "" {
		reason := ReasonFailed
		if res.Code == ReasonProtocol {
			reason = ReasonProtocol
		}
		return res, &Error{reason, res.Error}
	}
	if res.Unit == "" {
		return res, &Error{ReasonFailed, "助手没有返回单元名"}
	}
	return res, nil
}
