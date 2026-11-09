package app

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"time"
)

type itemIn struct {
	ProductID       int64  `json:"product_id"`
	Qty             int64  `json:"qty"`
	UnitPrice       *int64 `json:"unit_price"`
	Discount        int64  `json:"discount"`
	EmptiesReturned int64  `json:"empties_returned"`
}

type orderIn struct {
	CustomerID   int64    `json:"customer_id"`
	Items        []itemIn `json:"items"`
	Note         *string  `json:"note"`
	Address      *string  `json:"address"`
	Phone        *string  `json:"phone"`
	DriverID     *int64   `json:"driver_id"`
	NeedsInvoice *bool    `json:"needs_invoice"`
	InvoiceNo    *string  `json:"invoice_no"`
	BillingType  *string  `json:"billing_type"`
	PayMethod    *string  `json:"pay_method"` // 現金 cash／轉帳 transfer
}

func payMethod(p *string) (string, error) {
	if p == nil || *p == "" {
		return "cash", nil
	}
	if *p != "cash" && *p != "transfer" {
		return "", bad("付款方式只能是現金或轉帳")
	}
	return *p, nil
}

func (a *App) nextOrderNo(ctx context.Context, tx *sql.Tx) (string, error) {
	prefix := time.Now().Format("20060102") + "-"
	var last sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT MAX(order_no) FROM orders WHERE order_no LIKE ?`, prefix+"%").Scan(&last); err != nil {
		return "", err
	}
	n := 1
	if last.Valid {
		fmt.Sscanf(last.String[len(prefix):], "%d", &n)
		n++
	}
	return fmt.Sprintf("%s%03d", prefix, n), nil
}

func (a *App) insertItems(ctx context.Context, tx *sql.Tx, orderID, customerID int64, items []itemIn) error {
	if len(items) == 0 {
		return bad("訂單至少要有一樣商品")
	}
	for _, it := range items {
		if it.Qty <= 0 {
			return bad("數量要大於 0")
		}
		price := int64(0)
		if it.UnitPrice != nil {
			price = *it.UnitPrice
		} else {
			err := tx.QueryRowContext(ctx, `SELECT COALESCE(
				(SELECT price FROM customer_prices WHERE customer_id = ? AND product_id = ?),
				(SELECT sale_price FROM products WHERE id = ?))`, customerID, it.ProductID, it.ProductID).Scan(&price)
			if err != nil {
				return err
			}
		}
		amount := it.Qty*price - it.Discount
		if _, err := tx.ExecContext(ctx, `INSERT INTO order_items (order_id, product_id, qty, unit_price, discount, amount, empties_returned)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, orderID, it.ProductID, it.Qty, price, it.Discount, amount, it.EmptiesReturned); err != nil {
			return err
		}
	}
	return nil
}

func (a *App) createOrder(r *http.Request, u *User) (any, error) {
	var in orderIn
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var addr, billing string
	var phone, taxID sql.NullString
	var needsInv int64
	var defDriver sql.NullInt64
	err = tx.QueryRowContext(ctx, `SELECT address, phone, billing_type, needs_invoice, tax_id, default_driver_id FROM customers WHERE id = ? AND active = 1`,
		in.CustomerID).Scan(&addr, &phone, &billing, &needsInv, &taxID, &defDriver)
	if err == sql.ErrNoRows {
		return nil, bad("找不到客戶")
	} else if err != nil {
		return nil, err
	}
	if in.Address != nil && *in.Address != "" {
		addr = *in.Address
	}
	if in.Phone != nil && *in.Phone != "" {
		phone = sql.NullString{String: *in.Phone, Valid: true}
	}
	if in.BillingType != nil {
		billing = *in.BillingType
	}
	if in.NeedsInvoice != nil {
		needsInv = 0
		if *in.NeedsInvoice {
			needsInv = 1
		}
	}
	pay, err := payMethod(in.PayMethod)
	if err != nil {
		return nil, err
	}
	no, err := a.nextOrderNo(ctx, tx)
	if err != nil {
		return nil, err
	}
	res, err := tx.ExecContext(ctx, `INSERT INTO orders (order_no, customer_id, billing_type, address, phone, needs_invoice, tax_id, note, pay_method, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, no, in.CustomerID, billing, addr, phone, needsInv, taxID, in.Note, pay, u.ID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if err := a.insertItems(ctx, tx, id, in.CustomerID, in.Items); err != nil {
		return nil, err
	}
	if in.DriverID != nil {
		if err := dispatchTx(ctx, tx, id, *in.DriverID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	o, err := a.loadOrder(ctx, id)
	if err == nil && in.DriverID != nil && a.OnDispatch != nil {
		a.OnDispatch(*in.DriverID, o)
	}
	return o, err
}

func (a *App) loadOrder(ctx context.Context, id int64) (map[string]any, error) {
	o, err := row(ctx, a.DB, `SELECT o.*, c.name AS customer_name, c.floor, c.has_elevator, u.name AS driver_name,
			COALESCE((SELECT SUM(amount) FROM payments WHERE order_id = o.id),0) AS paid_amount
		FROM orders o JOIN customers c ON c.id = o.customer_id LEFT JOIN users u ON u.id = o.driver_id WHERE o.id = ?`, id)
	if err != nil || o == nil {
		return nil, orNotFound(err, "找不到訂單")
	}
	if o["items"], err = rows(ctx, a.DB, `SELECT oi.*, p.name AS product_name, p.category, p.empty_product_id FROM order_items oi
		JOIN products p ON p.id = oi.product_id WHERE oi.order_id = ? ORDER BY oi.id`, id); err != nil {
		return nil, err
	}
	if o["payments"], err = rows(ctx, a.DB, `SELECT p.*, u.name AS collected_by_name FROM payments p LEFT JOIN users u ON u.id = p.collected_by WHERE p.order_id = ? ORDER BY p.id`, id); err != nil {
		return nil, err
	}
	if o["deposits"], err = rows(ctx, a.DB, `SELECT * FROM cylinder_deposits WHERE order_id = ? ORDER BY id`, id); err != nil {
		return nil, err
	}
	return o, nil
}

// 師傅只能看派給自己的單
func (a *App) checkOrderAccess(ctx context.Context, id int64, u *User) error {
	if !u.IsDriver() {
		return nil
	}
	var driver sql.NullInt64
	if err := a.DB.QueryRowContext(ctx, `SELECT driver_id FROM orders WHERE id = ?`, id).Scan(&driver); err != nil {
		return notFound("找不到訂單")
	}
	if !driver.Valid || driver.Int64 != u.ID {
		return forbidden()
	}
	return nil
}

func (a *App) getOrder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	if err := a.checkOrderAccess(r.Context(), id, u); err != nil {
		return nil, err
	}
	return a.loadOrder(r.Context(), id)
}

func (a *App) listOrders(r *http.Request, u *User) (any, error) {
	q := r.URL.Query()
	status, driver, date, customer := q.Get("status"), q.Get("driver_id"), q.Get("date"), q.Get("customer_id")
	if u.IsDriver() {
		driver = fmt.Sprint(u.ID)
		if status == "" {
			status = "dispatched"
		}
		if status != "dispatched" && status != "delivered" {
			return nil, forbidden()
		}
	}
	return rows(r.Context(), a.DB, `
		SELECT o.id, o.order_no, o.customer_id, c.name AS customer_name, o.address, o.phone, c.floor, c.has_elevator,
		       o.status, o.billing_type, o.pay_method, o.driver_id, u.name AS driver_name, o.total_amount, o.note,
		       o.ordered_at, o.dispatched_at, o.delivered_at,
		       COALESCE((SELECT SUM(amount) FROM payments WHERE order_id = o.id),0) AS paid_amount,
		       (SELECT group_concat(p.name || '×' || oi.qty, '、') FROM order_items oi JOIN products p ON p.id = oi.product_id WHERE oi.order_id = o.id) AS summary
		FROM orders o JOIN customers c ON c.id = o.customer_id LEFT JOIN users u ON u.id = o.driver_id
		WHERE (? = '' OR o.status = ?)
		  AND (? = '' OR o.driver_id = ?)
		  AND (? = '' OR o.customer_id = ?)
		  AND (? = '' OR date(COALESCE(o.delivered_at, o.ordered_at)) = ?)
		ORDER BY CASE o.status WHEN 'new' THEN 0 WHEN 'dispatched' THEN 1 ELSE 2 END, o.id DESC
		LIMIT ?`, status, status, driver, driver, customer, customer, date, date, limitParam(r, 300))
}

func (a *App) updateOrder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in orderIn
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status string
	var customerID int64
	if err := tx.QueryRowContext(ctx, `SELECT status, customer_id FROM orders WHERE id = ?`, id).Scan(&status, &customerID); err != nil {
		return nil, notFound("找不到訂單")
	}
	f := map[string]any{}
	if in.Note != nil {
		f["note"] = *in.Note
	}
	if in.Address != nil {
		f["address"] = *in.Address
	}
	if in.Phone != nil {
		f["phone"] = *in.Phone
	}
	if in.InvoiceNo != nil {
		f["invoice_no"] = *in.InvoiceNo
	}
	if in.NeedsInvoice != nil {
		f["needs_invoice"] = map[bool]int64{true: 1, false: 0}[*in.NeedsInvoice]
	}
	if in.PayMethod != nil {
		pay, err := payMethod(in.PayMethod)
		if err != nil {
			return nil, err
		}
		f["pay_method"] = pay
	}
	if in.BillingType != nil {
		if status == "delivered" {
			return nil, bad("已送達的訂單不能改現結／月結，請先改回未送達")
		}
		f["billing_type"] = *in.BillingType
	}
	if len(f) > 0 {
		if err := updateRow(ctx, tx, "orders", id, f, ""); err != nil {
			return nil, err
		}
	}
	if in.Items != nil {
		if status == "delivered" || status == "cancelled" {
			return nil, bad("已送達或已取消的訂單不能修改明細")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM order_items WHERE order_id = ?`, id); err != nil {
			return nil, err
		}
		if err := a.insertItems(ctx, tx, id, customerID, in.Items); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a.loadOrder(ctx, id)
}

func dispatchTx(ctx context.Context, tx *sql.Tx, id, driverID int64) error {
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ? AND active = 1`, driverID).Scan(&role); err != nil || role != "driver" {
		return bad("請選擇師傅")
	}
	res, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'dispatched', driver_id = ?, dispatched_at = datetime('now','localtime')
		WHERE id = ? AND status IN ('new','dispatched')`, driverID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return bad("這張單已送達或已取消，不能派單")
	}
	return nil
}

// 派單（或改派給別的師傅）
func (a *App) dispatchOrder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		DriverID int64 `json:"driver_id"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := dispatchTx(ctx, tx, id, in.DriverID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	o, err := a.loadOrder(ctx, id)
	if err == nil && a.OnDispatch != nil {
		a.OnDispatch(in.DriverID, o)
	}
	return o, err
}

type depositIn struct {
	ProductID int64   `json:"product_id"`
	QtyChange int64   `json:"qty_change"`
	Amount    int64   `json:"amount"`
	Method    string  `json:"method"`
	ReceiptNo *string `json:"receipt_no"`
	Note      *string `json:"note"`
}

// 送達回報：師傅按「已送達」，填收了多少錢、回收幾個空瓶、押瓶
func (a *App) deliverOrder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	if err := a.checkOrderAccess(ctx, id, u); err != nil {
		return nil, err
	}
	var in struct {
		CollectedAmount int64  `json:"collected_amount"`
		Method          string `json:"method"`
		Empties         []struct {
			ItemID          int64 `json:"item_id"`
			EmptiesReturned int64 `json:"empties_returned"`
		} `json:"empties"`
		Deposit *depositIn `json:"deposit"`
		Note    *string    `json:"note"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if in.Method == "" {
		in.Method = "cash"
	}
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status string
	var customerID int64
	var driver sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT status, customer_id, driver_id FROM orders WHERE id = ?`, id).Scan(&status, &customerID, &driver); err != nil {
		return nil, notFound("找不到訂單")
	}
	if status != "dispatched" {
		return nil, bad("這張單不是「已派單」狀態，不能回報送達")
	}
	collector := u.ID
	if !u.IsDriver() && driver.Valid {
		collector = driver.Int64 // 店裡代按：錢算在送貨師傅身上
	}
	for _, e := range in.Empties {
		if _, err := tx.ExecContext(ctx, `UPDATE order_items SET empties_returned = ? WHERE id = ? AND order_id = ?`, e.EmptiesReturned, e.ItemID, id); err != nil {
			return nil, err
		}
	}
	if in.Note != nil && *in.Note != "" {
		if _, err := tx.ExecContext(ctx, `UPDATE orders SET note = trim(COALESCE(note,'') || char(10) || ?) WHERE id = ?`, "送達備註："+*in.Note, id); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE orders SET status = 'delivered', delivered_at = datetime('now','localtime') WHERE id = ?`, id); err != nil {
		return nil, err
	}
	if in.CollectedAmount != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO payments (customer_id, order_id, amount, method, collected_by, created_by) VALUES (?, ?, ?, ?, ?, ?)`,
			customerID, id, in.CollectedAmount, in.Method, collector, u.ID); err != nil {
			return nil, err
		}
	}
	if d := in.Deposit; d != nil && (d.QtyChange != 0 || d.Amount != 0) {
		if d.Method == "" {
			d.Method = "cash"
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cylinder_deposits (customer_id, product_id, qty_change, amount, method, collected_by, order_id, receipt_no, note, created_by)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, customerID, d.ProductID, d.QtyChange, d.Amount, d.Method, collector, id, d.ReceiptNo, d.Note, u.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return a.loadOrder(ctx, id)
}

func (a *App) cancelOrder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE orders SET status = 'cancelled', cancelled_at = datetime('now','localtime')
		WHERE id = ? AND status IN ('new','dispatched')`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, bad("只有還沒送達的單可以取消")
	}
	return a.loadOrder(r.Context(), id)
}

// 改回未送達（老闆）：庫存會自動沖回；已收的款不會自動刪，要另外處理
func (a *App) undeliverOrder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE orders SET status = 'dispatched', delivered_at = NULL WHERE id = ? AND status = 'delivered'`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, bad("這張單不是已送達")
	}
	return a.loadOrder(r.Context(), id)
}
