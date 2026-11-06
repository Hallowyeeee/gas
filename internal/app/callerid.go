package app

import (
	"context"
	"log"
	"net/http"
	"strings"
)

// ---------------------------------------------------------------------
// 來電顯示：盒子抓到號碼 → 找客戶 → 店裡電腦跳出客人資料
// ---------------------------------------------------------------------

// normalizePhone 只留數字；+886 / 886 開頭改回 0。
func normalizePhone(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	if strings.HasPrefix(d, "886") && len(d) >= 11 {
		d = "0" + d[3:]
	}
	return d
}

// samePhone：完全一樣，或一邊沒打區碼（例如 23456789 對 0223456789）。
func samePhone(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if a == b {
		return true
	}
	short, long := a, b
	if len(short) > len(long) {
		short, long = long, short
	}
	return len(short) >= 7 && strings.HasSuffix(long, short)
}

// findCallers 找出電話對得上的客戶（可能不只一位，例如同一家店兩個人）。
func (a *App) findCallers(ctx context.Context, phone string) ([]map[string]any, error) {
	list, err := rows(ctx, a.DB, `SELECT id, phone, phone2 FROM customers WHERE active = 1 AND (phone IS NOT NULL OR phone2 IS NOT NULL)`)
	if err != nil {
		return nil, err
	}
	var exact, loose []int64
	for _, c := range list {
		for _, k := range []string{"phone", "phone2"} {
			p, _ := c[k].(string)
			n := normalizePhone(p)
			if n == phone && n != "" {
				exact = append(exact, c["id"].(int64))
				break
			}
			if samePhone(n, phone) {
				loose = append(loose, c["id"].(int64))
				break
			}
		}
	}
	ids := exact
	if len(ids) == 0 {
		ids = loose
	}
	if len(ids) > 5 {
		ids = ids[:5]
	}
	out := []map[string]any{}
	for _, id := range ids {
		c, err := a.callerInfo(ctx, id)
		if err != nil {
			return nil, err
		}
		if c != nil {
			out = append(out, c)
		}
	}
	return out, nil
}

// callerInfo 跳出視窗要看的東西：基本資料、欠款、上次訂什麼、幾天沒叫、有沒有還沒送的單。
func (a *App) callerInfo(ctx context.Context, id int64) (map[string]any, error) {
	c, err := row(ctx, a.DB, `
		SELECT c.id, c.name, c.phone, c.phone2, c.address, c.floor, c.has_elevator, c.billing_type,
		       c.needs_invoice, c.tax_id, c.refill_days, c.note, c.default_driver_id, u.name AS default_driver_name,
		       COALESCE(b.balance, 0) AS balance
		FROM customers c
		LEFT JOIN users u ON u.id = c.default_driver_id
		LEFT JOIN v_customer_balance b ON b.customer_id = c.id
		WHERE c.id = ?`, id)
	if err != nil || c == nil {
		return c, err
	}
	last, err := row(ctx, a.DB, `
		SELECT o.id, o.order_no, o.status, o.total_amount, o.ordered_at, o.delivered_at, u.name AS driver_name,
		       CAST(julianday(date('now','localtime')) - julianday(date(COALESCE(o.delivered_at, o.ordered_at))) AS INTEGER) AS days_ago,
		       (SELECT group_concat(p.name || ' ×' || oi.qty, '、') FROM order_items oi JOIN products p ON p.id = oi.product_id WHERE oi.order_id = o.id) AS items
		FROM orders o LEFT JOIN users u ON u.id = o.driver_id
		WHERE o.customer_id = ? AND o.status <> 'cancelled'
		ORDER BY o.id DESC LIMIT 1`, id)
	if err != nil {
		return nil, err
	}
	if last == nil {
		// 還沒在新系統訂過：用舊系統最後一次送貨
		if last, err = a.legacyLast(ctx, id); err != nil {
			return nil, err
		}
	}
	c["last_order"] = last
	var open int64
	if err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM orders WHERE customer_id = ? AND status IN ('new','dispatched')`, id).Scan(&open); err != nil {
		return nil, err
	}
	c["open_orders"] = open
	return c, nil
}

// IncomingCall 有電話打進來（盒子或模擬）。回傳跳出視窗用的資料。
func (a *App) IncomingCall(ctx context.Context, raw, source string) (map[string]any, error) {
	phone := normalizePhone(raw)
	if len(phone) < 6 {
		return nil, bad("號碼太短：" + raw)
	}
	matches, err := a.findCallers(ctx, phone)
	if err != nil {
		return nil, err
	}
	var cid any
	if len(matches) > 0 {
		cid = matches[0]["id"]
	}
	res, err := a.DB.ExecContext(ctx, `INSERT INTO call_logs (phone, customer_id, source) VALUES (?, ?, ?)`, phone, cid, source)
	if err != nil {
		return nil, err
	}
	callID, _ := res.LastInsertId()
	call := map[string]any{"id": callID, "phone": phone, "source": source, "matches": matches}
	a.Events.Publish(0, Event{Type: "call", Call: call})
	if source == "box" && a.ShowWindow != nil {
		go a.ShowWindow()
	}
	log.Printf("來電 %s（%s），對到 %d 位客戶", phone, source, len(matches))
	return call, nil
}

// 設定頁「模擬來電」
func (a *App) testCall(r *http.Request, u *User) (any, error) {
	var in struct {
		Phone string `json:"phone"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	return a.IncomingCall(r.Context(), in.Phone, "test")
}

// 本機其他程式（例如來電顯示盒附的軟體）可以呼叫 http://localhost:8080/api/local/call?phone=0912345678
func (a *App) localCall(w http.ResponseWriter, r *http.Request) {
	if !isLocal(r) {
		writeErr(w, forbidden())
		return
	}
	phone := r.URL.Query().Get("phone")
	if phone == "" && r.Method == http.MethodPost {
		var in struct {
			Phone string `json:"phone"`
		}
		readJSON(r, &in)
		phone = in.Phone
	}
	call, err := a.IncomingCall(r.Context(), phone, "box")
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, call)
}

// 來電紀錄：某一天誰打來、有沒有下單
func (a *App) listCalls(r *http.Request, u *User) (any, error) {
	day := r.URL.Query().Get("date")
	if day == "" {
		day = "today"
	}
	return rows(r.Context(), a.DB, `
		SELECT l.id, l.phone, l.source, l.received_at, l.customer_id, c.name AS customer_name,
		       (SELECT o.order_no FROM orders o WHERE o.customer_id = l.customer_id AND o.ordered_at >= l.received_at
		          AND date(o.ordered_at) = date(l.received_at) ORDER BY o.id LIMIT 1) AS order_no
		FROM call_logs l LEFT JOIN customers c ON c.id = l.customer_id
		WHERE date(l.received_at) = CASE WHEN ? = 'today' THEN date('now','localtime') ELSE ? END
		ORDER BY l.id DESC LIMIT 300`, day, day)
}

// 查號碼（不留紀錄），給畫面重新整理用
func (a *App) lookupCaller(r *http.Request, u *User) (any, error) {
	phone := normalizePhone(r.URL.Query().Get("phone"))
	if phone == "" {
		return []any{}, nil
	}
	return a.findCallers(r.Context(), phone)
}
