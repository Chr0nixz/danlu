package config

import "testing"

func TestNotifyValidation(t *testing.T) {
	for _, raw := range []string{"https://ntfy.sh/danlu-abc_123", "http://127.0.0.1:17713/topic", "https://example.com/ntfy/topic"} {
		if err := (Notify{NTFY: raw, Token: "tk_123"}).Validate(); err != nil {
			t.Fatalf("应接受配置: %v", err)
		}
	}
	for _, raw := range []string{"file:///secret", "https://ntfy.sh/", "https://u:p@ntfy.sh/topic", "https://ntfy.sh/topic?token=secret", "https://ntfy.sh/topic#secret", "https://ntfy.sh/bad%2Ftopic", "https://ntfy.sh/topic\n", "https://ntfy.sh/中文"} {
		if err := (Notify{NTFY: raw}).Validate(); err == nil {
			t.Fatalf("应拒绝无效地址: %q", raw)
		}
	}
	if err := (Notify{Token: "bad\nheader"}).Validate(); err == nil {
		t.Fatal("不应接受含换行的令牌")
	}
}
