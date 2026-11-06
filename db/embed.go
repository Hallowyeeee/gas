// Package db 提供內嵌的資料庫結構。
package db

import _ "embed"

//go:embed schema.sql
var Schema string
