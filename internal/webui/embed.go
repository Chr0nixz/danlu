// Package webui 把构建好的前端（web/ 下 pnpm build 的产物）嵌入二进制。
package webui

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err)
	}
	return sub
}
