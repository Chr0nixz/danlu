//go:build !linux

package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// 只在 Linux 上真正运行任务；其他平台能编译，方便在 Windows 上开发和跑单元测试。
func (r *Runner) Start(s Spec, env []string) (Started, error) {
	if _, err := r.prepare(s); err != nil {
		return Started{}, err
	}
	return Started{}, errors.New("任务执行只支持 Linux")
}

// JobExec 只在 Linux 上有意义（严格任务的服务入口）。
func JobExec([]string) int {
	fmt.Fprintln(os.Stderr, "job-exec 只支持 Linux")
	return 1
}

func (r *Runner) Terminate(jobID int64, pgid int)   {}
func (r *Runner) KillAll(jobID int64, pgid int) int { return 0 }

func (r *Runner) ProcStats(jobID int64) ProcessStats { return ProcessStats{} }

func loadBashrcEnvironment(context.Context) ([]string, error) {
	return nil, errors.New("Bash 环境只支持 Linux")
}
