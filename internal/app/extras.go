package app

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	gasdb "github.com/hallowyeeee/gas/db"
)

// 存氣紀錄、調漲紀錄

func ensureExtras(db *sql.DB) error {
	_, err := db.Exec(gasdb.Extras)
	return err
}

type creditIn struct {
	ProductID *int64  `json:"product_id"`
	Kg        float64 `json:"kg"`
	Note      *string `json:"note"`
}

func insertCredit(ctx context.Context, tx interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, customerID int64, orderID *int64, c creditIn, by int64) error {
	if c.Kg == 0 || math.IsNaN(c.Kg) || math.Abs(c.Kg) > 1000 {
		return bad("存氣公斤數不對")
	}
	var pid any
	if c.ProductID != nil && *c.ProductID > 0 {
		pid = *c.ProductID
	}
	note := ""
	if c.Note != nil {
		note = strings.TrimSpace(*c.Note)
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO gas_credits (customer_id, product_id, kg, order_id, note, created_by) VALUES (?, ?, ?, ?, ?, ?)`,
		customerID, pid, math.Round(c.Kg*100)/100, orderID, nullIfEmpty(note), by)
	return err
}

func (a *App) listCredits(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	list, err := rows(r.Context(), a.DB, `SELECT g.*, p.name AS product_name, u.name AS created_by_name, o.order_no
		FROM gas_credits g LEFT JOIN products p ON p.id = g.product_id LEFT JOIN users u ON u.id = g.created_by LEFT JOIN orders o ON o.id = g.order_id
		WHERE g.customer_id = ? ORDER BY g.created_at DESC, g.id DESC`, id)
	if err != nil {
		return nil, err
	}
	return list, nil
}

func (a *App) createCredit(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in creditIn
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	return nil, insertCredit(r.Context(), a.DB, id, nil, in, u.ID)
}

func (a *App) deleteCredit(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return nil, execOne(r.Context(), a.DB, `DELETE FROM gas_credits WHERE id = ?`, id)
}

// 調漲紀錄
func (a *App) listRaises(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return rows(r.Context(), a.DB, `SELECT pr.*, p.name AS product_name, u.name AS created_by_name
		FROM price_raises pr LEFT JOIN products p ON p.id = pr.product_id LEFT JOIN users u ON u.id = pr.created_by
		WHERE pr.customer_id = ? ORDER BY pr.raised_on DESC, pr.id DESC`, id)
}

func (a *App) createRaise(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in struct {
		ProductID *int64  `json:"product_id"`
		OldPrice  *int64  `json:"old_price"`
		NewPrice  *int64  `json:"new_price"`
		RaisedOn  string  `json:"raised_on"`
		Note      *string `json:"note"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if in.RaisedOn == "" {
		in.RaisedOn = time.Now().Format("2006-01-02")
	}
	if _, err := time.Parse("2006-01-02", in.RaisedOn); err != nil {
		return nil, bad("日期格式不對")
	}
	var pid any
	if in.ProductID != nil && *in.ProductID > 0 {
		pid = *in.ProductID
	}
	_, err = a.DB.ExecContext(r.Context(), `INSERT INTO price_raises (customer_id, product_id, old_price, new_price, raised_on, note, created_by) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, pid, in.OldPrice, in.NewPrice, in.RaisedOn, in.Note, u.ID)
	return nil, err
}

func (a *App) deleteRaise(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	return nil, execOne(r.Context(), a.DB, `DELETE FROM price_raises WHERE id = ?`, id)
}

func execOne(ctx context.Context, db *sql.DB, q string, args ...any) error {
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound("找不到資料")
	}
	return nil
}

// 派單通知的文字：品項隻數和金額，例如「20kg 瓦斯×2、瓦斯管×1｜共 3 隻 $2,210」
func orderBrief(o map[string]any) string {
	items, _ := o["items"].([]map[string]any)
	var parts []string
	var n int64
	for _, it := range items {
		q, _ := it["qty"].(int64)
		parts = append(parts, fmt.Sprintf("%v×%d", it["product_name"], q))
		n += q
	}
	if len(parts) == 0 {
		return ""
	}
	total, _ := o["total_amount"].(int64)
	s := strings.Join(parts, "、") + fmt.Sprintf("｜共 %d 隻 $%s", n, commas(total))
	if o["billing_type"] == "monthly" {
		s += "（月結）"
	}
	return s
}

func commas(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	if neg {
		return "-" + s
	}
	return s
}
