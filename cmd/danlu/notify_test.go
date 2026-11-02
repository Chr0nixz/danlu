package main

import (
	"strings"
	"testing"
)

func TestNotifyTopicSetup(t *testing.T) {
	random, err := notifyTopicURL("", "", "", false)
	if err != nil || !strings.HasPrefix(random, "https://ntfy.sh/danlu-") || len(strings.TrimPrefix(random, "https://ntfy.sh/")) < 30 {
		t.Fatalf("随机订阅地址不正确: %q %v", random, err)
	}
	for _, tc := range []struct {
		existing, server, topic string
		rotate                  bool
		want                    string
	}{
		{"https://host/prefix/original", "", "", false, "https://host/prefix/original"},
		{"https://host/prefix/original", "", "new", false, "https://host/prefix/new"},
		{"", "http://host/ntfy/", "topic", false, "http://host/ntfy/topic"},
	} {
		got, err := notifyTopicURL(tc.existing, tc.server, tc.topic, tc.rotate)
		if err != nil || got != tc.want {
			t.Fatalf("地址拼接错误: %q %v", got, err)
		}
	}
	rotated, err := notifyTopicURL(random, "", "", true)
	if err != nil || random == rotated {
		t.Fatal("主动轮换应更换随机 topic")
	}
	if _, err := notifyTopicURL("", "https://ntfy.sh?token=secret", "topic", false); err == nil {
		t.Fatal("不接受服务器查询参数")
	}
}
