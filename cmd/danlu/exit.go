package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
)

// 退出码（FEATURES 5.1「给脚本和 AI 助手的约定」）。现有运维命令仍是 0/1/2，以后逐个往这张表上靠。
const (
	exitErr          = 1   // 其他错误：校验失败、服务端错误
	exitUsage        = 2   // 用法错误：参数不对、未知命令
	exitUnreachable  = 3   // 连不上服务：没在运行、socket 不存在、身份不符
	exitNotFound     = 4   // 任务不存在
	exitJobFailed    = 10  // 只有 wait、tail -f：有任务失败
	exitJobCancelled = 11  // 只有 wait、tail -f：没有失败但有取消
	exitTimeout      = 124 // 只有 wait：到了 --timeout 还有任务没结束，和 coreutils 的 timeout 一样
)

// cliError 带退出码和机器码。code 与 /api 的错误码同一张表；只在本地出现的错误用 cli_* 码。
type cliError struct {
	exit   int
	code   string
	msg    string
	params map[string]any
	silent bool // 结果已经打印过（wait 的任务失败、api 的错误响应），main 不再输出错误
}

func (e *cliError) Error() string { return e.msg }

func usageErr(format string, a ...any) error {
	return &cliError{exit: exitUsage, code: "cli_usage", msg: fmt.Sprintf(format, a...)}
}

// silentExit 只设退出码，不再打印错误：结果本身已经说明了情况。
func silentExit(code int) error { return &cliError{exit: code, silent: true} }

// errHelpShown：-h 已经打印了用法，以 0 退出。
var errHelpShown = errors.New("help shown")

// jsonErrors 为 true 时（命令带了 --json），错误以一行 JSON 写到 stderr，而不是"错误: …"。
var jsonErrors bool

// exitWith 打印错误并退出；err 为 nil 时什么都不做。
func exitWith(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, errHelpShown) {
		os.Exit(0)
	}
	ce := &cliError{exit: exitErr, code: "cli_error", msg: err.Error()}
	errors.As(err, &ce)
	if !ce.silent {
		if jsonErrors {
			body := map[string]any{"error": ce.msg, "code": ce.code}
			if len(ce.params) > 0 {
				body["params"] = ce.params
			}
			enc := json.NewEncoder(os.Stderr) // 一行，末尾换行；路径里的 < > & 不转义
			enc.SetEscapeHTML(false)
			_ = enc.Encode(body)
		} else {
			fmt.Fprintln(os.Stderr, "错误:", ce.msg)
		}
	}
	os.Exit(ce.exit)
}

// wantsJSON 在解析参数之前看有没有 --json，解析出错时也能按 JSON 报错。只看 -- 之前的部分。
func wantsJSON(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "--json", "-json", "--json=true", "-json=true":
			return true
		}
	}
	return false
}

// usages 是每个参数集的用法说明，只在 -h 时由 parseFlags 打印。
var usages = map[*flag.FlagSet]string{}

// newFlags 建一个参数集：-h 把用法和参数说明打印到 stdout，以 0 退出；参数错误以 2（cli_usage）退出，
// 只在 stderr 报一行错误。flag 包在 Parse 出错时会调用 Usage，所以 Usage 本身什么都不打印，
// 不然参数写错时用法会混进 stdout，-h 时会打印两遍。
func newFlags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	usages[fs] = usage
	return fs
}

func printUsage(fs *flag.FlagSet) {
	fmt.Fprintln(os.Stdout, "用法: "+usages[fs])
	fs.SetOutput(os.Stdout)
	fs.PrintDefaults()
	fs.SetOutput(io.Discard)
}

func parseFlags(fs *flag.FlagSet, args []string) error {
	// 先粗看一遍有没有 --json，参数解析出错时也按 JSON 报错；解析成功后以真正解析到的值为准
	// （danlu submit python x.py --json 里的 --json 属于任务命令）
	if wantsJSON(args) {
		jsonErrors = true
	}
	err := fs.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		printUsage(fs)
		return errHelpShown
	}
	if err != nil {
		return usageErr("%v（danlu %s -h 查看用法）", err, fs.Name())
	}
	if f := fs.Lookup("json"); f != nil {
		jsonErrors = f.Value.String() == "true"
	}
	return nil
}
