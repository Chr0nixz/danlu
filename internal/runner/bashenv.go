package runner

import (
	"bytes"
	"errors"
)

const bashEnvMarker = "\x1eDANLU_ENV\x1f"

func parseBashEnvironment(output []byte) ([]string, error) {
	marker := []byte(bashEnvMarker)
	i := bytes.Index(output, marker)
	if i < 0 {
		return nil, errors.New("交互式 Bash 没有返回环境变量")
	}

	var env []string
	for _, entry := range bytes.Split(output[i+len(marker):], []byte{0}) {
		if k, _, ok := bytes.Cut(entry, []byte{'='}); ok && len(k) > 0 {
			env = append(env, string(entry))
		}
	}
	if len(env) == 0 {
		return nil, errors.New("从 ~/.bashrc 没有读取到环境变量")
	}
	return env, nil
}
