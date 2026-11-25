package logs

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRuneSafe(t *testing.T) {
	s := []byte("训练")
	if got := RuneSafe(s[:4]); string(got) != "训" {
		t.Fatalf("got %q", got)
	}
	if got := RuneSafe(s); string(got) != "训练" {
		t.Fatalf("got %q", got)
	}
}

func TestReadRangeAlignsStart(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	os.WriteFile(p, []byte("ab训练cd"), 0o644)
	// 从 "训" 的第二个字节开始读，应跳到 "练"
	data, next, size, err := ReadRange(p, 3, 100)
	if err != nil || string(data) != "练cd" || next != size {
		t.Fatalf("got %q next=%d size=%d err=%v", data, next, size, err)
	}
}

// 结束的任务末尾留着半个字符：ReadRange 一直等它补齐，ReadFinal 把它读掉、到达文件末尾
func TestReadFinalConsumesTruncatedTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	os.WriteFile(p, []byte("ok\n训"[:5]), 0o644) // "训" 只有前两个字节
	data, next, _, err := ReadRange(p, 3, 100)
	if err != nil || len(data) != 0 || next != 3 {
		t.Fatalf("ReadRange 应该保留残缺字节: %q next=%d err=%v", data, next, err)
	}
	data, next, size, err := ReadFinal(p, 3, 100)
	if err != nil || string(data) != "训"[:2] || next != size {
		t.Fatalf("ReadFinal 应该读到文件末尾: %q next=%d size=%d err=%v", data, next, size, err)
	}
}

func TestLastLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	os.WriteFile(p, []byte("start\nepoch 1:  10%|#   | 1/10\repoch 1:  50%|#####| 5/10\x1b[0m\r\n\n"), 0o644)
	if got := LastLine(p); got != "epoch 1:  50%|#####| 5/10" {
		t.Fatalf("got %q", got)
	}

	// 跳过 danlu 自己的开始/结束提示行
	os.WriteFile(p, []byte(BannerPrefix+" #3 开始于 x\nval_acc=0.81\n"+BannerPrefix+"结束于 y，退出码 0\n"), 0o644)
	if got := LastLine(p); got != "val_acc=0.81" {
		t.Fatalf("got %q", got)
	}
	os.WriteFile(p, []byte(BannerPrefix+" #3 开始于 x\n"), 0o644)
	if got := LastLine(p); got != "" {
		t.Fatalf("got %q", got)
	}
}

// 信号状态行接在没换行的 tqdm 进度后面时，整行留着，不从中间截掉进度
func TestLastLineKeepsProgressGluedToBashStatus(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	line := "epoch 89/300:  59%|###  | 59/100 [loss=0.0279]/h/run/7/cmd.sh: line 1: 47971 Terminated              python3 train.py"
	os.WriteFile(p, []byte("epoch 89/300:  58%|###  | 58/100\r"+line+"\n"+BannerPrefix+"结束于 x，退出码 143\n"), 0o644)
	if got := LastLine(p); got != line {
		t.Fatalf("LastLine = %q", got)
	}
	os.WriteFile(p, []byte("/h/run/7/cmd.sh: line 1: foo: command not found\n"), 0o644)
	if got := LastLine(p); got != "foo: command not found" {
		t.Fatalf("LastLine = %q", got)
	}
}

func TestErrorLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "l.log")
	cases := map[string]string{
		// 普通 Python 异常，后面跟着 danlu 的结束提示
		"epoch 1: 50%|#####| 5/10\nTraceback (most recent call last):\n  File \"train.py\", line 9, in <module>\n" +
			"\x1b[31mRuntimeError: CUDA out of memory. Tried to allocate 2.00 GiB\x1b[0m\n" + BannerPrefix + "结束，退出码 1\n": "RuntimeError: CUDA out of memory. Tried to allocate 2.00 GiB",
		// torchrun：真正的原因在 ChildFailedError 汇总之前，带 rank 前缀
		"[rank0]: Traceback (most recent call last):\n[rank0]: torch.OutOfMemoryError: CUDA out of memory.\n" +
			"torch.distributed.elastic.multiprocessing.errors.ChildFailedError: \n====\ntrain.py FAILED\n====\n": "[rank0]: torch.OutOfMemoryError: CUDA out of memory.",
		"step 100 loss=0.3\nKilled\n":        "Killed",
		"epoch 3 done\nval_acc=0.81\n":       "",
		"loss: 0.5\nlr: 3e-4\nError: boom\n": "Error: boom",
		// bash 执行任务命令时的报错：去掉 cmd.sh 的绝对路径，第 1 行不写行号
		"/home/u/.danlu/run/8/cmd.sh: line 1: torchrun: command not found\n" + BannerPrefix + "结束于 x，退出码 127\n": "torchrun: command not found",
		// 数据目录带空格也能去掉；多行命令保留行号
		"/data/my dir/run/12/cmd.sh: line 3: cd: /nope: No such file or directory\n":   "line 3: cd: /nope: No such file or directory",
		"python: can't open file '/w/trian.py': [Errno 2] No such file or directory\n": "python: can't open file '/w/trian.py': [Errno 2] No such file or directory",
		"./train.sh: line 4: accelerate: command not found\n":                          "./train.sh: line 4: accelerate: command not found",
		// 程序自己打印的"文件不存在"不算报错行
		"warn: cache.json: No such file or directory\nepoch 1 done\n": "",
		// 信号的状态行不是报错行
		"epoch 3\n/h/run/7/cmd.sh: line 1: 12345 Terminated              python3 train.py\n": "",
	}
	for in, want := range cases {
		os.WriteFile(p, []byte(in), 0o644)
		if got := ErrorLine(p); got != want {
			t.Errorf("ErrorLine(%q) = %q，期望 %q", in, got, want)
		}
	}
}

func TestClassifyFailure(t *testing.T) {
	one, two, one26, one27, one37, one39, one43 := 1, 2, 126, 127, 137, 139, 143
	cases := []struct {
		name   string
		in     FailureInput
		kind   string
		reason string
	}{
		{"start", FailureInput{StartError: "启动失败: fork 失败"}, "start", "启动失败: fork 失败"},
		{"reboot", FailureInput{Reboot: true}, "reboot", "服务器重启，任务中断"},
		{"host oom event", FailureInput{OOMKnown: true, HostOOM: true}, "host_oom", "主机内存不足，被系统杀掉"},
		{"exit 137 with oom event", FailureInput{ExitCode: &one37, OOMKnown: true, HostOOM: true}, "host_oom", "主机内存不足，被系统杀掉"},
		{"dataloader worker oom", FailureInput{ExitCode: &one, OOMKnown: true, HostOOM: true, LogTail: "RuntimeError: DataLoader worker (pid 123) is killed by signal: Killed."}, "host_oom", "运行期间本机发生过 OOM kill"},
		{"dataloader worker without oom event", FailureInput{ExitCode: &one, OOMKnown: true, LogTail: "RuntimeError: DataLoader worker (pid 123) is killed by signal: Killed."}, "exception", "Python 异常"},
		{"exit 137 fallback", FailureInput{ExitCode: &one37}, "host_oom", "退出码 137，可能是主机内存不足"},
		{"exit 137 without oom event", FailureInput{ExitCode: &one37, OOMKnown: true}, "signal", "被信号终止（SIGKILL）"},
		{"exit 137 cuda oom without oom event", FailureInput{ExitCode: &one37, OOMKnown: true, LogTail: "CUDA out of memory"}, "cuda_oom", "CUDA 显存不足"},
		{"other exit ignores oom delta", FailureInput{ExitCode: &one, HostOOM: true, LogTail: "RuntimeError: bad input"}, "exception", "Python 异常"},
		{"cuda oom", FailureInput{ExitCode: &one, ExternalSeen: true, LogTail: "torch.OutOfMemoryError: CUDA out of memory"}, "cuda_oom", "CUDA 显存不足，运行期间卡上出现过外部进程"},
		{"cublas oom", FailureInput{ExitCode: &one, LogTail: "CUBLAS_STATUS_ALLOC_FAILED"}, "cuda_oom", "CUDA 显存不足"},
		{"nccl", FailureInput{ExitCode: &one, LogTail: "Watchdog caught collective operation timeout"}, "nccl", "NCCL 通信超时或出错"},
		{"exception", FailureInput{ExitCode: &one, LogTail: "ValueError: bad value"}, "exception", "Python 异常"},
		{"signal", FailureInput{ExitCode: &one39}, "signal", "被信号终止（SIGSEGV）"},
		{"killed", FailureInput{}, "killed", "进程被外部终止（没有留下退出码）"},
		{"exit", FailureInput{ExitCode: &two}, "exit", "退出码 2"},
		{"command not found", FailureInput{ExitCode: &one27, LogTail: "/h/run/8/cmd.sh: line 1: torchrun: command not found"}, "command", "命令没找到"},
		{"exit 127 beats earlier exception", FailureInput{ExitCode: &one27, LogTail: "ValueError: old\n/h/run/8/cmd.sh: line 2: foo: command not found"}, "command", "命令没找到"},
		{"not executable", FailureInput{ExitCode: &one26, LogTail: "/h/run/8/cmd.sh: line 1: ./run.sh: Permission denied"}, "command", "命令无法执行"},
		{"shell path error", FailureInput{ExitCode: &one, LogTail: "/h/run/8/cmd.sh: line 1: cd: /nope: No such file or directory"}, "command", "命令或路径出错"},
		{"python cannot open file", FailureInput{ExitCode: &two, LogTail: "python: can't open file '/w/trian.py': [Errno 2] No such file or directory"}, "command", "命令或路径出错"},
		// 进程被信号杀掉时 bash 打印的状态行带 cmd.sh 前缀，仍按信号归类
		{"bash status line for SIGTERM", FailureInput{ExitCode: &one43, LogTail: "epoch 3: 40%\n/h/run/7/cmd.sh: line 1: 12345 Terminated              python3 train.py 300\n"}, "signal", "被信号终止（SIGTERM）"},
		{"bash status line for SIGKILL", FailureInput{ExitCode: &one37, OOMKnown: true, LogTail: "/h/run/7/cmd.sh: line 1:  4242 Killed                  python train.py\n"}, "signal", "被信号终止（SIGKILL）"},
		{"conda activate failed is not command-not-found", FailureInput{ExitCode: &one27, LogTail: WrapperCondaFailed + ": vit\n"}, "command", "conda 环境激活失败"},
		{"conda missing", FailureInput{ExitCode: &one27, LogTail: WrapperCondaMissing + ": /opt/conda/etc/profile.d/conda.sh\n"}, "command", "conda 环境激活失败"},
		{"no workdir", FailureInput{ExitCode: &one, LogTail: WrapperNoWorkdir + ": /w/gone\n"}, "command", "无法进入工作目录"},
		{"exception after shell line", FailureInput{ExitCode: &one, LogTail: "x.sh: line 1: foo: command not found\nKeyError: 'lr'"}, "exception", "Python 异常"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyFailure(tc.in)
			if got.Kind != tc.kind || got.Reason != tc.reason {
				t.Fatalf("ClassifyFailure = %+v，期望 kind=%q reason=%q", got, tc.kind, tc.reason)
			}
		})
	}
	if got := ClassifyFailure(FailureInput{ExitCode: &one27}); got.Detail != DetailCmdNotFound {
		t.Fatalf("127 的细分码 = %q", got.Detail)
	}
	if got := ClassifyFailure(FailureInput{ExitCode: &one26}); got.Detail != DetailCmdNotExecutable {
		t.Fatalf("126 的细分码 = %q", got.Detail)
	}
}

func TestTracebackOffsetAcrossReadBoundary(t *testing.T) {
	p := filepath.Join(t.TempDir(), "large.log")
	marker := []byte("Traceback (most recent call last):")
	prefix := bytes.Repeat([]byte("x"), 64<<10-len(marker)/2)
	if err := os.WriteFile(p, append(append(prefix, marker...), []byte("\n  File train.py\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := TracebackOffset(p); got != int64(len(prefix)) {
		t.Fatalf("TracebackOffset = %d，期望 %d", got, len(prefix))
	}
	if got := TracebackOffset(filepath.Join(t.TempDir(), "missing.log")); got != -1 {
		t.Fatalf("没有日志时 TracebackOffset = %d，期望 -1", got)
	}
}

func TestTracebackOffsetOnlyReadsLogTail(t *testing.T) {
	p := filepath.Join(t.TempDir(), "long.log")
	marker := []byte("Traceback (most recent call last):\n")
	data := append(slices.Clone(marker), bytes.Repeat([]byte("step\n"), TracebackWindow/5+1)...)
	crash := int64(len(data))
	data = append(append(data, marker...), marker...)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := TracebackOffset(p); got != crash {
		t.Fatalf("TracebackOffset = %d，期望末尾第一个 traceback %d", got, crash)
	}
}

func TestFindTracker(t *testing.T) {
	cases := map[string]string{
		"wandb: 🚀 View run at \x1b[34m\x1b[4mhttps://wandb.ai/me/proj/runs/abc123\x1b[0m\n": "https://wandb.ai/me/proj/runs/abc123",
		"wandb: View project at https://wandb.ai/me/proj\n":                                 "",
		"swanlab: 🚀 View run at https://swanlab.cn/@me/proj/runs/x9y8\n":                    "https://swanlab.cn/@me/proj/runs/x9y8",
	}
	for in, want := range cases {
		if got := FindTrackerIn([]byte(in)); got != want {
			t.Errorf("FindTrackerIn(%q) = %q，期望 %q", in, got, want)
		}
	}
}
