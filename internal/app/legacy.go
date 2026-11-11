package app

import (
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------
// 從舊系統抓客戶資料：
// 瀏覽器（書籤小工具）按 F12 → 把那一頁送到 /api/local/capture
// → 讀出資料、排進「待確認」→ 店裡電腦跳出確認視窗 → 按 Enter 才存。
// ---------------------------------------------------------------------

const maxCaptureBytes = 8 << 20

// 預設只收捷程 easy88 網頁送來的資料；設定 legacy_hosts 可以改（逗號分開）
const defaultLegacyHosts = "easy88.com.tw"

func (a *App) legacyOriginOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" || origin == "null" {
		return true // 不是從網頁送來的（例如本機程式），isLocal 已經擋掉外面
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "127.0.0.1" {
		return true
	}
	hosts := a.setting(r, "legacy_hosts", defaultLegacyHosts)
	for _, h := range strings.Split(hosts, ",") {
		h = strings.ToLower(strings.TrimSpace(h))
		if h != "" && (host == h || strings.HasSuffix(host, "."+h)) {
			return true
		}
	}
	return false
}

// localCapture：書籤小工具送來的網頁（只收這台電腦、只收舊系統網址）。
func (a *App) localCapture(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && a.legacyOriginOK(r) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
	}
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if !isLocal(r) || !a.legacyOriginOK(r) {
		writeErr(w, forbidden())
		return
	}
	// 書籤小工具用表單送到小視窗（舊系統是 http 網頁，Chrome 不讓它直接連本機，只能用開視窗的方式）
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/x-www-form-urlencoded") ||
		strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		r.Body = http.MaxBytesReader(w, r.Body, maxCaptureBytes)
		if err := r.ParseForm(); err != nil {
			writeCaptureResult(w, nil, bad("讀取失敗："+err.Error()))
			return
		}
		res, err := a.captureFromForm(r.Context(), r.PostForm)
		writeCaptureResult(w, res, err)
		return
	}
	var in struct {
		URL  string `json:"url"`
		HTML string `json:"html"`
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxCaptureBytes))
	if err != nil {
		writeErr(w, bad("讀取失敗"))
		return
	}
	if err := json.Unmarshal(body, &in); err != nil || in.HTML == "" {
		writeErr(w, bad("沒有收到網頁內容"))
		return
	}
	res, err := a.addCapture(r.Context(), in.URL, in.HTML)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, res)
}

// captureFromForm：書籤小工具可能送來好幾個框架的網頁（enc=uri 表示有編碼過，避免中文編碼問題），
// 找出讀得懂的那一頁；都讀不懂就列出收到哪些頁，方便查原因。
func (a *App) captureFromForm(ctx context.Context, f url.Values) (map[string]any, error) {
	pages, urls, titles := f["html"], f["url"], f["title"]
	if len(pages) == 0 {
		return nil, bad("沒有收到網頁內容")
	}
	var diag []string
	for i, page := range pages {
		if f.Get("enc") == "uri" {
			if dec, err := url.QueryUnescape(strings.ReplaceAll(page, "+", "%2B")); err == nil {
				page = dec
			}
		}
		src, title := "", ""
		if i < len(urls) {
			src = urls[i]
		}
		if i < len(titles) {
			title = titles[i]
		}
		if _, err := ParseLegacyPage(page); err == nil {
			return a.addCapture(ctx, src, page)
		}
		short := src
		if j := strings.Index(short, "?"); j > 0 {
			short = short[:j]
		}
		if j := strings.LastIndex(short, "/"); j >= 0 {
			short = short[j+1:]
		}
		diag = append(diag, fmt.Sprintf("%s〔%s〕%d 字", short, title, len([]rune(page))))
	}
	log.Println("抓舊資料失敗，收到：", strings.Join(diag, "；"))
	return nil, bad(errNotCustomerPage.Error() + "（收到：" + strings.Join(diag, "；") + "）")
}

// writeCaptureResult 小視窗顯示結果，成功的話 1.5 秒後自己關掉。
func writeCaptureResult(w http.ResponseWriter, res map[string]any, err error) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	msg, color, closeJS := "", "#2b8a3e", "setTimeout(function(){window.close()},1500)"
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		msg, color, closeJS = "✗ "+err.Error(), "#c92a2a", "setTimeout(function(){window.close()},8000)"
	} else {
		msg = fmt.Sprintf("✓ 已送到瓦斯行：%v（送貨紀錄 %v 筆）", res["name"], res["deliveries"])
	}
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><title>瓦斯行</title></head>
<body style="margin:0;display:flex;align-items:center;justify-content:center;height:100vh;background:%s;color:#fff;font:bold 20px sans-serif;text-align:center;padding:12px;box-sizing:border-box">%s
<script>%s</script></body></html>`, color, html.EscapeString(msg), closeJS)
}

// uploadCapture：設定頁「上傳存下來的網頁檔」（Ctrl+S 存的 .htm）。
func (a *App) uploadCapture(r *http.Request, u *User) (any, error) {
	var in struct {
		HTML string `json:"html"`
	}
	r.Body = http.MaxBytesReader(nil, r.Body, maxCaptureBytes)
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	return a.addCapture(r.Context(), "upload", in.HTML)
}

// addCapture 讀網頁、排進待確認，叫店裡電腦跳出確認視窗。
func (a *App) addCapture(ctx context.Context, src, page string) (map[string]any, error) {
	p, err := ParseLegacyPage(page)
	if err != nil {
		return nil, bad(err.Error())
	}
	parsed, _ := json.Marshal(p)
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write([]byte(page))
	zw.Close()

	// 同一位客人還沒確認又按一次：用新的取代舊的
	var id int64
	err = a.DB.QueryRowContext(ctx, `SELECT id FROM legacy_captures WHERE status = 'pending' AND legacy_id = ? ORDER BY id DESC LIMIT 1`, p.LegacyID).Scan(&id)
	switch {
	case err == sql.ErrNoRows:
		res, err := a.DB.ExecContext(ctx, `INSERT INTO legacy_captures (legacy_id, name, phone, source_url, parsed, raw_gz) VALUES (?, ?, ?, ?, ?, ?)`,
			p.LegacyID, p.Name, p.Phone, src, string(parsed), gz.Bytes())
		if err != nil {
			return nil, err
		}
		id, _ = res.LastInsertId()
	case err != nil:
		return nil, err
	default:
		if _, err := a.DB.ExecContext(ctx, `UPDATE legacy_captures SET name = ?, phone = ?, source_url = ?, parsed = ?, raw_gz = ?, captured_at = datetime('now','localtime') WHERE id = ?`,
			p.Name, p.Phone, src, string(parsed), gz.Bytes(), id); err != nil {
			return nil, err
		}
	}
	a.Events.Publish(0, Event{Type: "capture", Message: p.Name, Capture: map[string]any{"id": id, "name": p.Name, "legacy_id": p.LegacyID}})
	if a.ShowWindow != nil {
		go a.ShowWindow()
	}
	log.Printf("抓到舊系統客戶 %s %s，送貨紀錄 %d 筆", p.LegacyID, p.Name, len(p.Deliveries))
	return map[string]any{"ok": true, "id": id, "name": p.Name, "legacy_id": p.LegacyID, "deliveries": len(p.Deliveries)}, nil
}

// 待確認清單
func (a *App) listCaptures(r *http.Request, u *User) (any, error) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	return rows(r.Context(), a.DB, `SELECT l.id, l.legacy_id, l.name, l.phone, l.status, l.customer_id, l.captured_at, l.done_at, c.name AS customer_name
		FROM legacy_captures l LEFT JOIN customers c ON c.id = l.customer_id
		WHERE l.status = ? ORDER BY l.id DESC LIMIT 200`, status)
}

// 一筆抓到的資料＋新系統裡對得上的客戶（給確認視窗用）
func (a *App) getCapture(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	cap, p, err := a.loadCapture(ctx, id)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"id": id, "status": cap["status"], "captured_at": cap["captured_at"], "page": p}
	// 先用舊系統編號找，再用電話找
	var matches []map[string]any
	if c, err := row(ctx, a.DB, `SELECT * FROM customers WHERE legacy_id = ?`, p.LegacyID); err != nil {
		return nil, err
	} else if c != nil {
		c["match_by"] = "舊系統編號"
		matches = append(matches, c)
	}
	if len(matches) == 0 {
		for _, ph := range []string{p.Phone, p.Phone2} {
			n := normalizePhone(ph)
			if len(n) < 6 {
				continue
			}
			found, err := a.findCallers(ctx, n)
			if err != nil {
				return nil, err
			}
			for _, f := range found {
				c, err := row(ctx, a.DB, `SELECT * FROM customers WHERE id = ?`, f["id"])
				if err != nil {
					return nil, err
				}
				if c != nil && !hasID(matches, c["id"]) {
					c["match_by"] = "電話"
					matches = append(matches, c)
				}
			}
		}
	}
	if matches == nil {
		matches = []map[string]any{}
	}
	out["matches"] = matches
	out["products"], err = a.legacyPriceProducts(ctx, p.Prices)
	if err != nil {
		return nil, err
	}
	var saved int64
	a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_deliveries d JOIN customers c ON c.id = d.customer_id WHERE c.legacy_id = ?`, p.LegacyID).Scan(&saved)
	out["saved_deliveries"] = saved
	return out, nil
}

func hasID(list []map[string]any, id any) bool {
	for _, m := range list {
		if m["id"] == id {
			return true
		}
	}
	return false
}

func (a *App) loadCapture(ctx context.Context, id int64) (map[string]any, *LegacyPage, error) {
	cap, err := row(ctx, a.DB, `SELECT id, status, parsed, captured_at FROM legacy_captures WHERE id = ?`, id)
	if err != nil {
		return nil, nil, err
	}
	if cap == nil {
		return nil, nil, notFound("找不到這筆抓取資料")
	}
	var p LegacyPage
	if err := json.Unmarshal([]byte(cap["parsed"].(string)), &p); err != nil {
		return nil, nil, err
	}
	return cap, &p, nil
}

// legacyPriceProducts：舊系統各規格售價對到新系統哪個瓦斯商品（名稱裡有「20kg」這種字、而且只有一個）。
func (a *App) legacyPriceProducts(ctx context.Context, prices map[string]int) (map[string]any, error) {
	gas, err := rows(ctx, a.DB, `SELECT id, name, sale_price FROM products WHERE category = 'gas' AND active = 1`)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for size := range prices {
		var hit map[string]any
		n := 0
		key := strings.ToLower(size)
		for _, g := range gas {
			if strings.Contains(strings.ToLower(squash(g["name"].(string))), key) {
				hit = g
				n++
			}
		}
		if n == 1 {
			out[size] = hit
		}
	}
	return out, nil
}

// cancelCapture 不存
func (a *App) cancelCapture(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	_, err = a.DB.ExecContext(r.Context(), `UPDATE legacy_captures SET status = 'cancelled', done_at = datetime('now','localtime') WHERE id = ? AND status = 'pending'`, id)
	return map[string]bool{"ok": true}, err
}

// legacyCustomerFields 確認視窗可以改的客戶欄位
var legacyCustomerFields = []string{"name", "phone", "phone2", "address", "area", "needs_invoice", "tax_id", "note"}

// saveCapture 確認存檔：客戶資料、桶子結餘、舊送貨紀錄、出桶鋼瓶、特價、換瓶天數。
func (a *App) saveCapture(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	cap, p, err := a.loadCapture(ctx, id)
	if err != nil {
		return nil, err
	}
	if cap["status"] != "pending" {
		return nil, bad("這筆已經處理過了")
	}
	custIn, _ := in["customer"].(map[string]any)
	fields, err := filterFields(custIn, legacyCustomerFields)
	if err != nil {
		return nil, err
	}
	if fields["name"] == nil {
		return nil, bad("名字一定要有")
	}
	if fields["address"] == nil {
		fields["address"] = "（舊系統沒有地址）" // 地址欄不能空，先放這句，之後補
	}
	targetID, _ := num(in["customer_id"]) // 0 = 新客戶

	backup := a.legacyBackupOnce(ctx)

	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	// 這個舊系統編號如果已經掛在別的客戶身上，先拿下來（以這次選的為準）
	if _, err := tx.ExecContext(ctx, `UPDATE customers SET legacy_id = NULL WHERE legacy_id = ? AND id <> ?`, p.LegacyID, targetID); err != nil {
		return nil, err
	}
	fields["legacy_id"] = p.LegacyID
	created := false
	if targetID == 0 {
		targetID, err = insertRow(ctx, tx, "customers", fields)
		if err != nil {
			return nil, err
		}
		created = true
	} else if err := updateRow(ctx, tx, "customers", targetID, fields, "updated_at = datetime('now','localtime')"); err != nil {
		return nil, err
	}

	// 「客戶編號」空著就直接用舊系統編號（已經有填、或別的客人用掉了就不動）
	if _, err := tx.ExecContext(ctx, `UPDATE customers SET code = ? WHERE id = ? AND (code IS NULL OR code = '')
		AND NOT EXISTS (SELECT 1 FROM customers WHERE code = ? AND id <> ?)`, p.LegacyID, targetID, p.LegacyID, targetID); err != nil {
		return nil, err
	}

	// 舊系統其他資訊
	prices, _ := json.Marshal(p.Prices)
	totals, _ := json.Marshal(p.Totals)
	if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_customer_info (customer_id, legacy_id, cylinder_type, prices, totals) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (customer_id) DO UPDATE SET legacy_id = excluded.legacy_id, cylinder_type = excluded.cylinder_type,
		  prices = excluded.prices, totals = excluded.totals, captured_at = datetime('now','localtime')`,
		targetID, p.LegacyID, nullIfEmpty(p.CylinderType), string(prices), string(totals)); err != nil {
		return nil, err
	}

	// 桶子結餘：用這次看到的數字整個換掉
	if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_cylinder_balance WHERE customer_id = ?`, targetID); err != nil {
		return nil, err
	}
	for size, qty := range p.Balance {
		if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_cylinder_balance (customer_id, size, qty) VALUES (?, ?, ?)`, targetID, size, qty); err != nil {
			return nil, err
		}
	}

	// 舊送貨紀錄：同一個時間的視為同一筆（再按一次 F12 只會補新的、更新舊的）
	added := 0
	for _, d := range p.Deliveries {
		var existed int
		tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM legacy_deliveries WHERE customer_id = ? AND delivered_at = ?`, targetID, d.At).Scan(&existed)
		var did int64
		err := tx.QueryRowContext(ctx, `INSERT INTO legacy_deliveries (customer_id, delivered_at, items, stored_gas, amount, paid, cylinder, pay_status, driver_name, position, note)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (customer_id, delivered_at) DO UPDATE SET items = excluded.items, stored_gas = excluded.stored_gas, amount = excluded.amount,
			  paid = excluded.paid, cylinder = excluded.cylinder, pay_status = excluded.pay_status, driver_name = excluded.driver_name,
			  position = excluded.position, note = excluded.note
			RETURNING id`,
			targetID, d.At, d.ItemsText(), d.StoredGas, d.Amount, d.Paid, nullIfEmpty(d.Cylinder), nullIfEmpty(d.PayStatus),
			nullIfEmpty(d.Driver), nullIfEmpty(d.Position), nullIfEmpty(d.Note)).Scan(&did)
		if err != nil {
			return nil, err
		}
		if existed == 0 {
			added++
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_delivery_items WHERE delivery_id = ?`, did); err != nil {
			return nil, err
		}
		for _, it := range d.Items {
			if _, err := tx.ExecContext(ctx, `INSERT INTO legacy_delivery_items (delivery_id, size, qty, unit_price) VALUES (?, ?, ?, ?)
				ON CONFLICT (delivery_id, size) DO UPDATE SET qty = qty + excluded.qty`, did, it.Size, it.Qty, it.Price); err != nil {
				return nil, err
			}
		}
	}

	// 出桶鋼瓶：用這次看到的清單整個換掉
	if _, err := tx.ExecContext(ctx, `DELETE FROM legacy_cylinders_out WHERE customer_id = ?`, targetID); err != nil {
		return nil, err
	}
	for _, c := range p.CylindersOut {
		if _, err := tx.ExecContext(ctx, `INSERT OR REPLACE INTO legacy_cylinders_out (customer_id, barcode, cylinder_no, size, next_inspection, age_years, driver_name, out_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, targetID, c.Barcode, nullIfEmpty(c.No), nullIfEmpty(c.Size), nullIfEmpty(c.NextInspection), c.AgeYears,
			nullIfEmpty(c.Driver), nullIfEmpty(c.OutAt)); err != nil {
			return nil, err
		}
	}

	// 特價：只存確認視窗有勾的（跟商品原價不一樣的）
	priceCount := 0
	if list, ok := in["prices"].([]any); ok {
		for _, x := range list {
			m, _ := x.(map[string]any)
			pid, ok1 := num(m["product_id"])
			price, ok2 := num(m["price"])
			if !ok1 || !ok2 || price < 0 {
				continue
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO customer_prices (customer_id, product_id, price) VALUES (?, ?, ?)
				ON CONFLICT (customer_id, product_id) DO UPDATE SET price = excluded.price`, targetID, pid, price); err != nil {
				return nil, err
			}
			priceCount++
		}
	}

	// 換瓶天數：還沒填的話，用舊送貨紀錄算
	refill := 0
	var cur sql.NullInt64
	tx.QueryRowContext(ctx, `SELECT refill_days FROM customers WHERE id = ?`, targetID).Scan(&cur)
	if !cur.Valid {
		var dates []string
		rs, err := tx.QueryContext(ctx, `SELECT DISTINCT date(delivered_at) FROM legacy_deliveries d
			WHERE customer_id = ? AND EXISTS (SELECT 1 FROM legacy_delivery_items i WHERE i.delivery_id = d.id) ORDER BY 1`, targetID)
		if err != nil {
			return nil, err
		}
		for rs.Next() {
			var s string
			rs.Scan(&s)
			dates = append(dates, s)
		}
		rs.Close()
		if refill = typicalInterval(dates); refill > 0 {
			if _, err := tx.ExecContext(ctx, `UPDATE customers SET refill_days = ? WHERE id = ?`, refill, targetID); err != nil {
				return nil, err
			}
		}
	}

	if _, err := tx.ExecContext(ctx, `UPDATE legacy_captures SET status = 'saved', customer_id = ?, done_at = datetime('now','localtime') WHERE id = ?`, targetID, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	a.backfillCalls(ctx, targetID)
	log.Printf("舊系統客戶 %s 已存成客戶 #%d（新增送貨紀錄 %d 筆）", p.LegacyID, targetID, added)
	return map[string]any{"ok": true, "customer_id": targetID, "created": created, "deliveries_added": added,
		"deliveries_total": len(p.Deliveries), "prices": priceCount, "refill_days": refill, "backup": backup}, nil
}

func nullIfEmpty(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// typicalInterval 叫瓦斯的間隔天數（中位數）；紀錄太少回 0。
// 只看最近兩年，太久以前的習慣可能不一樣。
func typicalInterval(dates []string) int {
	var ts []time.Time
	for _, d := range dates {
		if t, err := time.Parse("2006-01-02", d); err == nil {
			ts = append(ts, t)
		}
	}
	if len(ts) < 3 {
		return 0
	}
	cut := ts[len(ts)-1].AddDate(-2, 0, 0)
	var gaps []int
	for i := 1; i < len(ts); i++ {
		if ts[i-1].Before(cut) {
			continue
		}
		g := int(ts[i].Sub(ts[i-1]).Hours() / 24)
		if g > 0 {
			gaps = append(gaps, g)
		}
	}
	if len(gaps) < 2 {
		return 0
	}
	sort.Ints(gaps)
	return gaps[len(gaps)/2]
}

// legacyBackupOnce：每天第一次存舊資料前先備份一次。回傳備份檔位置（今天已經備份過就空白）。
func (a *App) legacyBackupOnce(ctx context.Context) string {
	if a.Backup == nil {
		return ""
	}
	today := time.Now().Format("2006-01-02")
	var last string
	a.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'legacy_backup_day'`).Scan(&last)
	if last == today {
		return ""
	}
	path, err := a.Backup.Now()
	if err != nil {
		log.Println("匯入前備份失敗：", err)
		return ""
	}
	a.DB.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES ('legacy_backup_day', ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, today)
	return path
}

// customerLegacy 客戶頁要看的舊資料
func (a *App) customerLegacy(ctx context.Context, id int64) (map[string]any, error) {
	out := map[string]any{}
	info, err := row(ctx, a.DB, `SELECT legacy_id, cylinder_type, prices, totals, captured_at FROM legacy_customer_info WHERE customer_id = ?`, id)
	if err != nil {
		return nil, err
	}
	if info == nil {
		return nil, nil
	}
	for _, k := range []string{"prices", "totals"} {
		var m map[string]int
		if s, ok := info[k].(string); ok {
			json.Unmarshal([]byte(s), &m)
		}
		info[k] = m
	}
	out["info"] = info
	if out["balance"], err = rows(ctx, a.DB, `SELECT size, qty FROM legacy_cylinder_balance WHERE customer_id = ? ORDER BY size`, id); err != nil {
		return nil, err
	}
	if out["deliveries"], err = rows(ctx, a.DB, `SELECT delivered_at, items, stored_gas, amount, cylinder, pay_status, driver_name, note
		FROM legacy_deliveries WHERE customer_id = ? ORDER BY delivered_at DESC`, id); err != nil {
		return nil, err
	}
	if out["cylinders_out"], err = rows(ctx, a.DB, `SELECT barcode, cylinder_no, size, next_inspection, age_years, driver_name, out_at,
		CAST(julianday('now','localtime') - julianday(out_at) AS INTEGER) AS days
		FROM legacy_cylinders_out WHERE customer_id = ? ORDER BY out_at`, id); err != nil {
		return nil, err
	}
	return out, nil
}

// legacyLast 來電視窗用：沒有新系統訂單時，顯示舊系統最後一次送貨（欄位跟 last_order 一樣）。
func (a *App) legacyLast(ctx context.Context, id int64) (map[string]any, error) {
	return row(ctx, a.DB, `SELECT delivered_at, delivered_at AS ordered_at, items, amount AS total_amount, driver_name, 1 AS legacy,
		CAST(julianday(date('now','localtime')) - julianday(date(delivered_at)) AS INTEGER) AS days_ago
		FROM legacy_deliveries WHERE customer_id = ? ORDER BY delivered_at DESC LIMIT 1`, id)
}
