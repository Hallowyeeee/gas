package app

import (
	"net/http"
	"strconv"
)

// ---------------------------------------------------------------------
// 客戶
// ---------------------------------------------------------------------

var customerFields = []string{"code", "name", "phone", "phone2", "address", "area", "billing_type",
	"default_driver_id", "refill_days", "floor", "has_elevator", "needs_invoice", "tax_id", "invoice_title", "note", "active"}

func limitParam(r *http.Request, def int) int {
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && n > 0 && n <= 1000 {
		return n
	}
	return def
}

func (a *App) listCustomers(r *http.Request, u *User) (any, error) {
	q := r.URL.Query().Get("q")
	like := "%" + q + "%"
	active := r.URL.Query().Get("all") == ""
	return rows(r.Context(), a.DB, `
		SELECT c.*, b.balance FROM customers c LEFT JOIN v_customer_balance b ON b.customer_id = c.id
		WHERE (? = '' OR c.name LIKE ? OR c.phone LIKE ? OR c.phone2 LIKE ? OR c.address LIKE ? OR c.code LIKE ?)
		  AND (? = 0 OR c.active = 1)
		ORDER BY c.name LIMIT ?`, q, like, like, like, like, like, active, limitParam(r, 200))
}

func (a *App) getCustomer(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	ctx := r.Context()
	c, err := row(ctx, a.DB, `SELECT c.*, b.total_sales, b.total_paid, b.balance
		FROM customers c LEFT JOIN v_customer_balance b ON b.customer_id = c.id WHERE c.id = ?`, id)
	if err != nil || c == nil {
		return nil, orNotFound(err, "找不到客戶")
	}
	if c["prices"], err = rows(ctx, a.DB, `SELECT cp.product_id, p.name, cp.price FROM customer_prices cp JOIN products p ON p.id = cp.product_id WHERE cp.customer_id = ?`, id); err != nil {
		return nil, err
	}
	if c["deposits"], err = rows(ctx, a.DB, `SELECT * FROM v_customer_deposits WHERE customer_id = ?`, id); err != nil {
		return nil, err
	}
	if c["recent_orders"], err = rows(ctx, a.DB, `SELECT id, order_no, status, total_amount, ordered_at, delivered_at FROM orders WHERE customer_id = ? ORDER BY id DESC LIMIT 20`, id); err != nil {
		return nil, err
	}
	return c, nil
}

func orNotFound(err error, msg string) error {
	if err != nil {
		return err
	}
	return notFound(msg)
}

func (a *App) createCustomer(r *http.Request, u *User) (any, error) {
	return a.createSimple(r, "customers", customerFields)
}

func (a *App) updateCustomer(r *http.Request, u *User) (any, error) {
	return a.updateSimple(r, "customers", customerFields, "updated_at = datetime('now','localtime')")
}

func (a *App) setCustomerPrice(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	pid, ok := num(in["product_id"])
	if !ok {
		return nil, bad("請選商品")
	}
	if in["price"] == nil {
		_, err = a.DB.ExecContext(r.Context(), `DELETE FROM customer_prices WHERE customer_id = ? AND product_id = ?`, id, pid)
		return nil, err
	}
	price, ok := num(in["price"])
	if !ok {
		return nil, bad("價格必須是整數")
	}
	_, err = a.DB.ExecContext(r.Context(), `INSERT INTO customer_prices (customer_id, product_id, price) VALUES (?, ?, ?)
		ON CONFLICT (customer_id, product_id) DO UPDATE SET price = excluded.price`, id, pid, price)
	return nil, err
}

// ---------------------------------------------------------------------
// 商品
// ---------------------------------------------------------------------

var productFields = []string{"sku", "name", "category", "unit", "sale_price", "cost_price", "track_stock",
	"empty_product_id", "low_stock_alert", "active"}

func (a *App) listProducts(r *http.Request, u *User) (any, error) {
	cat := r.URL.Query().Get("category")
	return rows(r.Context(), a.DB, `
		SELECT p.*, s.qty AS stock_qty FROM products p LEFT JOIN v_stock s ON s.product_id = p.id
		WHERE (? = '' OR p.category = ?) AND (? <> '' OR p.active = 1)
		ORDER BY p.category, p.name`, cat, cat, r.URL.Query().Get("all"))
}

func (a *App) createProduct(r *http.Request, u *User) (any, error) {
	return a.createSimple(r, "products", productFields)
}

func (a *App) updateProduct(r *http.Request, u *User) (any, error) {
	return a.updateSimple(r, "products", productFields, "")
}

// ---------------------------------------------------------------------
// 供應商
// ---------------------------------------------------------------------

var supplierFields = []string{"name", "phone", "note", "active"}

func (a *App) listSuppliers(r *http.Request, u *User) (any, error) {
	return rows(r.Context(), a.DB, `SELECT s.*, b.balance FROM suppliers s LEFT JOIN v_supplier_balance b ON b.supplier_id = s.id ORDER BY s.active DESC, s.name`)
}

func (a *App) createSupplier(r *http.Request, u *User) (any, error) {
	return a.createSimple(r, "suppliers", supplierFields)
}

func (a *App) updateSupplier(r *http.Request, u *User) (any, error) {
	return a.updateSimple(r, "suppliers", supplierFields, "")
}

// ---------------------------------------------------------------------
// 設定
// ---------------------------------------------------------------------

func (a *App) getSettings(r *http.Request, u *User) (any, error) {
	list, err := rows(r.Context(), a.DB, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	for _, m := range list {
		out[m["key"].(string)] = m["value"]
	}
	return out, nil
}

func (a *App) putSettings(r *http.Request, u *User) (any, error) {
	var in map[string]string
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	for k, v := range in {
		if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return nil, err
		}
	}
	return nil, nil
}

func (a *App) setting(r *http.Request, key, def string) string {
	var v string
	if err := a.DB.QueryRowContext(r.Context(), `SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
		return def
	}
	return v
}

// ---------------------------------------------------------------------

func (a *App) createSimple(r *http.Request, table string, allowed []string) (any, error) {
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, allowed)
	if err != nil {
		return nil, err
	}
	id, err := insertRow(r.Context(), a.DB, table, f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

func (a *App) updateSimple(r *http.Request, table string, allowed []string, extra string) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, allowed)
	if err != nil {
		return nil, err
	}
	return nil, updateRow(r.Context(), a.DB, table, id, f, extra)
}
