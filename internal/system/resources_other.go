//go:build !linux

package system

import (
	"context"
	"errors"
)

type ProcResourceSource struct{}

func NewResourceSource(string, uint64, func() GPUStats) ResourceSource { return ProcResourceSource{} }

func (ProcResourceSource) Query(context.Context) (ResourceSnapshot, error) {
	return ResourceSnapshot{}, errors.New("服务器资源详情仅支持 Linux")
}
