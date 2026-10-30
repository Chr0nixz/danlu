//go:build !linux

package runner

import "errors"

// 只在 Linux 上真正运行任务；其他平台能编译，方便在 Windows 上开发和跑单元测试。
func (r *Runner) Start(s Spec) (int, uint64, error) {
	if _, err := r.prepare(s); err != nil {
		return 0, 0, err
	}
	return 0, 0, errors.New("任务执行只支持 Linux")
}

func Terminate(jobID int64, pgid int)   {}
func KillAll(jobID int64, pgid int) int { return 0 }
