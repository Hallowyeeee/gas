// Package web 內嵌前端網頁。
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// Static 回傳網頁檔案。
func Static() fs.FS {
	sub, _ := fs.Sub(files, "static")
	return sub
}
