package api

import (
	"errors"
	"net/http"

	"danlu/internal/auth"
	"danlu/internal/notify"
	"danlu/internal/scheduler"
	"danlu/internal/settings"
	"danlu/internal/store"
	"danlu/internal/terminal"
	jobtrace "danlu/internal/trace"
)

// writeErrC 带机器码的错误响应：前端按 code 本地化，error 仍是中文原文（旧客户端回退用）。
// params 是本地化插值参数，没有时不输出这个字段。
func writeErrC(w http.ResponseWriter, status int, code, msg string, params map[string]any) {
	body := map[string]any{"error": msg, "code": code}
	if len(params) > 0 {
		body["params"] = params
	}
	writeJSON(w, status, body)
}

// sentinelCodes 是哨兵错误到机器码的映射：错误自己不带码时按 errors.Is 查这张表。
// 目录里 error.<code> 的中文必须与哨兵错误原文一致（catalog_test 核对）。
var sentinelCodes = []struct {
	err  error
	code string
}{
	{auth.ErrBadCredentials, "login_bad_credentials"},
	{auth.ErrLoginBusy, "login_busy"},
	{auth.ErrNeedTOTP, "login_need_totp"},
	{auth.ErrBadTOTP, "login_bad_totp"},
	{notify.ErrDisabled, "notify_disabled"},
	{notify.ErrBusy, "notify_busy"},
	{store.ErrPruning, "job_star_pruning"},
	{terminal.ErrNotFound, "terminal_not_found"},
	{terminal.ErrLimit, "terminal_limit"},
	{terminal.ErrReplaced, "terminal_replaced"},
	{terminal.ErrClosed, "terminal_closed"},
	{terminal.ErrUnsupported, "terminal_unsupported"},
	{scheduler.ErrReservationEnded, "reservation_ended"},
	{scheduler.ErrNotifyFinished, "job_notify_finished"},
	{jobtrace.ErrPathRequired, "trace_path_required"},
	{jobtrace.ErrPathTooLong, "trace_path_too_long"},
	{jobtrace.ErrPathNotAbs, "trace_path_not_abs"},
}

// writeErrE 从错误里提取码：scheduler.ValidationError / settings.InputError 自带的码优先，
// 其次按哨兵错误映射；都没有就退回纯文本（和 writeErr 一样，不带 code 字段）。
func writeErrE(w http.ResponseWriter, status int, err error) {
	var ve *scheduler.ValidationError
	if errors.As(err, &ve) && ve.Code != "" {
		writeErrC(w, status, ve.Code, ve.Msg, ve.Params)
		return
	}
	var ie *settings.InputError
	if errors.As(err, &ie) && ie.Code() != "" {
		writeErrC(w, status, ie.Code(), ie.Error(), ie.Params())
		return
	}
	for _, sc := range sentinelCodes {
		if errors.Is(err, sc.err) {
			writeErrC(w, status, sc.code, err.Error(), nil)
			return
		}
	}
	writeErr(w, status, err.Error())
}
