package system

import (
	"bufio"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

type cpuTimes struct {
	total uint64
	idle  uint64
}

func parseCPUStat(input string) (cpuTimes, error) {
	s := bufio.NewScanner(strings.NewReader(input))
	if !s.Scan() {
		return cpuTimes{}, errors.New("/proc/stat 中没有 CPU 数据")
	}
	f := strings.Fields(s.Text())
	if len(f) < 5 || f[0] != "cpu" {
		return cpuTimes{}, fmt.Errorf("无法解析 /proc/stat CPU 行: %q", s.Text())
	}
	var times cpuTimes
	for i, field := range f[1:] {
		// guest 和 guest_nice 已经包含在 user 和 nice 中。
		if i >= 8 {
			break
		}
		v, err := strconv.ParseUint(field, 10, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("无法解析 /proc/stat CPU 计数: %w", err)
		}
		times.total += v
		if i == 3 || i == 4 { // idle plus iowait
			times.idle += v
		}
	}
	return times, nil
}

func parseMemInfo(input string) (totalBytes, availableBytes uint64, err error) {
	values := map[string]uint64{}
	s := bufio.NewScanner(strings.NewReader(input))
	for s.Scan() {
		f := strings.Fields(s.Text())
		if len(f) < 2 {
			continue
		}
		key := strings.TrimSuffix(f[0], ":")
		if key != "MemTotal" && key != "MemAvailable" && key != "MemFree" && key != "Buffers" && key != "Cached" && key != "SReclaimable" && key != "Shmem" {
			continue
		}
		v, parseErr := strconv.ParseUint(f[1], 10, 64)
		if parseErr != nil {
			return 0, 0, fmt.Errorf("无法解析 /proc/meminfo 的 %s: %w", key, parseErr)
		}
		values[key] = v
	}
	if scanErr := s.Err(); scanErr != nil {
		return 0, 0, scanErr
	}
	if values["MemTotal"] == 0 {
		return 0, 0, errors.New("/proc/meminfo 中没有 MemTotal")
	}
	available, ok := values["MemAvailable"]
	if !ok {
		available = values["MemFree"] + values["Buffers"] + values["Cached"] + values["SReclaimable"]
		if available > values["Shmem"] {
			available -= values["Shmem"]
		} else {
			available = 0
		}
	}
	totalBytes = values["MemTotal"] * 1024
	availableBytes = min(available, values["MemTotal"]) * 1024
	return totalBytes, availableBytes, nil
}
