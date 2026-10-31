//go:build linux

package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadBashrcEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("BASHRC_REMOVE_TEST", "remove-me")
	contents := "echo startup noise\nexport BASHRC_TEST_VALUE=loaded\nLOCAL_ONLY=hidden\nunset BASHRC_REMOVE_TEST\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := loadBashrcEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, entry := range env {
		key, value, ok := strings.Cut(entry, "=")
		if ok {
			values[key] = value
		}
	}
	if values["BASHRC_TEST_VALUE"] != "loaded" {
		t.Fatalf("没有读取到 .bashrc 导出的变量: %q", values["BASHRC_TEST_VALUE"])
	}
	if _, ok := values["LOCAL_ONLY"]; ok {
		t.Fatal("未导出的 shell 变量不应进入任务环境")
	}
	if _, ok := values["BASHRC_REMOVE_TEST"]; ok {
		t.Fatal(".bashrc 中 unset 的变量不应保留在任务环境")
	}
}
