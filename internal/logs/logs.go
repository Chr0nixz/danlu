// Package logs 读取任务日志：分段读取、取最后一行和报错行、识别 W&B / SwanLab 链接。
package logs

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode/utf8"
)

// ReadRange 读取 [off, off+limit) 之间的内容，首尾都对齐到完整的 UTF-8 字符。
// 返回实际读到的数据、下一次应该从哪里读，以及文件当前大小。
func ReadRange(path string, off, limit int64) ([]byte, int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, off, 0, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, off, 0, err
	}
	size := fi.Size()
	if off < 0 {
		off = 0
	}
	if off >= size {
		return nil, off, size, nil
	}
	n := min(limit, size-off)
	buf := make([]byte, n)
	if _, err := f.ReadAt(buf, off); err != nil && err != io.EOF {
		return nil, off, size, err
	}
	// 开头若落在多字节字符中间，跳过这几个续字节
	skip := 0
	for skip < len(buf) && skip < 3 && !utf8.RuneStart(buf[skip]) {
		skip++
	}
	data := RuneSafe(buf[skip:])
	return data, off + int64(skip) + int64(len(data)), size, nil
}

// Tail 读取最后 n 字节，返回数据和起始偏移。
func Tail(path string, n int64) ([]byte, int64, int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, 0, 0, err
	}
	start := max(0, fi.Size()-n)
	data, next, size, err := ReadRange(path, start, n)
	return data, next - int64(len(data)), size, err
}

// RuneSafe 去掉末尾不完整的 UTF-8 字符，留到下一次再读，避免中文被切成乱码。
func RuneSafe(b []byte) []byte {
	for i := 1; i <= 3 && i <= len(b); i++ {
		c := b[len(b)-i]
		if utf8.RuneStart(c) {
			if !utf8.FullRune(b[len(b)-i:]) {
				return b[:len(b)-i]
			}
			return b
		}
	}
	return b
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*\x07`)

func StripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// BannerPrefix 是 runner 包装脚本在任务开始和结束时打印的提示行的前缀。
const BannerPrefix = "[danlu] 任务"

// LastLine 取日志的最后一行可见内容（处理 \r 进度条和 ANSI 颜色），用于在列表里一眼看进度。
// 跳过 danlu 自己的开始/结束提示，这样结束的任务显示的是程序最后的输出（通常是最终指标）。
func LastLine(path string) string {
	return lastMatch(path, 8192, func(l string) bool { return !strings.HasPrefix(l, BannerPrefix) })
}

// 像报错的行：Python 异常（含 torchrun 给每个 rank 加的 "[rank0]: " 前缀）、被 OOM killer 杀掉、段错误、CUDA 报错
var errLineRe = regexp.MustCompile(`^(?:\[rank\d+\]:\s*)?(?:[A-Za-z_]\w*\.)*\w*(?:Error|Exception)(?::|$)|^Killed$|Segmentation fault|core dumped|CUDA error:`)

// ErrorLine 在日志末尾找最后一条像报错的行，用来在失败任务上直接说明"挂在哪"；找不到返回空串。
// torchrun 退出前总会打印一行 ChildFailedError 汇总，真正的原因在它前面，所以跳过它。
func ErrorLine(path string) string {
	data, _, _, err := Tail(path, 32<<10)
	if err != nil {
		return ""
	}
	return ErrorLineIn(string(data))
}

// ErrorLineIn 从日志片段中找最后一条像报错的可见行。
func ErrorLineIn(data string) string {
	return lastMatchText(data, func(l string) bool {
		return !strings.Contains(l, "ChildFailedError") && errLineRe.MatchString(l)
	})
}

// lastMatch 从日志末尾 tail 字节里倒着找第一条满足 ok 的可见行（处理 \r 进度条和 ANSI 颜色），截到 200 字。
func lastMatch(path string, tail int64, ok func(string) bool) string {
	data, _, _, err := Tail(path, tail)
	if err != nil {
		return ""
	}
	return lastMatchText(string(data), ok)
}

func lastMatchText(data string, ok func(string) bool) string {
	lines := strings.Split(data, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimRight(lines[i], "\r")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		l = strings.TrimSpace(StripANSI(l))
		if l != "" && ok(l) {
			if r := []rune(l); len(r) > 200 {
				l = string(r[:200]) + "…"
			}
			return l
		}
	}
	return ""
}

type FailureInput struct {
	StartError string
	ExitCode   *int
	Reboot     bool
	// OOMKnown 表示读到了任务运行期间的 oom_kill 计数；HostOOM 表示计数增加了。
	OOMKnown     bool
	HostOOM      bool
	ExternalSeen bool
	LogTail      string
}

type Failure struct {
	Kind   string
	Reason string
}

// 主机内存不足的两种原因文字；推送按它区分确认和推测，不转发其他原因文字。
const (
	ReasonHostOOM      = "主机内存不足，被系统杀掉"
	ReasonMaybeHostOOM = "退出码 137，可能是主机内存不足"
)

// ClassifyFailure 按阶段方案规定的优先级归类失败。日志只需传末尾片段。
func ClassifyFailure(in FailureInput) Failure {
	exit137 := in.ExitCode != nil && *in.ExitCode == 137
	switch {
	case in.StartError != "":
		return Failure{Kind: "start", Reason: in.StartError}
	case in.Reboot && in.ExitCode == nil:
		return Failure{Kind: "reboot", Reason: "服务器重启，任务中断"}
	case in.HostOOM && (exit137 || in.ExitCode == nil):
		return Failure{Kind: "host_oom", Reason: ReasonHostOOM}
	case exit137 && !in.OOMKnown:
		// 读不到计数时只能凭退出码推测；读得到而计数没变，就按日志和信号继续判断。
		return Failure{Kind: "host_oom", Reason: ReasonMaybeHostOOM}
	}

	text := strings.ToLower(StripANSI(in.LogTail))
	switch {
	case strings.Contains(text, "cuda out of memory"),
		strings.Contains(text, "outofmemoryerror"),
		strings.Contains(text, "cublas_status_alloc_failed"):
		reason := "CUDA 显存不足"
		if in.ExternalSeen {
			reason += "，运行期间卡上出现过外部进程"
		}
		return Failure{Kind: "cuda_oom", Reason: reason}
	case strings.Contains(text, "watchdog caught collective operation timeout"),
		strings.Contains(text, "nccl error"),
		(strings.Contains(text, "processgroupnccl") && strings.Contains(text, "timeout")):
		return Failure{Kind: "nccl", Reason: "NCCL 通信超时或出错"}
	case ErrorLineIn(in.LogTail) != "":
		return Failure{Kind: "exception", Reason: "Python 异常"}
	}

	if in.ExitCode != nil {
		if *in.ExitCode > 128 {
			sig := *in.ExitCode - 128
			return Failure{Kind: "signal", Reason: fmt.Sprintf("被信号终止（%s）", signalName(sig))}
		}
		return Failure{Kind: "exit", Reason: fmt.Sprintf("退出码 %d", *in.ExitCode)}
	}
	return Failure{Kind: "killed", Reason: "进程被外部终止（没有留下退出码）"}
}

func signalName(n int) string {
	if name, ok := map[int]string{2: "SIGINT", 3: "SIGQUIT", 4: "SIGILL", 6: "SIGABRT", 7: "SIGBUS", 8: "SIGFPE", 9: "SIGKILL", 11: "SIGSEGV", 13: "SIGPIPE", 14: "SIGALRM", 15: "SIGTERM"}[n]; ok {
		return name
	}
	return fmt.Sprintf("信号 %d", n)
}

// TracebackWindow 是查找 traceback 的范围：日志末尾这一段。
// 崩溃时各个 rank 的 traceback 都在末尾；只读末尾让收尾在调度锁里保持毫秒级，
// 也避开训练早期打印过、随后又恢复的 traceback。
const TracebackWindow = 4 << 20

// TracebackOffset 返回日志末尾 TracebackWindow 字节内第一个 traceback 标记的偏移，没有时返回 -1。
func TracebackOffset(path string) int64 {
	f, err := os.Open(path)
	if err != nil {
		return -1
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return -1
	}
	start := max(0, fi.Size()-TracebackWindow)
	buf := make([]byte, fi.Size()-start)
	n, err := f.ReadAt(buf, start)
	if err != nil && err != io.EOF {
		return -1
	}
	if i := bytes.Index(buf[:n], []byte("Traceback (most recent call last):")); i >= 0 {
		return start + int64(i)
	}
	return -1
}

var trackerRe = regexp.MustCompile(`https://(?:wandb\.ai|swanlab\.cn)/[^\s\x1b'"<>]*?/runs/[A-Za-z0-9_-]+`)

// FindTracker 在日志开头 limit 字节里找 W&B / SwanLab 的 run 链接。
func FindTracker(path string, limit int64) string {
	data, _, _, err := ReadRange(path, 0, limit)
	if err != nil {
		return ""
	}
	return FindTrackerIn(data)
}

func FindTrackerIn(data []byte) string {
	if !bytes.Contains(data, []byte("https://")) {
		return ""
	}
	return trackerRe.FindString(string(data))
}
