package runner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"danlu/internal/execx"
)

const maxPackageList = 16 << 20

// CapturePackages 读取任务使用的 conda 或 Python 包清单，不启动训练任务。
func (r *Runner) CapturePackages(ctx context.Context, s Spec, baseEnv []string) ([]byte, error) {
	var name string
	var args []string
	if s.CondaEnv != "" {
		if r.CondaBase == "" {
			return nil, errors.New("没有配置 conda_base")
		}
		name = filepath.Join(r.CondaBase, "bin", "conda")
		if _, err := os.Stat(name); err != nil {
			name = filepath.Join(r.CondaBase, "condabin", "conda")
		}
		// 不在 <conda_base>/envs 下的环境以绝对路径作名字，conda 的 -n 不接受带 / 的值，要用 -p
		flag := "-n"
		if strings.HasPrefix(s.CondaEnv, "/") {
			flag = "-p"
		}
		args = []string{"list", flag, s.CondaEnv, "--export"}
	} else {
		name, args = "python3", []string{"-m", "pip", "freeze"}
	}
	cmd := execx.Command(ctx, name, args...)
	// 不带任务的身份标记：包清单可能比任务本身跑得久，带着标记的话，
	// 任务收尾清理残留进程时会把它当成任务进程杀掉，还会在备注里多记一个"已清理"
	for _, kv := range r.BuildEnv(baseEnv, s) {
		if !strings.HasPrefix(kv, JobMarker+"=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	out, err := execx.Output(cmd)
	if err != nil {
		return nil, fmt.Errorf("读取环境包列表失败: %w", err)
	}
	if len(out) > maxPackageList {
		return nil, fmt.Errorf("环境包列表超过 %d MiB", maxPackageList>>20)
	}
	return out, nil
}

// StorePackages 按内容哈希保存包列表；同一个清单只保留一份。
func (r *Runner) StorePackages(packages []byte) (string, error) {
	if r.EnvRoot == "" {
		return "", errors.New("没有配置环境包目录")
	}
	hash := sha256.Sum256(packages)
	name := hex.EncodeToString(hash[:])
	if err := os.MkdirAll(r.EnvRoot, 0o700); err != nil {
		return "", err
	}
	target := filepath.Join(r.EnvRoot, name+".txt")
	tmp, err := os.CreateTemp(r.EnvRoot, name+".tmp-")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", err
	}
	if _, err := tmp.Write(packages); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Link(tmpPath, target); err != nil {
		if !errors.Is(err, os.ErrExist) {
			return "", err
		}
		old, readErr := os.ReadFile(target)
		if readErr != nil {
			return "", readErr
		}
		if !bytes.Equal(old, packages) {
			return "", errors.New("环境包哈希文件内容不一致")
		}
	}
	return name, nil
}
