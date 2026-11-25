package app

import (
	"database/sql"
	"encoding/base64"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// 鋼瓶清單：一支一支登記容器號碼、規格、下次檢驗期限，快到期、過期提醒。
// 師傅手機可以掃貼紙上的條碼找到鋼瓶、拍貼紙登記新鋼瓶。

type cylinderIn struct {
	Serial      *string  `json:"serial"`
	LabelCode   *string  `json:"label_code"`
	SizeKg      *float64 `json:"size_kg"`
	TareKg      *float64 `json:"tare_kg"`
	MadeOn      *string  `json:"made_on"`
	InspectedOn *string  `json:"inspected_on"`
	NextDue     *string  `json:"next_due"`
	Status      *string  `json:"status"`
	CustomerID  *int64   `json:"customer_id"`
	Note        *string  `json:"note"`
	Photo       *string  `json:"photo"`
}

// 快到期：下次檢驗期限在 30 天內
const cylinderSoonDays = 30

const cylinderSelect = `SELECT y.id, y.serial, y.label_code, y.size_kg, y.tare_kg, y.made_on, y.inspected_on, y.next_due,
	y.status, y.customer_id, c.name AS customer_name, y.note, y.created_at, y.updated_at, (y.photo IS NOT NULL) AS has_photo,
	CASE WHEN y.status = 'scrapped' THEN 'scrapped'
	     WHEN y.next_due < date('now','localtime') THEN 'overdue'
	     WHEN y.next_due <= date('now','localtime','+30 days') THEN 'soon'
	     ELSE 'ok' END AS due_state,
	CAST(julianday(y.next_due) - julianday(date('now','localtime')) AS INTEGER) AS days_left
	FROM cylinders y LEFT JOIN customers c ON c.id = y.customer_id`

var (
	reDate  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
	reMonth = regexp.MustCompile(`^\d{4}-\d{2}$`)
)

// normCode 條碼、容器號碼：去空白、轉大寫
func normCode(s string) string {
	return strings.ToUpper(strings.Join(strings.Fields(s), ""))
}

func isYMD(s string) bool {
	if !reDate.MatchString(s) {
		return false
	}
	_, err := time.Parse("2006-01-02", s)
	return err == nil
}

func (in *cylinderIn) check() error {
	if in.Serial != nil {
		v := normCode(*in.Serial)
		in.Serial = &v
	}
	if in.LabelCode != nil {
		v := normCode(*in.LabelCode)
		in.LabelCode = &v
	}
	if in.NextDue != nil && !isYMD(*in.NextDue) {
		return bad("下次檢驗期限的日期不對")
	}
	if in.InspectedOn != nil && *in.InspectedOn != "" && !isYMD(*in.InspectedOn) {
		return bad("定期檢驗日期不對")
	}
	if in.MadeOn != nil && *in.MadeOn != "" && !reMonth.MatchString(*in.MadeOn) {
		return bad("出廠年月不對")
	}
	if in.Status != nil {
		switch *in.Status {
		case "store", "customer", "scrapped":
		default:
			return bad("狀態不對")
		}
	}
	if in.SizeKg != nil && *in.SizeKg < 0 || in.TareKg != nil && *in.TareKg < 0 {
		return bad("重量不能是負的")
	}
	return nil
}

func decodePhoto(s *string) ([]byte, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	i := strings.Index(*s, ",")
	b, err := base64.StdEncoding.DecodeString((*s)[i+1:])
	if err != nil || len(b) > maxPhotoBytes {
		return nil, bad("照片讀不到或太大")
	}
	return b, nil
}

func (a *App) listCylinders(r *http.Request, u *User) (any, error) {
	q := r.URL.Query()
	where, args := []string{"1=1"}, []any{}
	switch q.Get("filter") {
	case "overdue":
		where = append(where, "y.status <> 'scrapped' AND y.next_due < date('now','localtime')")
	case "soon":
		where = append(where, "y.status <> 'scrapped' AND y.next_due >= date('now','localtime') AND y.next_due <= date('now','localtime','+30 days')")
	case "scrapped":
		where = append(where, "y.status = 'scrapped'")
	case "all":
	default:
		where = append(where, "y.status <> 'scrapped'")
	}
	if v := strings.TrimSpace(q.Get("q")); v != "" {
		like := "%" + normCode(v) + "%"
		where = append(where, "(y.serial LIKE ? OR y.label_code LIKE ? OR c.name LIKE ?)")
		args = append(args, like, like, "%"+v+"%")
	}
	return rows(r.Context(), a.DB, cylinderSelect+" WHERE "+strings.Join(where, " AND ")+" ORDER BY y.status = 'scrapped', y.next_due, y.serial LIMIT 500", args...)
}

// 首頁、商品庫存的提醒數字
func (a *App) cylinderSummary(r *http.Request, u *User) (any, error) {
	return row(r.Context(), a.DB, `SELECT
		COUNT(*) AS total,
		COALESCE(SUM(next_due < date('now','localtime')),0) AS overdue,
		COALESCE(SUM(next_due >= date('now','localtime') AND next_due <= date('now','localtime','+30 days')),0) AS soon
		FROM cylinders WHERE status <> 'scrapped'`)
}

// 掃到條碼（或打容器號碼）找鋼瓶：目前貼紙 → 換掉的舊貼紙 → 容器號碼
func (a *App) lookupCylinder(r *http.Request, u *User) (any, error) {
	code := normCode(r.URL.Query().Get("code"))
	if code == "" {
		return nil, bad("沒有號碼")
	}
	ctx := r.Context()
	if c, err := row(ctx, a.DB, cylinderSelect+` WHERE y.label_code = ?`, code); err == nil && c != nil {
		return map[string]any{"found": true, "by": "label", "cylinder": c}, nil
	}
	var cid int64
	if a.DB.QueryRowContext(ctx, `SELECT cylinder_id FROM cylinder_labels WHERE label_code = ? ORDER BY id DESC LIMIT 1`, code).Scan(&cid) == nil {
		if c, err := row(ctx, a.DB, cylinderSelect+` WHERE y.id = ?`, cid); err == nil && c != nil {
			return map[string]any{"found": true, "by": "old_label", "cylinder": c}, nil
		}
	}
	if c, err := row(ctx, a.DB, cylinderSelect+` WHERE y.serial = ?`, code); err == nil && c != nil {
		return map[string]any{"found": true, "by": "serial", "cylinder": c}, nil
	}
	return map[string]any{"found": false, "code": code}, nil
}

func (a *App) getCylinder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	c, err := row(r.Context(), a.DB, cylinderSelect+` WHERE y.id = ?`, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, notFound("找不到這支鋼瓶")
	}
	labels, err := rows(r.Context(), a.DB, `SELECT l.label_code, l.next_due, l.replaced_at, u.name AS replaced_by_name
		FROM cylinder_labels l LEFT JOIN users u ON u.id = l.replaced_by WHERE l.cylinder_id = ? ORDER BY l.id DESC`, id)
	if err != nil {
		return nil, err
	}
	c["old_labels"] = labels
	return c, nil
}

// 條碼已經是別支鋼瓶的就擋下來
func (a *App) labelTaken(r *http.Request, code string, self int64) error {
	if code == "" {
		return nil
	}
	var other string
	err := a.DB.QueryRowContext(r.Context(), `SELECT serial FROM cylinders WHERE label_code = ? AND id <> ?`, code, self).Scan(&other)
	if err == nil {
		return bad("這個條碼已經是容器號碼 " + other + " 的鋼瓶")
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return nil
}

func (a *App) createCylinder(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxPhotoBytes*2)
	a.route(a.createCylinderH)(w, r)
}

func (a *App) createCylinderH(r *http.Request, u *User) (any, error) {
	var in cylinderIn
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if err := in.check(); err != nil {
		return nil, err
	}
	if in.Serial == nil || *in.Serial == "" {
		return nil, bad("請填容器號碼")
	}
	if in.NextDue == nil {
		return nil, bad("請填下次檢驗期限")
	}
	label := ""
	if in.LabelCode != nil {
		label = *in.LabelCode
	}
	if err := a.labelTaken(r, label, 0); err != nil {
		return nil, err
	}
	var dup int64
	if a.DB.QueryRowContext(r.Context(), `SELECT id FROM cylinders WHERE serial = ?`, *in.Serial).Scan(&dup) == nil {
		return nil, bad("容器號碼 " + *in.Serial + " 已經登記過了")
	}
	photo, err := decodePhoto(in.Photo)
	if err != nil {
		return nil, err
	}
	status := "store"
	if in.Status != nil {
		status = *in.Status
	}
	str := func(p *string) any {
		if p == nil {
			return nil
		}
		return nullIfEmpty(*p)
	}
	fl := func(p *float64) any {
		if p == nil || *p == 0 {
			return nil
		}
		return *p
	}
	var cust any
	if status == "customer" && in.CustomerID != nil {
		cust = *in.CustomerID
	}
	res, err := a.DB.ExecContext(r.Context(), `INSERT INTO cylinders (serial, label_code, size_kg, tare_kg, made_on, inspected_on, next_due, status, customer_id, note, photo, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		*in.Serial, nullIfEmpty(label), fl(in.SizeKg), fl(in.TareKg), str(in.MadeOn), str(in.InspectedOn), *in.NextDue, status, cust, str(in.Note), photo, u.ID)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return row(r.Context(), a.DB, cylinderSelect+` WHERE y.id = ?`, id)
}

// 改資料（狀態、規格、期限…）。換貼紙請用 relabel，舊號碼才會留紀錄。
func (a *App) updateCylinder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in cylinderIn
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if err := in.check(); err != nil {
		return nil, err
	}
	set, args := []string{"updated_at = datetime('now','localtime')"}, []any{}
	add := func(col string, v any) { set = append(set, col+" = ?"); args = append(args, v) }
	if in.Serial != nil {
		if *in.Serial == "" {
			return nil, bad("容器號碼不能空白")
		}
		var other int64
		if a.DB.QueryRowContext(r.Context(), `SELECT id FROM cylinders WHERE serial = ? AND id <> ?`, *in.Serial, id).Scan(&other) == nil {
			return nil, bad("容器號碼 " + *in.Serial + " 已經是別支鋼瓶")
		}
		add("serial", *in.Serial)
	}
	if in.SizeKg != nil {
		add("size_kg", *in.SizeKg)
	}
	if in.TareKg != nil {
		add("tare_kg", *in.TareKg)
	}
	if in.MadeOn != nil {
		add("made_on", nullIfEmpty(*in.MadeOn))
	}
	if in.InspectedOn != nil {
		add("inspected_on", nullIfEmpty(*in.InspectedOn))
	}
	if in.NextDue != nil {
		add("next_due", *in.NextDue)
	}
	if in.Note != nil {
		add("note", nullIfEmpty(*in.Note))
	}
	if in.Status != nil {
		add("status", *in.Status)
		if *in.Status != "customer" {
			add("customer_id", nil)
		}
	}
	if in.CustomerID != nil {
		add("customer_id", *in.CustomerID)
	}
	args = append(args, id)
	res, err := a.DB.ExecContext(r.Context(), `UPDATE cylinders SET `+strings.Join(set, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, notFound("找不到這支鋼瓶")
	}
	return row(r.Context(), a.DB, cylinderSelect+` WHERE y.id = ?`, id)
}

type relabelIn struct {
	LabelCode   string  `json:"label_code"`
	NextDue     string  `json:"next_due"`
	InspectedOn *string `json:"inspected_on"`
}

// 換新貼紙（重新檢驗過）：舊條碼留在紀錄裡，換上新條碼和新的檢驗期限
func (a *App) relabelCylinder(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in relabelIn
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	in.LabelCode = normCode(in.LabelCode)
	if in.LabelCode == "" {
		return nil, bad("請掃新貼紙的條碼")
	}
	if !isYMD(in.NextDue) {
		return nil, bad("請填新的下次檢驗期限")
	}
	if in.InspectedOn != nil && *in.InspectedOn != "" && !isYMD(*in.InspectedOn) {
		return nil, bad("定期檢驗日期不對")
	}
	if err := a.labelTaken(r, in.LabelCode, id); err != nil {
		return nil, err
	}
	ctx := r.Context()
	tx, err := a.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var old sql.NullString
	var oldDue string
	if err := tx.QueryRowContext(ctx, `SELECT label_code, next_due FROM cylinders WHERE id = ?`, id).Scan(&old, &oldDue); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, notFound("找不到這支鋼瓶")
		}
		return nil, err
	}
	if old.Valid && old.String != "" && old.String != in.LabelCode {
		if _, err := tx.ExecContext(ctx, `INSERT INTO cylinder_labels (cylinder_id, label_code, next_due, replaced_by) VALUES (?, ?, ?, ?)`, id, old.String, oldDue, u.ID); err != nil {
			return nil, err
		}
	}
	insp := any(nil)
	if in.InspectedOn != nil {
		insp = nullIfEmpty(*in.InspectedOn)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cylinders SET label_code = ?, next_due = ?, inspected_on = COALESCE(?, inspected_on),
		updated_at = datetime('now','localtime') WHERE id = ?`, in.LabelCode, in.NextDue, insp, id); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return row(ctx, a.DB, cylinderSelect+` WHERE y.id = ?`, id)
}

// 貼紙照片
func (a *App) cylinderPhoto(w http.ResponseWriter, r *http.Request) {
	u, err := a.userFromRequest(r)
	if err != nil || u == nil {
		http.Error(w, "請先登入", http.StatusUnauthorized)
		return
	}
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var b []byte
	if a.DB.QueryRowContext(r.Context(), `SELECT photo FROM cylinders WHERE id = ?`, id).Scan(&b) != nil || len(b) == 0 {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Write(b)
}
