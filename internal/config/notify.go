package config

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

type Notify struct {
	NTFY  string `toml:"ntfy"`
	Token string `toml:"token,omitempty"`
}

var topicPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// ParseNTFY 不在错误里复述地址：topic 本身就是订阅密钥。
func ParseNTFY(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u == nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" ||
		u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || strings.ContainsAny(raw, "\r\n\t ") {
		return nil, errors.New("notify.ntfy 必须是 HTTP(S) topic 地址，不能包含用户名、查询参数或片段")
	}
	parts := strings.Split(u.Path, "/")
	if len(parts) < 2 || !topicPattern.MatchString(parts[len(parts)-1]) || u.RawPath != "" {
		return nil, errors.New("ntfy topic 需要 1–64 位字母、数字、下划线或连字符，地址不能以斜杠结尾")
	}
	return u, nil
}

func (n Notify) Validate() error {
	if len(n.Token) > 4096 || strings.ContainsAny(n.Token, "\r\n") {
		return errors.New("notify.token 不能包含换行，长度不能超过 4096 字节")
	}
	if n.NTFY == "" {
		return nil
	}
	_, err := ParseNTFY(n.NTFY)
	return err
}
