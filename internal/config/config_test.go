package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestResolvePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ResolvePath("~/danlu-outputs")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, "danlu-outputs"); got != want {
		t.Fatalf("ResolvePath = %q，期望 %q", got, want)
	}
	if _, err := ResolvePath("bad\x00path"); err == nil {
		t.Fatal("含 NUL 的路径应该拒绝")
	}
}

func TestFailFastConfig(t *testing.T) {
	for _, tc := range []struct {
		name, value string
		want        time.Duration
		invalid     bool
	}{
		{name: "旧配置使用两分钟默认值", want: 2 * time.Minute},
		{name: "关闭", value: "0s"},
		{name: "自定义时长", value: "45s", want: 45 * time.Second},
		{name: "负数", value: "-1s", invalid: true},
		{name: "无效时长", value: "bad", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := PathsFor(t.TempDir())
			text := "[server]\nport = 17699\nentrance = 'abcdef'\n[runtime]\n"
			if tc.value != "" {
				text += "fail_fast = '" + tc.value + "'\n"
			}
			if err := os.WriteFile(p.Config, []byte(text), 0o600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(p)
			if tc.invalid {
				if err == nil {
					t.Fatal("应拒绝无效的 fail_fast 配置")
				}
				return
			}
			if err != nil || c.Runtime.FailFast.Duration != tc.want {
				t.Fatalf("Load = %+v, %v，期望 fail_fast=%s", c, err, tc.want)
			}
			if err := Save(p, c); err != nil {
				t.Fatal(err)
			}
			got, err := Load(p)
			if err != nil || got.Runtime.FailFast.Duration != tc.want {
				t.Fatalf("保存后时长改变: %+v, %v", got, err)
			}
		})
	}
}

func TestDiskAndRetentionConfig(t *testing.T) {
	c := Default()
	c.Server.Port, c.Server.Entrance = 17699, "abcdef"
	if c.Runtime.DiskWarnGB != 20 || c.Runtime.DiskMinGB != 2 || c.Retention.KeepDays != 90 || c.Retention.MaxTotalGB != 50 {
		t.Fatal("磁盘与清理默认值不正确")
	}
	for _, target := range []*int64{&c.Runtime.DiskWarnGB, &c.Runtime.DiskMinGB, &c.Retention.KeepDays, &c.Retention.MaxTotalGB} {
		original := *target
		*target = -1
		if err := c.Validate(); err == nil {
			t.Fatal("负数阈值应被拒绝")
		}
		*target = 0
		if err := c.Validate(); err != nil {
			t.Fatalf("0 应允许关闭对应策略: %v", err)
		}
		*target = original
	}
}

func TestTerminalEnabledByDefaultAndCanBeDisabled(t *testing.T) {
	if !Default().Terminal.Enabled {
		t.Fatal("终端默认应启用")
	}
	p := PathsFor(t.TempDir())
	base := "[server]\nport = 17699\nentrance = 'abcdef'\n"
	if err := os.WriteFile(p.Config, []byte(base), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(p)
	if err != nil || !c.Terminal.Enabled {
		t.Fatalf("旧配置应默认启用终端: %+v, %v", c, err)
	}
	if err := os.WriteFile(p.Config, []byte(base+"[terminal]\nenabled = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = Load(p)
	if err != nil || c.Terminal.Enabled {
		t.Fatalf("应能通过配置关闭终端: %+v, %v", c, err)
	}
}
