package qrutil

import (
	"errors"
	"fmt"
	"strings"

	"github.com/skip2/go-qrcode"
)

func Terminal(content string) (string, error) {
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return "", err
	}
	return code.ToSmallString(false), nil
}

func SVG(content string) ([]byte, error) {
	code, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, err
	}
	bitmap := code.Bitmap()
	if len(bitmap) == 0 {
		return nil, errors.New("二维码矩阵为空")
	}
	size := len(bitmap)
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="320" height="320" viewBox="0 0 %d %d" role="img" aria-labelledby="title" shape-rendering="crispEdges"><title id="title">丹炉手机接入二维码</title><rect width="100%%" height="100%%" fill="#fff"/><path fill="#171717" d="`, size, size)
	for y, row := range bitmap {
		if len(row) != len(bitmap) {
			return nil, errors.New("二维码矩阵不是正方形")
		}
		start := -1
		for x, dark := range row {
			if dark && start < 0 {
				start = x
			}
			if start >= 0 && (!dark || x == len(row)-1) {
				end := x
				if dark && x == len(row)-1 {
					end++
				}
				fmt.Fprintf(&b, "M%d %dh%dv1h-%dz", start, y, end-start, end-start)
				start = -1
			}
		}
	}
	b.WriteString(`"/></svg>`)
	return []byte(b.String()), nil
}
