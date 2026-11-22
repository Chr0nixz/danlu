//go:build linux

package runner

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"syscall"
)

// JobExec 是严格任务的服务入口（danlu job-exec），由 systemd 以用户身份执行：
// 换上 danlu 写好的完整环境（读完即删，里面可能有令牌），把标准输出和错误接到任务日志，
// 进入任务目录，然后 exec 包装脚本。pid 不变，所以包装脚本写的 pid 就是服务的主进程。
// 只在出错时返回。
func JobExec(args []string) int {
	fs := flag.NewFlagSet("job-exec", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	envFile := fs.String("env", "", "")
	logPath := fs.String("log", "", "")
	dir := fs.String("dir", "", "")
	if err := fs.Parse(args); err != nil || *envFile == "" || *logPath == "" || len(fs.Args()) == 0 {
		fmt.Fprintln(os.Stderr, "用法: danlu job-exec --env 文件 --log 文件 [--dir 目录] -- 命令…")
		return 2
	}
	b, err := os.ReadFile(*envFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[danlu] 读不到任务环境:", err)
		return 1
	}
	_ = os.Remove(*envFile)
	var env []string
	for _, kv := range bytes.Split(b, []byte{0}) {
		if len(kv) > 0 {
			env = append(env, string(kv))
		}
	}
	logf, err := os.OpenFile(*logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		fmt.Fprintln(os.Stderr, "[danlu] 打不开任务日志:", err)
		return 1
	}
	for _, fd := range []int{1, 2} {
		if err := syscall.Dup3(int(logf.Fd()), fd, 0); err != nil {
			fmt.Fprintln(os.Stderr, "[danlu] 接日志失败:", err)
			return 1
		}
	}
	if *dir != "" {
		if err := os.Chdir(*dir); err != nil {
			fmt.Fprintln(os.Stderr, "[danlu] 进不了任务目录:", err)
			return 1
		}
	}
	argv := fs.Args()
	err = syscall.Exec(argv[0], argv, env)
	fmt.Fprintln(os.Stderr, "[danlu] 启动包装脚本失败:", err) // 这时标准错误已经是任务日志
	return 1
}
