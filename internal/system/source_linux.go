//go:build linux

package system

import (
	"context"
	"math"
	"os"
	"time"
)

type ProcSource struct{ previous *cpuTimes }

func NewSource() Source { return &ProcSource{} }

func (s *ProcSource) Query(ctx context.Context) (HostSample, error) {
	if err := ctx.Err(); err != nil {
		return HostSample{}, err
	}
	cpuData, err := os.ReadFile("/proc/stat")
	if err != nil {
		return HostSample{}, err
	}
	current, err := parseCPUStat(string(cpuData))
	if err != nil {
		return HostSample{}, err
	}
	memData, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return HostSample{}, err
	}
	total, available, err := parseMemInfo(string(memData))
	if err != nil {
		return HostSample{}, err
	}

	var cpu *int
	if previous := s.previous; previous != nil && current.total > previous.total && current.idle >= previous.idle {
		deltaTotal := current.total - previous.total
		deltaIdle := current.idle - previous.idle
		value := int(math.Round(float64(deltaTotal-deltaIdle) / float64(deltaTotal) * 100))
		value = max(0, min(100, value))
		cpu = &value
	}
	s.previous = &current
	used := total - available
	percent := int(math.Round(float64(used) / float64(total) * 100))
	return HostSample{
		At: time.Now().Unix(), CPUPercent: cpu,
		MemoryUsedBytes: used, MemoryTotalBytes: total, MemoryPercent: percent,
	}, nil
}
