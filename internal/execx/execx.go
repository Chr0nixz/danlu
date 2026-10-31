// Package execx 创建 danlu 调用的外部命令（.bashrc、git、nvidia-smi、conda），
// 在 exec.CommandContext 上加两道护栏：
//
//   - 命令在自己的进程组里运行，ctx 结束时连它起的子进程一起杀掉；
//   - 命令退出后如果还有子进程占着输出管道，最多再等 WaitDelay 就返回。
//
// 不加护栏的话，超时只杀得掉命令本身，Wait 会一直等后台子进程关掉管道，调用方跟着卡住。
// 训练任务本身不走这里，由 runner 用 setsid 启动。
package execx

import (
	"errors"
	"os/exec"
	"time"
)

// WaitDelay 是命令退出（或超时被杀）以后，最多再等输出管道关闭的时间。
const WaitDelay = 2 * time.Second

// Output 执行命令并返回标准输出，返回前结束命令留下的整个进程组。
// 命令本身成功、只是有子进程没关管道（exec.ErrWaitDelay）时，照常返回输出。
func Output(cmd *exec.Cmd) ([]byte, error) {
	out, err := cmd.Output()
	KillGroup(cmd)
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}
	return out, err
}
