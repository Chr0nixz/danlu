// Package logs 读取任务日志：分段读取、取最后一行、识别 W&B / SwanLab 链接。
package logs

import (
	"bytes"
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
	data, _, _, err := Tail(path, 8192)
	if err != nil {
		return ""
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimRight(lines[i], "\r")
		if j := strings.LastIndexByte(l, '\r'); j >= 0 {
			l = l[j+1:]
		}
		l = strings.TrimSpace(StripANSI(l))
		if l != "" && !strings.HasPrefix(l, BannerPrefix) {
			if r := []rune(l); len(r) > 200 {
				l = string(r[:200]) + "…"
			}
			return l
		}
	}
	return ""
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
