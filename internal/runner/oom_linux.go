//go:build linux

package runner

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// OOMKills 读取当前 danlu cgroup 的 memory.events.oom_kill 计数。
func (r *Runner) OOMKills() (int64, bool) {
	path := os.Getenv("DANLU_MEMORY_EVENTS")
	if path == "" {
		var ok bool
		path, ok = memoryEventsPath()
		if !ok {
			return 0, false
		}
	}
	return readOOMKills(path)
}

func memoryEventsPath() (string, bool) {
	b, err := os.ReadFile("/proc/self/cgroup")
	if err != nil {
		return "", false
	}
	for _, line := range strings.Split(string(b), "\n") {
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 || parts[0] != "0" || parts[1] != "" {
			continue
		}
		rel := strings.TrimPrefix(parts[2], "/")
		root := filepath.Clean("/sys/fs/cgroup")
		path := filepath.Clean(filepath.Join(root, rel, "memory.events"))
		if !strings.HasPrefix(path, root+string(os.PathSeparator)) {
			return "", false
		}
		return path, true
	}
	return "", false
}

func readOOMKills(path string) (int64, bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 || fields[0] != "oom_kill" {
			continue
		}
		count, err := strconv.ParseInt(fields[1], 10, 64)
		return count, err == nil
	}
	return 0, false
}
