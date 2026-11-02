//go:build !linux

package runner

// OOMKills 在非 Linux 系统上不可用。
func (r *Runner) OOMKills() (int64, bool) { return 0, false }
