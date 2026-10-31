// Package system 采集首页需要的主机 CPU 和内存使用情况。
package system

import (
	"context"
	"time"

	"danlu/internal/sample"
)

type HostSample struct {
	At               int64  `json:"at"`
	CPUPercent       *int   `json:"cpu_percent"`
	MemoryUsedBytes  uint64 `json:"memory_used_bytes"`
	MemoryTotalBytes uint64 `json:"memory_total_bytes"`
	MemoryPercent    int    `json:"memory_percent"`
}

const HistorySize = 31 // 每 2 秒采样一次，保留约 1 分钟

type Source interface {
	Query(context.Context) (HostSample, error)
}

// Monitor 的值是最近约 1 分钟的采样窗口。
type Monitor = sample.Sampler[[]HostSample]

func NewMonitor(src Source) *Monitor {
	// 只有采样循环一个 goroutine 调用 Query，history 不用加锁
	var history []HostSample
	query := func(ctx context.Context) ([]HostSample, error) {
		s, err := src.Query(ctx)
		if err == nil {
			history = append(history, s)
			if len(history) > HistorySize {
				history = append([]HostSample(nil), history[len(history)-HistorySize:]...)
			}
		}
		return cloneSamples(history), err
	}
	return &Monitor{Name: "主机资源数据", Every: 2 * time.Second, Stale: 20 * time.Second, Query: query, Clone: cloneSamples}
}

func cloneSamples(in []HostSample) []HostSample {
	out := append([]HostSample(nil), in...)
	for i := range out {
		if out[i].CPUPercent != nil {
			v := *out[i].CPUPercent
			out[i].CPUPercent = &v
		}
	}
	return out
}
