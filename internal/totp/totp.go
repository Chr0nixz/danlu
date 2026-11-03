// Package totp 实现 RFC 6238 使用的 SHA-1、30 秒、6 位验证码。
package totp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const period = 30 * time.Second

var ErrSecret = errors.New("TOTP 密钥无效")

func GenerateSecret() (string, error) {
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw), nil
}

func ProvisioningURI(secret, account string) string {
	label := url.PathEscape("Danlu:" + account)
	return "otpauth://totp/" + label + "?secret=" + url.QueryEscape(secret) +
		"&issuer=Danlu&algorithm=SHA1&digits=6&period=30"
}

func Code(secret string, at time.Time) (string, int64, error) {
	key, err := decodeSecret(secret)
	if err != nil {
		return "", 0, err
	}
	step := at.Unix() / int64(period.Seconds())
	return codeAt(key, step), step, nil
}

// Verify 接受当前及前后各一个 30 秒时间窗。
func Verify(secret, supplied string, at time.Time) (int64, bool) {
	if len(supplied) != 6 {
		return 0, false
	}
	for _, c := range supplied {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	key, err := decodeSecret(secret)
	if err != nil {
		return 0, false
	}
	current := at.Unix() / int64(period.Seconds())
	for _, offset := range [...]int64{0, -1, 1} {
		step := current + offset
		if step < 0 {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(codeAt(key, step)), []byte(supplied)) == 1 {
			return step, true
		}
	}
	return 0, false
}

func decodeSecret(secret string) ([]byte, error) {
	secret = strings.ToUpper(strings.TrimSpace(secret))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil || len(key) < 16 {
		return nil, ErrSecret
	}
	return key, nil
}

func codeAt(key []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))
	h := hmac.New(sha1.New, key)
	_, _ = h.Write(counter[:])
	sum := h.Sum(nil)
	offset := sum[len(sum)-1] & 0x0f
	n := binary.BigEndian.Uint32(sum[offset:offset+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", n%1_000_000)
}
