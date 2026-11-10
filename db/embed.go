// Package db 提供內嵌的資料庫結構。
package db

import _ "embed"

//go:embed schema.sql
var Schema string

// Legacy 是第 3 版：從舊系統（捷程 easy88）抓過來的資料。
//
//go:embed legacy.sql
var Legacy string

// Phones 是第 4 版：一位客戶可以有很多支電話。
//
//go:embed phones.sql
var Phones string

// Dispatch 是第 5 版：預約配送日期／時段、師傅排班、來電處理狀態、訂單常態備註。
//
//go:embed dispatch.sql
var Dispatch string

// Notify 是第 6 版：派單已讀、沒看到再提醒、每支手機通知有沒有收到。
//
//go:embed notify.sql
var Notify string
