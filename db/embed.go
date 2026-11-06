// Package db 提供內嵌的資料庫結構。
package db

import _ "embed"

//go:embed schema.sql
var Schema string

// Legacy 是第 3 版：從舊系統（捷程 easy88）抓過來的資料。
//
//go:embed legacy.sql
var Legacy string
