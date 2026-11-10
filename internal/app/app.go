// Package app 是瓦斯行系統的後端：HTTP API、登入、資料存取。
package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strconv"
	"strings"
	"sync"

	gasdb "github.com/hallowyeeee/gas/db"
	_ "modernc.org/sqlite"
)

// Version 由打包時填入。
var Version = "dev"

// App 是整個後端。
type App struct {
	DB     *sql.DB
	Tunnel *Tunnel
	Backup *Backup
	Static fs.FS

	Events *Hub

	// ShowWindow 把電腦上的程式視窗叫到最前面
	ShowWindow func()
	// OpenURL 用電腦預設程式打開網址（例如 line:// 打開 LINE 電腦版）
	OpenURL func(url string)
	// ExePath、Restart 給線上更新用
	ExePath string
	Restart func()
	upd     updater

	// OnDispatch 派單後呼叫（之後用來發手機通知）
	OnDispatch func(driverID int64, order map[string]any)

	mu sync.Mutex // 產生單號時避免重複

	callerBox *CallerBox
}

// OpenDB 開啟（必要時建立）資料庫。
func OpenDB(path string) (*sql.DB, error) {
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)"
	if path == ":memory:" {
		dsn = "file::memory:?_pragma=foreign_keys(1)"
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite 一次只讓一個連線寫入最穩，一家店的量完全夠用。
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func migrate(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='schema_version'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := db.Exec(gasdb.Schema); err != nil {
			return fmt.Errorf("建立資料庫失敗: %w", err)
		}
	}
	// 舊版資料庫逐版升級
	var ver int
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_version`).Scan(&ver); err != nil {
		return err
	}
	for i := ver; i < len(upgrades); i++ {
		if i == 2 {
			if err := ensureColumn(db, "customers", "legacy_id", "TEXT"); err != nil {
				return err
			}
		}
		if i == 3 { // 第 4 版：記單時選現金／轉帳
			if err := ensureColumn(db, "orders", "pay_method", "TEXT NOT NULL DEFAULT 'cash' CHECK (pay_method IN ('cash','transfer'))"); err != nil {
				return err
			}
		}
		if i == 4 { // 第 5 版：預約日期時段、來電處理、常態備註
			for _, c := range [][3]string{
				{"orders", "delivery_date", "TEXT"},
				{"orders", "delivery_slot", "TEXT CHECK (delivery_slot IS NULL OR delivery_slot IN ('am','pm','eve'))"},
				{"call_logs", "handled_at", "TEXT"},
				{"call_logs", "order_id", "INTEGER REFERENCES orders(id)"},
				{"customers", "order_note", "TEXT"},
			} {
				if err := ensureColumn(db, c[0], c[1], c[2]); err != nil {
					return err
				}
			}
		}
		if i == 5 { // 第 6 版：派單已讀、沒看到再提醒、通知有沒有收到
			for _, c := range [][3]string{
				{"orders", "seen_at", "TEXT"},
				{"orders", "reminded_at", "TEXT"},
				{"push_subscriptions", "platform", "TEXT"},
				{"push_subscriptions", "last_sent_at", "TEXT"},
				{"push_subscriptions", "last_ack_at", "TEXT"},
				{"push_subscriptions", "last_error", "TEXT"},
			} {
				if err := ensureColumn(db, c[0], c[1], c[2]); err != nil {
					return err
				}
			}
		}
		if _, err := db.Exec(upgrades[i]); err != nil {
			return fmt.Errorf("資料庫升級到第 %d 版失敗: %w", i+1, err)
		}
		if _, err := db.Exec(`UPDATE schema_version SET version = ?`, i+1); err != nil {
			return err
		}
	}
	return nil
}

// ensureColumn 欄位還沒有才加（ALTER TABLE 重複加會失敗）。
func ensureColumn(db *sql.DB, table, col, typ string) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, col).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	_, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, col, typ))
	return err
}

// upgrades[i] 把第 i 版升到第 i+1 版（第 0 格不用，新資料庫直接用 schema.sql）。
var upgrades = []string{
	0: "",
	1: `CREATE TABLE IF NOT EXISTS call_logs (
	  id          INTEGER PRIMARY KEY,
	  phone       TEXT    NOT NULL,
	  customer_id INTEGER REFERENCES customers(id),
	  source      TEXT    NOT NULL DEFAULT 'box' CHECK (source IN ('box','test')),
	  received_at TEXT    NOT NULL DEFAULT (datetime('now','localtime'))
	);
	CREATE INDEX IF NOT EXISTS idx_call_logs_time ON call_logs(received_at);`,
	2: gasdb.Legacy,
	3: gasdb.Phones,
	4: gasdb.Dispatch,
	5: gasdb.Notify,
}

// ---------------------------------------------------------------------
// 共用小工具
// ---------------------------------------------------------------------

type apiError struct {
	Status int
	Msg    string
}

func (e *apiError) Error() string { return e.Msg }

func bad(msg string) error      { return &apiError{http.StatusBadRequest, msg} }
func notFound(msg string) error { return &apiError{http.StatusNotFound, msg} }
func forbidden() error          { return &apiError{http.StatusForbidden, "沒有權限"} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, err error) {
	var ae *apiError
	if errors.As(err, &ae) {
		writeJSON(w, ae.Status, map[string]string{"error": ae.Msg})
		return
	}
	msg := err.Error()
	// 資料庫自己擋下來的錯誤（CHECK、trigger）屬於使用者輸入問題
	if strings.Contains(msg, "constraint failed") || strings.Contains(msg, "不能") {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": cleanDBError(msg)})
		return
	}
	writeJSON(w, http.StatusInternalServerError, map[string]string{"error": msg})
}

func cleanDBError(msg string) string {
	if i := strings.Index(msg, ": "); i >= 0 && strings.HasPrefix(msg, "SQL logic error") {
		msg = msg[i+2:]
	}
	msg = strings.TrimSuffix(msg, " (1811)")
	if strings.Contains(msg, "UNIQUE constraint failed") {
		return "資料重複：" + msg
	}
	return msg
}

// handler 讓每個 API 只要回傳 (結果, 錯誤)。
type handlerFunc func(r *http.Request, u *User) (any, error)

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return bad("資料格式錯誤：" + err.Error())
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, bad("編號錯誤")
	}
	return id, nil
}

func nilCtx() context.Context { return context.Background() }

// rows 把查詢結果轉成 []map，方便直接回傳 JSON。
func rows(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) ([]map[string]any, error) {
	rs, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	cols, err := rs.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rs.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rs.Scan(ptrs...); err != nil {
			return nil, err
		}
		m := make(map[string]any, len(cols))
		for i, c := range cols {
			if b, ok := vals[i].([]byte); ok {
				m[c] = string(b)
			} else {
				m[c] = vals[i]
			}
		}
		out = append(out, m)
	}
	return out, rs.Err()
}

func row(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) (map[string]any, error) {
	list, err := rows(ctx, q, query, args...)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list[0], nil
}

// filterFields 只留下允許寫入的欄位，並把 JSON 數字轉成 int64。
func filterFields(in map[string]any, allowed []string) (map[string]any, error) {
	out := map[string]any{}
	for _, k := range allowed {
		v, ok := in[k]
		if !ok {
			continue
		}
		switch t := v.(type) {
		case json.Number:
			n, err := t.Int64()
			if err != nil {
				return nil, bad(k + " 必須是整數")
			}
			out[k] = n
		case bool:
			if t {
				out[k] = int64(1)
			} else {
				out[k] = int64(0)
			}
		case string:
			if strings.TrimSpace(t) == "" {
				out[k] = nil
			} else {
				out[k] = strings.TrimSpace(t)
			}
		default:
			out[k] = v
		}
	}
	return out, nil
}

func insertRow(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, table string, fields map[string]any) (int64, error) {
	if len(fields) == 0 {
		return 0, bad("沒有資料")
	}
	cols := make([]string, 0, len(fields))
	marks := make([]string, 0, len(fields))
	args := make([]any, 0, len(fields))
	for k, v := range fields {
		cols = append(cols, k)
		marks = append(marks, "?")
		args = append(args, v)
	}
	res, err := tx.ExecContext(ctx, fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(cols, ","), strings.Join(marks, ",")), args...)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func updateRow(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, table string, id int64, fields map[string]any, extra string) error {
	if len(fields) == 0 {
		return bad("沒有要修改的資料")
	}
	sets := make([]string, 0, len(fields))
	args := make([]any, 0, len(fields)+1)
	for k, v := range fields {
		sets = append(sets, k+" = ?")
		args = append(args, v)
	}
	if extra != "" {
		sets = append(sets, extra)
	}
	args = append(args, id)
	res, err := tx.ExecContext(ctx, fmt.Sprintf("UPDATE %s SET %s WHERE id = ?", table, strings.Join(sets, ", ")), args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("找不到資料")
	}
	return nil
}

func num(v any) (int64, bool) {
	switch t := v.(type) {
	case json.Number:
		n, err := t.Int64()
		return n, err == nil
	case int64:
		return t, true
	case float64:
		return int64(t), t == float64(int64(t))
	}
	return 0, false
}
