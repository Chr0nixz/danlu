//go:build linux

package doctor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"time"

	"danlu/internal/config"
	"danlu/internal/localsock"
)

func platformChecks(ctx context.Context, paths config.Paths) []Check {
	home, err := os.UserHomeDir()
	if err != nil {
		home = filepath.Dir(paths.Home)
	}
	unit := filepath.Join(home, ".config", "systemd", "user", "danlu.service")
	if _, err := os.Stat(unit); err != nil {
		return append(systemdUnavailable(ctx, "systemd 用户服务文件不存在: "+unit), timeSyncCheck(ctx))
	}
	checks := []Check{{"service_unit", "ok", "systemd 服务单元", unit, ""}}
	out, err := output(ctx, "systemctl", "--user", "show", "danlu.service", "-p", "LoadState", "-p", "ActiveState", "-p", "KillMode", "-p", "Restart", "-p", "OOMPolicy")
	if err != nil {
		checks = append(checks,
			Check{"service_active", "fail", "danlu 服务", "systemctl 查询失败: " + err.Error(), "运行 danlu install-service 并检查 systemctl --user status danlu"},
			Check{"service_kill_mode", "warn", "训练进程隔离", "无法确认 KillMode=process", "确认服务单元使用 KillMode=process"},
			Check{"service_restart", "warn", "服务自动重启", "无法确认 Restart=on-failure", "确认服务单元使用 Restart=on-failure"},
			Check{"service_oom_policy", "warn", "训练 OOM 不重启服务", "无法确认 OOMPolicy=continue", "确认服务单元使用 OOMPolicy=continue"},
			Check{"service_load", "warn", "systemd 服务加载", "无法确认 LoadState=loaded", "运行 systemctl --user daemon-reload"},
		)
	} else {
		props := parseProperties(string(out))
		checks = append(checks,
			propertyCheck("service_active", "danlu 服务", props["ActiveState"], "active", "检查 systemctl --user status danlu"),
			propertyCheck("service_kill_mode", "训练进程隔离", props["KillMode"], "process", "运行 danlu install-service，确认 KillMode=process"),
			propertyCheck("service_restart", "服务自动重启", props["Restart"], "on-failure", "运行 danlu install-service，确认 Restart=on-failure"),
			oomPolicyCheck(props["OOMPolicy"]),
		)
		if props["LoadState"] != "loaded" {
			checks = append(checks, Check{"service_load", "fail", "systemd 服务单元", "LoadState=" + props["LoadState"], "运行 systemctl --user daemon-reload"})
		}
	}
	checks = append(checks, lingerCheck(ctx), timeSyncCheck(ctx))
	return checks
}

func systemdUnavailable(ctx context.Context, detail string) []Check {
	return []Check{
		{"service_unit", "fail", "systemd 服务单元", detail, "运行 danlu install-service"},
		{"service_active", "fail", "danlu 服务", "无法确认服务处于运行状态", "检查 systemctl --user status danlu"},
		{"service_kill_mode", "warn", "训练进程隔离", "无法确认 KillMode=process", "确认服务单元使用 KillMode=process"},
		{"service_restart", "warn", "服务自动重启", "无法确认 Restart=on-failure", "确认服务单元使用 Restart=on-failure"},
		{"service_oom_policy", "warn", "训练 OOM 不重启服务", "无法确认 OOMPolicy=continue", "确认服务单元使用 OOMPolicy=continue"},
		lingerCheck(ctx),
	}
}

func lingerCheck(ctx context.Context) Check {
	current, err := user.Current()
	if err != nil {
		return Check{"linger", "warn", "linger", "无法确定当前用户: " + err.Error(), "检查 loginctl show-user 的 Linger 状态"}
	}
	out, err := output(ctx, "loginctl", "show-user", current.Username, "-p", "Linger", "--value")
	if err != nil {
		return Check{"linger", "warn", "linger", "无法查询: " + err.Error(), "运行 sudo loginctl enable-linger " + current.Username}
	}
	if strings.TrimSpace(string(out)) == "yes" {
		return Check{"linger", "ok", "linger", "已开启", ""}
	}
	return Check{"linger", "fail", "linger", "未开启", "运行 sudo loginctl enable-linger " + current.Username}
}

func timeSyncCheck(ctx context.Context) Check {
	out, err := output(ctx, "timedatectl", "show", "-p", "NTPSynchronized", "--value")
	if err != nil {
		return Check{"time_sync", "warn", "系统时间同步", "timedatectl 不可用，未检查", "可用时运行 timedatectl status 检查 NTP 同步"}
	}
	switch strings.TrimSpace(string(out)) {
	case "yes":
		return Check{"time_sync", "ok", "系统时间同步", "NTP 已同步", ""}
	case "no":
		return Check{"time_sync", "warn", "系统时间同步", "NTP 尚未同步", "检查 systemd-timesyncd 或 chrony"}
	default:
		return Check{"time_sync", "warn", "系统时间同步", "timedatectl 未返回同步状态", "检查 NTP 服务状态"}
	}
}

// localSocketCheck 看命令行入口（数据目录里的 danlu.sock，FEATURES 5.1）在不在、权限对不对、连不连得上。
func localSocketCheck(paths config.Paths) (Check, bool) {
	const id, title = "cli_socket", "命令行入口"
	path := localsock.Path(paths.Home)
	fi, err := os.Lstat(path)
	switch {
	case errors.Is(err, os.ErrNotExist) && len(path) > localsock.MaxPath:
		return Check{id, "warn", title, fmt.Sprintf("数据目录路径太长：socket 路径 %d 字节，超过 unix socket 的 %d 字节上限", len(path), localsock.MaxPath),
			"把数据目录（DANLU_HOME）换到路径更短的地方"}, true
	case errors.Is(err, os.ErrNotExist):
		return Check{id, "warn", title, "没有 " + path + "：服务没有运行，或者还是不带命令行入口的旧版本",
			"运行 danlu restart；仍然没有就查 danlu logs 里的「命令行入口」"}, true
	case err != nil:
		return Check{id, "warn", title, err.Error(), ""}, true
	case fi.Mode()&os.ModeSocket == 0:
		return Check{id, "fail", title, path + " 不是 socket", "删掉它后运行 danlu restart"}, true
	case fi.Mode().Perm() != 0o600:
		return Check{id, "fail", title, fmt.Sprintf("%s 的权限是 %o，应为 600", path, fi.Mode().Perm()), "运行 danlu restart 重新创建"}, true
	}
	conn, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return Check{id, "warn", title, "连不上 " + path + "：" + err.Error(), "运行 danlu restart"}, true
	}
	conn.Close()
	return Check{id, "ok", title, path, ""}, true
}
