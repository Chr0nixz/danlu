package strict

import (
	"errors"
	"strings"
	"testing"
)

// 经典 sudo 认 !requiretty，规则原样写入
func TestSudoersRuleKeepsRequirettyWhenAccepted(t *testing.T) {
	var tried []string
	rule, err := pickSudoersRule(1001, "alice", func(r string) ([]byte, error) {
		tried = append(tried, r)
		return nil, nil
	})
	if err != nil || len(tried) != 1 || !strings.Contains(rule, "Defaults!"+HelperPath+" !requiretty") {
		t.Fatalf("应只校验一次、保留 requiretty 一行: %v %q", err, rule)
	}
	if !strings.Contains(rule, "alice ALL=(root) NOPASSWD: "+HelperPath+" start, "+HelperPath+" check") {
		t.Fatalf("授权行不对: %q", rule)
	}
}

// sudo-rs 的 visudo 不认 requiretty（GitHub 的 Ubuntu 运行器上实际遇到）：去掉这一行再试，授权行不变
func TestSudoersRuleDropsRequirettyForSudoRs(t *testing.T) {
	sudoRs := func(r string) ([]byte, error) {
		if strings.Contains(r, "requiretty") {
			return []byte("/etc/sudoers.d/danlu-gpu-1001.tmp:3:47: syntax error: unknown setting: 'requiretty'"), errors.New("exit status 1")
		}
		return nil, nil
	}
	rule, err := pickSudoersRule(1001, "#1001", sudoRs)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(rule, "requiretty") || strings.Contains(rule, "Defaults") {
		t.Fatalf("应去掉 requiretty 一行: %q", rule)
	}
	if !strings.Contains(rule, "#1001 ALL=(root) NOPASSWD: "+HelperPath+" start, "+HelperPath+" check") {
		t.Fatalf("授权行不应变: %q", rule)
	}
}

// 别的原因校验失败不重试，原样报错；去掉一行后仍失败时两次的输出都带上
func TestSudoersRuleOtherErrors(t *testing.T) {
	calls := 0
	_, err := pickSudoersRule(1001, "alice", func(string) ([]byte, error) {
		calls++
		return []byte("syntax error near line 4"), errors.New("exit status 1")
	})
	if err == nil || calls != 1 || !strings.Contains(err.Error(), "near line 4") {
		t.Fatalf("其他错误应直接报出、不重试: %v（校验 %d 次）", err, calls)
	}

	_, err = pickSudoersRule(1001, "alice", func(r string) ([]byte, error) {
		if strings.Contains(r, "requiretty") {
			return []byte("unknown setting: 'requiretty'"), errors.New("exit status 1")
		}
		return []byte("unknown user"), errors.New("exit status 1")
	})
	if err == nil || !strings.Contains(err.Error(), "requiretty") || !strings.Contains(err.Error(), "unknown user") {
		t.Fatalf("两次都失败时应带上两次的输出: %v", err)
	}
}
