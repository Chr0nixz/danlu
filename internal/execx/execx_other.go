//go:build !linux

package execx

import (
	"context"
	"os/exec"
)

// Command 在其他平台上只设 WaitDelay，方便在 Windows 上开发和跑单测。
func Command(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = WaitDelay
	return cmd
}

func KillGroup(*exec.Cmd) {}
