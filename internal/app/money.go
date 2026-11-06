package app

import (
	"net/http"
	"time"
)

// ---------------------------------------------------------------------
// 收款
// ---------------------------------------------------------------------

func (a *App) listPayments(r *http.Request, u *User) (any, error) {
	q := r.URL.Query()
	return rows(r.Context(), a.DB, `
		SELECT p.*, c.name AS customer_name, o.order_no, u.name AS collected_by_name
		FROM payments p JOIN customers c ON c.id = p.customer_id
		LEFT JOIN orders o ON o.id = p.order_id LEFT JOIN users u ON u.id = p.collected_by
		WHERE (? = '' OR p.customer_id = ?) AND (? = '' OR date(p.received_at) = ?) AND (? = '' OR p.collected_by = ?)
		ORDER BY p.id DESC LIMIT ?`,
		q.Get("customer_id"), q.Get("customer_id"), q.Get("date"), q.Get("date"), q.Get("collected_by"), q.Get("collected_by"), limitParam(r, 300))
}

func (a *App) createPayment(r *http.Request, u *User) (any, error) {
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, []string{"customer_id", "order_id", "amount", "method", "collected_by", "received_at", "note"})
	if err != nil {
		return nil, err
	}
	if f["collected_by"] == nil {
		f["collected_by"] = u.ID
	}
	f["created_by"] = u.ID
	id, err := insertRow(r.Context(), a.DB, "payments", f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

func (a *App) deletePayment(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	res, err := a.DB.ExecContext(r.Context(), `DELETE FROM payments WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("找不到這筆收款")
	}
	return nil, nil
}

// ---------------------------------------------------------------------
// 師傅交帳
// ---------------------------------------------------------------------

func (a *App) listHandovers(r *http.Request, u *User) (any, error) {
	d := r.URL.Query().Get("date")
	return rows(r.Context(), a.DB, `SELECT h.*, u.name AS driver_name FROM cash_handovers h JOIN users u ON u.id = h.driver_id
		WHERE (? = '' OR h.work_date = ?) ORDER BY h.id DESC LIMIT ?`, d, d, limitParam(r, 300))
}

func (a *App) createHandover(r *http.Request, u *User) (any, error) {
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, []string{"driver_id", "work_date", "amount", "note"})
	if err != nil {
		return nil, err
	}
	if f["work_date"] == nil {
		f["work_date"] = time.Now().Format("2006-01-02")
	}
	f["received_by"] = u.ID
	id, err := insertRow(r.Context(), a.DB, "cash_handovers", f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

func (a *App) deleteHandover(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	_, err = a.DB.ExecContext(r.Context(), `DELETE FROM cash_handovers WHERE id = ?`, id)
	return nil, err
}

// ---------------------------------------------------------------------
// 押瓶
// ---------------------------------------------------------------------

func (a *App) listDeposits(r *http.Request, u *User) (any, error) {
	c := r.URL.Query().Get("customer_id")
	return rows(r.Context(), a.DB, `SELECT x.*, c.name AS customer_name, p.name AS cylinder_name, u.name AS collected_by_name
		FROM cylinder_deposits x JOIN customers c ON c.id = x.customer_id JOIN products p ON p.id = x.product_id
		LEFT JOIN users u ON u.id = x.collected_by
		WHERE (? = '' OR x.customer_id = ?) ORDER BY x.id DESC LIMIT ?`, c, c, limitParam(r, 300))
}

func (a *App) createDeposit(r *http.Request, u *User) (any, error) {
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, []string{"customer_id", "product_id", "qty_change", "amount", "method", "collected_by", "order_id", "receipt_no", "happened_at", "note"})
	if err != nil {
		return nil, err
	}
	if u.IsDriver() || f["collected_by"] == nil {
		f["collected_by"] = u.ID
	}
	f["created_by"] = u.ID
	id, err := insertRow(r.Context(), a.DB, "cylinder_deposits", f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

// ---------------------------------------------------------------------
// 進貨、付款給供應商
// ---------------------------------------------------------------------

func (a *App) listPurchases(r *http.Request, u *User) (any, error) {
	s := r.URL.Query().Get("supplier_id")
	return rows(r.Context(), a.DB, `SELECT p.*, s.name AS supplier_name,
			(SELECT group_concat(pr.name || '×' || pi.qty, '、') FROM purchase_items pi JOIN products pr ON pr.id = pi.product_id WHERE pi.purchase_id = p.id) AS summary
		FROM purchases p JOIN suppliers s ON s.id = p.supplier_id
		WHERE (? = '' OR p.supplier_id = ?) ORDER BY p.id DESC LIMIT ?`, s, s, limitParam(r, 200))
}

func (a *App) getPurchase(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	p, err := row(r.Context(), a.DB, `SELECT p.*, s.name AS supplier_name FROM purchases p JOIN suppliers s ON s.id = p.supplier_id WHERE p.id = ?`, id)
	if err != nil || p == nil {
		return nil, orNotFound(err, "找不到進貨單")
	}
	if p["items"], err = rows(r.Context(), a.DB, `SELECT pi.*, pr.name AS product_name FROM purchase_items pi JOIN products pr ON pr.id = pi.product_id WHERE pi.purchase_id = ?`, id); err != nil {
		return nil, err
	}
	return p, nil
}

func (a *App) createPurchase(r *http.Request, u *User) (any, error) {
	var in struct {
		SupplierID  int64   `json:"supplier_id"`
		PurchasedAt *string `json:"purchased_at"`
		InvoiceNo   *string `json:"invoice_no"`
		Note        *string `json:"note"`
		Items       []struct {
			ProductID       int64 `json:"product_id"`
			Qty             int64 `json:"qty"`
			UnitCost        int64 `json:"unit_cost"`
			EmptiesReturned int64 `json:"empties_returned"`
		} `json:"items"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if len(in.Items) == 0 {
		return nil, bad("進貨單至少要有一樣商品")
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `INSERT INTO purchases (supplier_id, purchased_at, invoice_no, note, created_by)
		VALUES (?, COALESCE(?, datetime('now','localtime')), ?, ?, ?)`, in.SupplierID, in.PurchasedAt, in.InvoiceNo, in.Note, u.ID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	for _, it := range in.Items {
		if _, err := tx.ExecContext(ctx, `INSERT INTO purchase_items (purchase_id, product_id, qty, unit_cost, amount, empties_returned) VALUES (?, ?, ?, ?, ?, ?)`,
			id, it.ProductID, it.Qty, it.UnitCost, it.Qty*it.UnitCost, it.EmptiesReturned); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

// 刪除進貨單：庫存自動扣回
func (a *App) deletePurchase(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE supplier_payments SET purchase_id = NULL WHERE purchase_id = ?`, id); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM purchase_items WHERE purchase_id = ?`, id); err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `DELETE FROM purchases WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("找不到進貨單")
	}
	return nil, tx.Commit()
}

func (a *App) listSupplierPayments(r *http.Request, u *User) (any, error) {
	s := r.URL.Query().Get("supplier_id")
	return rows(r.Context(), a.DB, `SELECT sp.*, s.name AS supplier_name FROM supplier_payments sp JOIN suppliers s ON s.id = sp.supplier_id
		WHERE (? = '' OR sp.supplier_id = ?) ORDER BY sp.id DESC LIMIT ?`, s, s, limitParam(r, 200))
}

func (a *App) createSupplierPayment(r *http.Request, u *User) (any, error) {
	return a.createSimple(r, "supplier_payments", []string{"supplier_id", "purchase_id", "amount", "method", "paid_at", "note"})
}

// ---------------------------------------------------------------------
// 庫存、換瓶提醒
// ---------------------------------------------------------------------

func (a *App) listStock(r *http.Request, u *User) (any, error) {
	return rows(r.Context(), a.DB, `SELECT * FROM v_stock ORDER BY category, name`)
}

func (a *App) adjustStock(r *http.Request, u *User) (any, error) {
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, []string{"product_id", "qty_change", "note"})
	if err != nil {
		return nil, err
	}
	f["reason"] = "adjust"
	id, err := insertRow(r.Context(), a.DB, "stock_movements", f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

func (a *App) listStockMovements(r *http.Request, u *User) (any, error) {
	p := r.URL.Query().Get("product_id")
	return rows(r.Context(), a.DB, `SELECT m.*, p.name AS product_name FROM stock_movements m JOIN products p ON p.id = m.product_id
		WHERE (? = '' OR m.product_id = ?) ORDER BY m.id DESC LIMIT ?`, p, p, limitParam(r, 300))
}

func (a *App) listRefillDue(r *http.Request, u *User) (any, error) {
	return rows(r.Context(), a.DB, `SELECT * FROM v_refill_due ORDER BY due_date`)
}

func (a *App) createRefillLog(r *http.Request, u *User) (any, error) {
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, []string{"customer_id", "action", "snooze_to", "note"})
	if err != nil {
		return nil, err
	}
	f["created_by"] = u.ID
	id, err := insertRow(r.Context(), a.DB, "refill_reminder_logs", f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

// ---------------------------------------------------------------------
// 報表
// ---------------------------------------------------------------------

func (a *App) report(r *http.Request, u *User) (any, error) {
	q := r.URL.Query()
	date, from, to, period := q.Get("date"), q.Get("from"), q.Get("to"), q.Get("period")
	ctx := r.Context()
	switch r.PathValue("name") {
	case "driver-daily":
		return rows(ctx, a.DB, `SELECT * FROM v_driver_daily WHERE (? = '' OR work_date = ?) ORDER BY work_date DESC, driver_name`, date, date)
	case "mismatch":
		return rows(ctx, a.DB, `SELECT * FROM v_driver_daily_mismatch WHERE (? = '' OR work_date >= ?) AND (? = '' OR work_date <= ?) ORDER BY work_date DESC`, from, from, to, to)
	case "customer-balance":
		return rows(ctx, a.DB, `SELECT * FROM v_customer_balance WHERE balance <> 0 ORDER BY balance DESC`)
	case "monthly":
		return rows(ctx, a.DB, `SELECT m.*, b.balance FROM v_monthly_statement m LEFT JOIN v_customer_balance b ON b.customer_id = m.customer_id
			WHERE (? = '' OR m.period = ?) ORDER BY m.period DESC, m.name`, period, period)
	case "supplier-balance":
		return rows(ctx, a.DB, `SELECT * FROM v_supplier_balance ORDER BY balance DESC`)
	case "profit":
		return rows(ctx, a.DB, `SELECT * FROM v_daily_profit WHERE (? = '' OR work_date >= ?) AND (? = '' OR work_date <= ?) ORDER BY work_date DESC, category`, from, from, to, to)
	case "deposits":
		return rows(ctx, a.DB, `SELECT * FROM v_customer_deposits WHERE cylinders_held <> 0 OR deposit_amount <> 0 ORDER BY customer_name`)
	case "deposit-total":
		return row(ctx, a.DB, `SELECT * FROM v_deposit_total`)
	case "unpaid-orders":
		return rows(ctx, a.DB, `SELECT v.*, c.name AS customer_name FROM v_order_payment v JOIN customers c ON c.id = v.customer_id
			WHERE v.status = 'delivered' AND v.billing_type = 'cash' AND v.unpaid_amount <> 0 ORDER BY v.delivered_date DESC`)
	case "audit":
		if u.Role != "admin" {
			return nil, forbidden()
		}
		return rows(ctx, a.DB, `SELECT * FROM audit_log ORDER BY id DESC LIMIT ?`, limitParam(r, 200))
	}
	return nil, notFound("沒有這個報表")
}
