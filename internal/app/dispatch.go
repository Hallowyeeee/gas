package app

import (
	"net/http"
	"time"
)

// ---------------------------------------------------------------------
// 派送：師傅排班、來電處理、常叫品項
// ---------------------------------------------------------------------

func validDate(s string) error {
	if _, err := time.Parse("2006-01-02", s); err != nil {
		return bad("日期格式錯誤")
	}
	return nil
}

// 排班：某一天每位師傅上午／下午／晚上有沒有班
func (a *App) listShifts(r *http.Request, u *User) (any, error) {
	date := r.URL.Query().Get("date")
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	if err := validDate(date); err != nil {
		return nil, err
	}
	return rows(r.Context(), a.DB, `SELECT driver_id, work_date, slot FROM driver_shifts WHERE work_date = ? ORDER BY driver_id`, date)
}

func (a *App) setShift(r *http.Request, u *User) (any, error) {
	var in struct {
		DriverID int64  `json:"driver_id"`
		WorkDate string `json:"work_date"`
		Slot     string `json:"slot"`
		On       bool   `json:"on"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if err := validDate(in.WorkDate); err != nil {
		return nil, err
	}
	if in.Slot != "am" && in.Slot != "pm" && in.Slot != "eve" {
		return nil, bad("時段只能是上午、下午、晚上")
	}
	ctx := r.Context()
	var role string
	if err := a.DB.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, in.DriverID).Scan(&role); err != nil || role != "driver" {
		return nil, bad("請選擇師傅")
	}
	var err error
	if in.On {
		_, err = a.DB.ExecContext(ctx, `INSERT OR IGNORE INTO driver_shifts (driver_id, work_date, slot) VALUES (?, ?, ?)`, in.DriverID, in.WorkDate, in.Slot)
	} else {
		_, err = a.DB.ExecContext(ctx, `DELETE FROM driver_shifts WHERE driver_id = ? AND work_date = ? AND slot = ?`, in.DriverID, in.WorkDate, in.Slot)
	}
	if err != nil {
		return nil, err
	}
	return rows(ctx, a.DB, `SELECT driver_id, work_date, slot FROM driver_shifts WHERE work_date = ? ORDER BY driver_id`, in.WorkDate)
}

// 複製排班：把某天的班表整份套到另一天（例如「照昨天排」）
func (a *App) copyShifts(r *http.Request, u *User) (any, error) {
	var in struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if err := validDate(in.From); err != nil {
		return nil, err
	}
	if err := validDate(in.To); err != nil {
		return nil, err
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM driver_shifts WHERE work_date = ?`, in.To); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO driver_shifts (driver_id, work_date, slot) SELECT driver_id, ?, slot FROM driver_shifts WHERE work_date = ?`, in.To, in.From); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return rows(ctx, a.DB, `SELECT driver_id, work_date, slot FROM driver_shifts WHERE work_date = ? ORDER BY driver_id`, in.To)
}

// 來電歷史：日期區間（預設今天）；unhandled=1 只列最近 3 天還沒處理的
func (a *App) listCallHistory(r *http.Request, u *User) (any, error) {
	q := r.URL.Query()
	today := time.Now().Format("2006-01-02")
	from, to := q.Get("from"), q.Get("to")
	if q.Get("unhandled") != "" {
		from, to = time.Now().AddDate(0, 0, -2).Format("2006-01-02"), today
	}
	if from == "" {
		from = today
	}
	if to == "" {
		to = from
	}
	if err := validDate(from); err != nil {
		return nil, err
	}
	if err := validDate(to); err != nil {
		return nil, err
	}
	list, err := rows(r.Context(), a.DB, `
		SELECT l.id, l.phone, l.source, l.received_at, l.customer_id, c.name AS customer_name, c.address,
		       l.handled_at, COALESCE(o.order_no,
		         (SELECT o2.order_no FROM orders o2 WHERE o2.customer_id = l.customer_id AND o2.ordered_at >= l.received_at
		            AND date(o2.ordered_at) = date(l.received_at) ORDER BY o2.id LIMIT 1)) AS order_no
		FROM call_logs l LEFT JOIN customers c ON c.id = l.customer_id LEFT JOIN orders o ON o.id = l.order_id
		WHERE date(l.received_at) BETWEEN ? AND ?
		ORDER BY l.id DESC LIMIT ?`, from, to, limitParam(r, 300))
	if err != nil || q.Get("unhandled") == "" {
		return list, err
	}
	// 未處理：沒下單、也沒按「已處理」；同一支號碼只留最新一通
	out := []map[string]any{}
	seen := map[string]bool{}
	for _, l := range list {
		p, _ := l["phone"].(string)
		if seen[p] {
			continue
		}
		seen[p] = true
		if l["handled_at"] == nil && l["order_no"] == nil {
			out = append(out, l)
		}
	}
	return out, nil
}

// 來電按「已處理」（回撥過、只是問問題…）：同一支號碼較早的未處理來電一起清掉
func (a *App) handleCall(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE call_logs SET handled_at = datetime('now','localtime')
		WHERE handled_at IS NULL AND phone = (SELECT phone FROM call_logs WHERE id = ?) AND id <= ?`, id, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("找不到這通來電，或已經處理過")
	}
	return nil, nil
}

// 常叫品項：最近 20 張單最常買的；回購：上一張單的品項
func (a *App) customerFavorites(r *http.Request, id int64) (map[string]any, error) {
	ctx := r.Context()
	fav, err := rows(ctx, a.DB, `
		SELECT oi.product_id, p.name, SUM(oi.qty) AS qty, COUNT(*) AS times
		FROM order_items oi JOIN products p ON p.id = oi.product_id
		WHERE oi.order_id IN (SELECT id FROM orders WHERE customer_id = ? AND status <> 'cancelled' ORDER BY id DESC LIMIT 20)
		  AND p.active = 1
		GROUP BY oi.product_id ORDER BY times DESC, qty DESC LIMIT 6`, id)
	if err != nil {
		return nil, err
	}
	last, err := row(ctx, a.DB, `SELECT id, order_no, total_amount, delivery_date, ordered_at FROM orders
		WHERE customer_id = ? AND status <> 'cancelled' ORDER BY id DESC LIMIT 1`, id)
	if err != nil {
		return nil, err
	}
	if last != nil {
		if last["items"], err = rows(ctx, a.DB, `SELECT oi.product_id, p.name, oi.qty FROM order_items oi
			JOIN products p ON p.id = oi.product_id WHERE oi.order_id = ? AND p.active = 1 ORDER BY oi.id`, last["id"]); err != nil {
			return nil, err
		}
	}
	return map[string]any{"frequent": fav, "last": last}, nil
}
