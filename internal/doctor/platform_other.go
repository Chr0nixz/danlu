//go:build !linux

package doctor

import (
	"context"

	"danlu/internal/config"
)

func platformChecks(context.Context, config.Paths) []Check {
	return []Check{
		{"service_unit", "warn", "systemd 服务单元", "当前平台不支持 systemd 检查", "在 Linux GPU 服务器上运行完整体检"},
		{"service_active", "warn", "danlu 服务", "当前平台不支持 systemd 检查", "在 Linux GPU 服务器上运行完整体检"},
		{"service_kill_mode", "warn", "训练进程隔离", "当前平台不支持 systemd 检查", "在 Linux GPU 服务器上运行完整体检"},
		{"service_restart", "warn", "服务自动重启", "当前平台不支持 systemd 检查", "在 Linux GPU 服务器上运行完整体检"},
		{"linger", "warn", "linger", "当前平台不支持 loginctl 检查", "在 Linux GPU 服务器上运行完整体检"},
		{"time_sync", "warn", "系统时间同步", "当前平台未检查时间同步", "在 Linux GPU 服务器上运行完整体检"},
	}
}
