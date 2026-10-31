//go:build linux

package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const maxBashEnvOutput = 8 << 20

type limitedBuffer struct {
	bytes.Buffer
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > maxBashEnvOutput {
		return 0, errors.New("环境变量输出超过 8 MiB")
	}
	return b.Buffer.Write(p)
}

func loadBashrcEnvironment() ([]string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("确定用户主目录失败: %w", err)
	}
	bashrc := filepath.Join(home, ".bashrc")
	info, err := os.Stat(bashrc)
	if err != nil {
		return nil, fmt.Errorf("读取 %s 失败: %w", bashrc, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%s 不是普通文件", bashrc)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--rcfile", bashrc, "-ic",
		"builtin printf '\\036DANLU_ENV\\037'; exec /usr/bin/env -0")
	cmd.Env = os.Environ()
	cmd.Dir = home
	cmd.Stdin = nil
	cmd.Stderr = io.Discard
	var output limitedBuffer
	cmd.Stdout = &output
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("加载 %s 超时（10 秒）", bashrc)
		}
		return nil, fmt.Errorf("加载 %s 失败: %w", bashrc, err)
	}
	env, err := parseBashEnvironment(output.Bytes())
	if err != nil {
		return nil, err
	}
	return env, nil
}
