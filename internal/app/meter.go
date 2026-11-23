package app

import (
	"context"
	"database/sql"
	"encoding/base64"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	gasdb "github.com/hallowyeeee/gas/db"
)

// 計流量（抄錶）收費：師傅輸入這次錶上的度數，程式算出用了幾度、要收多少。
// 本店收費方式在設定 charge_mode：cylinder 只算桶（預設）、meter 只計流量、both 兩種都有。

func ensureMeter(db *sql.DB) error {
	for _, c := range [][3]string{
		{"customers", "charge_type", "TEXT NOT NULL DEFAULT 'cylinder'"}, // cylinder 算桶 / meter 計流量
		{"customers", "meter_no", "TEXT"},                                // 錶號
		{"customers", "meter_price", "INTEGER"},                          // 每度單價
		{"customers", "meter_start", "REAL"},                             // 起始度數（第一次抄錶前的錶數）
	} {
		if err := ensureColumn(db, c[0], c[1], c[2]); err != nil {
			return err
		}
	}
	_, err := db.Exec(gasdb.Meter)
	return err
}

const maxPhotoBytes = 2 << 20

func (a *App) chargeMode(ctx context.Context) string {
	var v string
	a.DB.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = 'charge_mode'`).Scan(&v)
	if v != "meter" && v != "both" {
		v = "cylinder"
	}
	return v
}

// 計流量的客人（師傅抄錶時選）：只給名字、地址、錶號、上次度數
func (a *App) meterCustomers(r *http.Request, u *User) (any, error) {
	ctx := r.Context()
	month := time.Now().Format("2006-01")
	list, err := rows(ctx, a.DB, `SELECT c.id, c.name, c.code, c.address, c.floor, c.meter_no, c.billing_type,
			CASE WHEN c.meter_price > 0 THEN 1 ELSE 0 END AS has_price, `+priceCol(u)+`
			COALESCE((SELECT reading FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL ORDER BY m.read_at DESC, m.id DESC LIMIT 1), c.meter_start) AS last_reading,
			(SELECT read_at FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL ORDER BY m.read_at DESC, m.id DESC LIMIT 1) AS last_read_at,
			(SELECT usage FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND m.usage > 0 ORDER BY m.read_at DESC, m.id DESC LIMIT 1) AS prev_usage,
			EXISTS (SELECT 1 FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?) AS read_this_month
		FROM customers c WHERE c.active = 1 AND c.charge_type = 'meter'
		ORDER BY read_this_month, c.address`, month)
	if err != nil {
		return nil, err
	}
	return map[string]any{"mode": a.chargeMode(ctx), "customers": list}, nil
}

// 師傅手機也會看到單價（要算給客人聽），店裡人員還多看欠款
func priceCol(u *User) string {
	if u.IsDriver() {
		return "c.meter_price,"
	}
	return "c.meter_price, (SELECT balance FROM v_customer_balance b WHERE b.customer_id = c.id) AS balance,"
}

type meterIn struct {
	CustomerID      int64   `json:"customer_id"`
	Reading         float64 `json:"reading"`
	Photo           string  `json:"photo"` // data:image/jpeg;base64,...
	CollectedAmount int64   `json:"collected_amount"`
	Method          string  `json:"method"`
	Note            *string `json:"note"`
}

func (a *App) createMeterReading(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes*2)
	a.route(a.createMeterReadingH)(w, r)
}

func (a *App) createMeterReadingH(r *http.Request, u *User) (any, error) {
	var in meterIn
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if in.Reading < 0 || math.IsNaN(in.Reading) {
		return nil, bad("度數不能是負的")
	}
	var photo []byte
	if in.Photo != "" {
		i := strings.Index(in.Photo, ",")
		b, err := base64.StdEncoding.DecodeString(in.Photo[i+1:])
		if err != nil || len(b) > maxPhotoBytes {
			return nil, bad("照片讀不到或太大")
		}
		photo = b
	}
	if in.Method == "" {
		in.Method = "cash"
	}
	if in.Method != "cash" && in.Method != "transfer" {
		return nil, bad("付款方式只能是現金或轉帳")
	}
	ctx := r.Context()
	a.mu.Lock()
	defer a.mu.Unlock()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var chargeType, billing string
	var price sql.NullInt64
	var start sql.NullFloat64
	err = tx.QueryRowContext(ctx, `SELECT charge_type, billing_type, meter_price, meter_start FROM customers WHERE id = ? AND active = 1`, in.CustomerID).
		Scan(&chargeType, &billing, &price, &start)
	if err == sql.ErrNoRows {
		return nil, bad("找不到客戶")
	} else if err != nil {
		return nil, err
	}
	if chargeType != "meter" {
		return nil, bad("這位客人不是計流量收費")
	}
	if !price.Valid || price.Int64 <= 0 {
		return nil, bad("這位客人還沒填每度單價，請店裡先到客戶資料填好")
	}
	var prev sql.NullFloat64
	tx.QueryRowContext(ctx, `SELECT reading FROM meter_readings WHERE customer_id = ? AND cancelled_at IS NULL ORDER BY read_at DESC, id DESC LIMIT 1`, in.CustomerID).Scan(&prev)
	if !prev.Valid {
		prev = start
	}
	note := ""
	if in.Note != nil {
		note = strings.TrimSpace(*in.Note)
	}
	p := in.Reading
	if prev.Valid {
		p = prev.Float64
	} else {
		note = strings.TrimSpace("第一次抄錶，當作起始度數 " + note)
	}
	usage := math.Round((in.Reading-p)*100) / 100
	if usage < 0 {
		return nil, bad("這次度數比上次（" + strconv.FormatFloat(p, 'f', -1, 64) + "）還小，請再看一次錶")
	}
	amount := int64(math.Round(usage * float64(price.Int64)))
	res, err := tx.ExecContext(ctx, `INSERT INTO meter_readings (customer_id, prev_reading, reading, usage, unit_price, amount, billing_type, photo, note, read_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, in.CustomerID, p, in.Reading, usage, price.Int64, amount, billing, photo, nullIfEmpty(note), u.ID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	if in.CollectedAmount != 0 {
		if _, err := tx.ExecContext(ctx, `INSERT INTO payments (customer_id, amount, method, collected_by, note, created_by) VALUES (?, ?, ?, ?, ?, ?)`,
			in.CustomerID, in.CollectedAmount, in.Method, u.ID, "抄錶收款", u.ID); err != nil {
			return nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return row(ctx, a.DB, meterSelect+` WHERE m.id = ?`, id)
}

const meterSelect = `SELECT m.id, m.customer_id, c.name AS customer_name, m.prev_reading, m.reading, m.usage, m.unit_price, m.amount,
	m.billing_type, m.note, m.read_at, m.cancelled_at, u.name AS read_by_name, (m.photo IS NOT NULL) AS has_photo
	FROM meter_readings m JOIN customers c ON c.id = m.customer_id LEFT JOIN users u ON u.id = m.read_by`

func (a *App) listMeterReadings(r *http.Request, u *User) (any, error) {
	q := r.URL.Query()
	where, args := []string{"1=1"}, []any{}
	if v := q.Get("customer_id"); v != "" {
		where = append(where, "m.customer_id = ?")
		args = append(args, v)
	}
	if v := q.Get("month"); v != "" {
		where = append(where, "strftime('%Y-%m', m.read_at) = ?")
		args = append(args, v)
	}
	if u.IsDriver() { // 師傅只看自己今天抄的
		where = append(where, "m.read_by = ? AND date(m.read_at) = date('now','localtime')")
		args = append(args, u.ID)
	}
	return rows(r.Context(), a.DB, meterSelect+" WHERE "+strings.Join(where, " AND ")+" ORDER BY m.read_at DESC, m.id DESC LIMIT 200", args...)
}

// 錶的照片
func (a *App) meterPhoto(w http.ResponseWriter, r *http.Request) {
	u, err := a.userFromRequest(r)
	if err != nil || u == nil || u.IsDriver() {
		http.Error(w, "請先登入", http.StatusUnauthorized)
		return
	}
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var b []byte
	if a.DB.QueryRowContext(r.Context(), `SELECT photo FROM meter_readings WHERE id = ?`, id).Scan(&b) != nil || len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(b)
}

// 抄錯了：作廢（金額不再算進欠款；已收的錢不會自動刪）
func (a *App) cancelMeterReading(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE meter_readings SET cancelled_at = datetime('now','localtime') WHERE id = ? AND cancelled_at IS NULL`, id)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("找不到這筆抄錶，或已經作廢")
	}
	return nil, nil
}

// 用量統計：選一個月，看算桶（每位客人幾桶）和計流量（每位客人幾度）
func (a *App) usageStats(r *http.Request, u *User) (any, error) {
	ctx := r.Context()
	month := r.URL.Query().Get("month")
	t, err := time.Parse("2006-01", month)
	if err != nil {
		t = time.Now()
		month = t.Format("2006-01")
	}
	prevMonth := t.AddDate(0, -1, 0).Format("2006-01")
	cyl, err := rows(ctx, a.DB, `SELECT o.customer_id, c.name, c.code, c.address, p.id AS product_id, p.name AS product_name,
			SUM(oi.qty) AS qty, SUM(oi.amount) AS amount
		FROM orders o JOIN order_items oi ON oi.order_id = o.id JOIN products p ON p.id = oi.product_id JOIN customers c ON c.id = o.customer_id
		WHERE o.status = 'delivered' AND p.category = 'gas' AND strftime('%Y-%m', o.delivered_at) = ?
		GROUP BY o.customer_id, p.id ORDER BY c.name`, month)
	if err != nil {
		return nil, err
	}
	prev, err := rows(ctx, a.DB, `SELECT o.customer_id, SUM(oi.qty) AS qty
		FROM orders o JOIN order_items oi ON oi.order_id = o.id JOIN products p ON p.id = oi.product_id
		WHERE o.status = 'delivered' AND p.category = 'gas' AND strftime('%Y-%m', o.delivered_at) = ?
		GROUP BY o.customer_id`, prevMonth)
	if err != nil {
		return nil, err
	}
	meter, err := rows(ctx, a.DB, `SELECT c.id AS customer_id, c.name, c.code, c.address, c.meter_no, c.meter_price, c.billing_type,
			(SELECT MIN(prev_reading) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?1) AS from_reading,
			(SELECT MAX(reading) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?1) AS to_reading,
			(SELECT SUM(usage) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?1) AS usage,
			(SELECT SUM(amount) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?1) AS amount,
			(SELECT MAX(read_at) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?1) AS read_at,
			(SELECT id FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?1 AND m.photo IS NOT NULL ORDER BY m.read_at DESC LIMIT 1) AS photo_id,
			(SELECT SUM(usage) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?2) AS prev_usage
		FROM customers c
		WHERE c.charge_type = 'meter' AND (c.active = 1 OR EXISTS (SELECT 1 FROM meter_readings m WHERE m.customer_id = c.id AND strftime('%Y-%m', m.read_at) = ?1))
		ORDER BY (SELECT COUNT(*) FROM meter_readings m WHERE m.customer_id = c.id AND m.cancelled_at IS NULL AND strftime('%Y-%m', m.read_at) = ?1) > 0, c.address`, month, prevMonth)
	if err != nil {
		return nil, err
	}
	return map[string]any{"month": month, "prev_month": prevMonth, "mode": a.chargeMode(ctx),
		"cylinders": cyl, "cylinders_prev": prev, "meter": meter}, nil
}
