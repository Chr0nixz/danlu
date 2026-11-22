// Package settings 是面板上能改、保存后立即生效的设置：调度与告警、数据保留、终端保留时长，
// 以及手机和电脑共用的面板偏好。
//
// 值存在数据库的 settings 表里，服务进程从不写 config.toml。config.toml 里的同名项只是初始值：
// 库里还没有某一项时才用它。安全设置（入口、登录有效期、失败锁定）在 internal/auth 里，做法相同。
package settings

import (
	"errors"
	"fmt"
	"time"
)

// InputError 是用户填的值不合要求，接口据此返回 400 而不是 500。
// code/params 供前端本地化；msg 仍是中文原文，旧客户端直接显示它。
type InputError struct {
	msg    string
	code   string
	params map[string]any
}

func (e *InputError) Error() string { return e.msg }

// Invalid 返回一个不带机器码的 InputError。
func Invalid(msg string) error { return &InputError{msg: msg} }

// InvalidC 返回带机器码的 InputError；没有插值参数时 params 传 nil。
func InvalidC(code, msg string, params map[string]any) error {
	return &InputError{msg: msg, code: code, params: params}
}

// Code 是机器码；没给码的旧调用点返回空字符串。
func (e *InputError) Code() string { return e.code }

// Params 是本地化用的插值参数，可能为 nil。
func (e *InputError) Params() map[string]any { return e.params }

func IsInputError(err error) bool {
	var ie *InputError
	return errors.As(err, &ie)
}

// FormatDuration 把时长写成"7 天""12 小时""90 分钟""30 秒"，用于操作记录。
func FormatDuration(d time.Duration) string {
	switch {
	case d >= 24*time.Hour && d%(24*time.Hour) == 0:
		return fmt.Sprintf("%d 天", d/(24*time.Hour))
	case d >= time.Hour && d%time.Hour == 0:
		return fmt.Sprintf("%d 小时", d/time.Hour)
	case d >= time.Minute && d%time.Minute == 0:
		return fmt.Sprintf("%d 分钟", d/time.Minute)
	case d >= time.Second && d%time.Second == 0:
		return fmt.Sprintf("%d 秒", d/time.Second)
	default:
		return d.String()
	}
}
