//go:build !linux

package system

import (
	"context"
	"errors"
)

type ProcSource struct{}

func NewSource() Source { return ProcSource{} }

func (ProcSource) Query(context.Context) (HostSample, error) {
	return HostSample{}, errors.New("系统资源监控仅支持 Linux")
}
