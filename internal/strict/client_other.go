//go:build !linux

package strict

import (
	"context"
	"time"
)

func Installed() bool { return false }

func Check(context.Context) Status {
	return Status{Reason: ReasonUnsupported, Detail: "严格限卡只支持 Linux", CheckedAt: time.Now().Unix()}
}

func Start(context.Context, StartRequest) (StartResult, error) {
	return StartResult{}, &Error{ReasonUnsupported, "严格限卡只支持 Linux"}
}
