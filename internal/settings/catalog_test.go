package settings

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"danlu/internal/i18ntest"
)

// 范围与偏好校验错误按项编码（range_<name>[_zero]、pref_invalid_<name>），整句文案在 core 目录里，
// zh 必须与这里的原文逐字一致：改 specs/prefSpecs 的范围或标签时，这个测试提醒同步改目录。
func TestErrorCatalogMatchesServerText(t *testing.T) {
	zh := i18ntest.Core(t, "zh-CN")
	check := func(err error) {
		t.Helper()
		var ie *InputError
		if !errors.As(err, &ie) || ie.Code() == "" {
			t.Fatalf("期望带码的 InputError，得到 %v", err)
		}
		got, ok := zh["error."+ie.Code()]
		if !ok {
			t.Errorf("core 目录缺 error.%s（服务器原文：%s）", ie.Code(), ie.Error())
		} else if got != ie.Error() {
			t.Errorf("error.%s 与服务器原文不一致：\n  目录   %s\n  服务器 %s", ie.Code(), got, ie.Error())
		}
	}

	known := map[string]bool{}
	for _, s := range specs {
		err := validate(s, s.max+1)
		check(err)
		var ie *InputError
		errors.As(err, &ie)
		known[ie.Code()] = true
	}
	for _, p := range prefSpecs {
		_, err := p.parse(json.RawMessage(`"not-a-valid-value"`))
		check(err)
		var ie *InputError
		errors.As(err, &ie)
		known[ie.Code()] = true
	}

	// 反向：目录里不能留已经没有对应设置项的码
	for _, code := range i18ntest.ErrorKeys(zh) {
		if (strings.HasPrefix(code, "range_") || strings.HasPrefix(code, "pref_invalid_")) && !known[code] {
			t.Errorf("core 目录的 error.%s 已经没有对应的设置项", code)
		}
	}
}
