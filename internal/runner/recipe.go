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
		args = []string{"list", "-n", s.CondaEnv, "--export"}
	} else {
		name, args = "python3", []string{"-m", "pip", "freeze"}
	}
	cmd := execx.Command(ctx, name, args...)
	cmd.Env = r.BuildEnv(baseEnv, s)
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
