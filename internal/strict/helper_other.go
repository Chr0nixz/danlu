//go:build !linux

package strict

import (
	"encoding/json"
	"io"
)

// RunHelper 只在 Linux 上有意义。
func RunHelper(_ []string, _ io.Reader, stdout io.Writer, _ string) int {
	_ = json.NewEncoder(stdout).Encode(StartResult{Error: "严格限卡只支持 Linux"})
	return 1
}
