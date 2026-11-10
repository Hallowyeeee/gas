package app

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func bossClient(t *testing.T) (*App, *client) {
	a, c := setup(t)
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	return a, c.as(res["token"].(string))
}

// 預約日期＋時段、待派清單、拖拉派單改時段、收回派單
func TestDeliverySlotAndBoard(t *testing.T) {
	_, boss := bossClient(t)
	ming := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"}))
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "phone": "0912000001", "address": "中正路 1 號"}))
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 900}))
	items := []any{map[string]any{"product_id": gas, "qty": 2}}
	today := time.Now().Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")

	o1 := boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items})
	if o1["delivery_date"] != today || o1["delivery_slot"] != nil {
		t.Fatal("沒填預約就是今天、不指定", o1["delivery_date"], o1["delivery_slot"])
	}
	o2 := boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items, "delivery_date": tomorrow, "delivery_slot": "pm"})
	if o2["delivery_date"] != tomorrow || o2["delivery_slot"] != "pm" {
		t.Fatal("預約明天下午", o2["delivery_date"], o2["delivery_slot"])
	}
	if code, _, _ := boss.do("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items, "delivery_slot": "night"}); code != 400 {
		t.Fatal("不認得的時段要擋", code)
	}
	if code, _, _ := boss.do("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items, "delivery_date": "11/10"}); code != 400 {
		t.Fatal("日期格式錯要擋", code)
	}
	// 今天的待派只有 o1；明天的預約有 o2
	if l := boss.list("/api/orders?open_until=" + today); len(l) != 1 || n(l[0].(map[string]any)["id"]) != id(o1) {
		t.Fatal("今天待派不對", l)
	}
	if l := boss.list("/api/orders?delivery_date=" + tomorrow); len(l) != 1 || l[0].(map[string]any)["gas_qty"] != float64(2) {
		t.Fatal("明日預約不對", l)
	}
	// 拖到阿明的上午
	d := boss.ok("POST", fmt.Sprint("/api/orders/", id(o1), "/dispatch"), map[string]any{"driver_id": ming, "delivery_slot": "am"})
	if d["status"] != "dispatched" || d["delivery_slot"] != "am" {
		t.Fatal("拖拉派單要改時段", d["status"], d["delivery_slot"])
	}
	if l := boss.list("/api/orders?open_until=" + today); len(l) != 0 {
		t.Fatal("派走了就不在待派", l)
	}
	// 拖回待派
	u := boss.ok("POST", fmt.Sprint("/api/orders/", id(o1), "/undispatch"), map[string]any{})
	if u["status"] != "new" || u["driver_id"] != nil {
		t.Fatal("收回派單", u["status"], u["driver_id"])
	}
	if code, _, _ := boss.do("POST", fmt.Sprint("/api/orders/", id(o1), "/undispatch"), map[string]any{}); code != 400 {
		t.Fatal("新單不能再收回", code)
	}
	// 改預約日期／時段
	e := boss.ok("PUT", fmt.Sprint("/api/orders/", id(o2)), map[string]any{"delivery_date": today, "delivery_slot": ""})
	if e["delivery_date"] != today || e["delivery_slot"] != nil {
		t.Fatal("改預約", e["delivery_date"], e["delivery_slot"])
	}
	// 日期區間
	if l := boss.list("/api/orders?from=" + today + "&to=" + today); len(l) != 2 {
		t.Fatal("日期區間", len(l))
	}
}

// 現金欠款分頁：送達了但錢沒收齊的現結單
func TestCashDebtFilter(t *testing.T) {
	_, boss := bossClient(t)
	ming := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"}))
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "address": "中正路 1 號"}))
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 900}))
	for _, paid := range []int{900, 500} {
		o := boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": []any{map[string]any{"product_id": gas, "qty": 1}}, "driver_id": ming})
		boss.ok("POST", fmt.Sprint("/api/orders/", id(o), "/deliver"), map[string]any{"collected_amount": paid})
	}
	if l := boss.list("/api/orders?debt=cash"); len(l) != 1 || l[0].(map[string]any)["paid_amount"] != float64(500) {
		t.Fatal("現金欠款只列沒收齊的", l)
	}
}

func TestShifts(t *testing.T) {
	_, boss := bossClient(t)
	ming := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"}))
	staff := id(boss.ok("POST", "/api/users", map[string]any{"name": "小美", "role": "staff", "login_name": "mei", "password": "pass1234"}))
	day := "2026-11-10"
	boss.ok("PUT", "/api/shifts", map[string]any{"driver_id": ming, "work_date": day, "slot": "am", "on": true})
	boss.ok("PUT", "/api/shifts", map[string]any{"driver_id": ming, "work_date": day, "slot": "pm", "on": true})
	boss.ok("PUT", "/api/shifts", map[string]any{"driver_id": ming, "work_date": day, "slot": "am", "on": true}) // 重按不會壞
	if l := boss.list("/api/shifts?date=" + day); len(l) != 2 {
		t.Fatal("排了上午下午", l)
	}
	boss.ok("PUT", "/api/shifts", map[string]any{"driver_id": ming, "work_date": day, "slot": "pm", "on": false})
	if l := boss.list("/api/shifts?date=" + day); len(l) != 1 {
		t.Fatal("取消下午", l)
	}
	if code, _, _ := boss.do("PUT", "/api/shifts", map[string]any{"driver_id": staff, "work_date": day, "slot": "am", "on": true}); code != 400 {
		t.Fatal("店員不能排班", code)
	}
	if code, _, _ := boss.do("PUT", "/api/shifts", map[string]any{"driver_id": ming, "work_date": day, "slot": "noon", "on": true}); code != 400 {
		t.Fatal("時段錯要擋", code)
	}
	boss.ok("PUT", "/api/shifts", map[string]any{"driver_id": ming, "work_date": "2026-11-11", "slot": "eve", "on": true})
	boss.ok("POST", "/api/shifts/copy", map[string]any{"from": day, "to": "2026-11-11"})
	if l := boss.list("/api/shifts?date=2026-11-11"); len(l) != 1 || l[0].(map[string]any)["slot"] != "am" {
		t.Fatal("照某天排要整份蓋過去", l)
	}
}

// 來電歷史、未處理來電：下了單或按已處理就消失
func TestCallHistoryUnhandled(t *testing.T) {
	a, boss := bossClient(t)
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "phone": "0912000001", "address": "中正路 1 號"}))
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 900}))
	ctx := nilCtx()
	k1, _ := a.IncomingCall(ctx, "0912000001", "test")
	a.IncomingCall(ctx, "0422235200", "test")
	a.IncomingCall(ctx, "0422235200", "test") // 同一支打兩次只算一通
	un := boss.list("/api/calls/history?unhandled=1")
	if len(un) != 2 {
		t.Fatal("未處理應該 2 通", un)
	}
	// 從來電記單 → 那通就處理好了
	boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": []any{map[string]any{"product_id": gas, "qty": 1}}, "call_id": k1["id"]})
	un = boss.list("/api/calls/history?unhandled=1")
	if len(un) != 1 || un[0].(map[string]any)["phone"] != "0422235200" {
		t.Fatal("下單後剩陌生號碼", un)
	}
	boss.ok("POST", fmt.Sprint("/api/calls/", n(un[0].(map[string]any)["id"]), "/handled"), map[string]any{})
	if un = boss.list("/api/calls/history?unhandled=1"); len(un) != 0 {
		t.Fatal("按已處理後清空（含同號碼較早那通）", un)
	}
	h := boss.list("/api/calls/history")
	if len(h) != 3 || h[2].(map[string]any)["order_no"] == nil {
		t.Fatal("歷史要列全部、標出下單", h)
	}
}

// 常叫品項、回購、訂單常態備註
func TestCustomerFavorites(t *testing.T) {
	_, boss := bossClient(t)
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "address": "中正路 1 號", "order_note": "不要給好桶"}))
	g20 := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 900}))
	g16 := id(boss.ok("POST", "/api/products", map[string]any{"name": "16kg 瓦斯", "category": "gas", "sale_price": 700}))
	for i := 0; i < 3; i++ {
		boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": []any{map[string]any{"product_id": g20, "qty": 1}}})
	}
	boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": []any{map[string]any{"product_id": g16, "qty": 2}}})
	c := boss.ok("GET", fmt.Sprint("/api/customers/", cid), nil)
	if c["order_note"] != "不要給好桶" {
		t.Fatal("常態備註", c["order_note"])
	}
	f := c["favorites"].(map[string]any)
	fr := f["frequent"].([]any)
	if len(fr) != 2 || n(fr[0].(map[string]any)["product_id"]) != g20 {
		t.Fatal("最常叫 20kg", fr)
	}
	last := f["last"].(map[string]any)["items"].([]any)
	if len(last) != 1 || n(last[0].(map[string]any)["qty"]) != 2 {
		t.Fatal("回購是上一張單", last)
	}
}

func TestDashboard(t *testing.T) {
	_, boss := bossClient(t)
	ming := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"}))
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "address": "中正路 1 號"}))
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 900}))
	o := boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": []any{map[string]any{"product_id": gas, "qty": 2}}, "driver_id": ming})
	boss.ok("POST", fmt.Sprint("/api/orders/", id(o), "/deliver"), map[string]any{"collected_amount": 1800})
	boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": []any{map[string]any{"product_id": gas, "qty": 1}}})
	d := boss.ok("GET", "/api/reports/dashboard?period=month", nil)
	if d["sales_today"] != float64(1800) || d["orders_today"] != float64(2) || d["due_today"] != float64(2) || d["done_today"] != float64(1) || d["dispatched_today"] != float64(1) {
		t.Fatal("今天的數字不對", d)
	}
	gs := d["gas_sales"].([]any)
	if len(gs) != 1 || gs[0].(map[string]any)["qty"] != float64(2) {
		t.Fatal("瓦斯品項銷售", gs)
	}
	m := d["months"].([]any)
	if len(m) != 12 || m[time.Now().Month()-1] != float64(1800) {
		t.Fatal("每月營業額", m)
	}
	if h := d["hourly"].([]any); len(h) != 24 {
		t.Fatal("每小時", h)
	}
}

// 登入有在用就自動延長
func TestSessionRenew(t *testing.T) {
	a, boss := bossClient(t)
	a.DB.Exec(`UPDATE sessions SET expires_at = datetime('now','localtime','+3 days')`)
	boss.ok("GET", "/api/me", nil)
	var left float64
	a.DB.QueryRow(`SELECT julianday(expires_at) - julianday('now','localtime') FROM sessions`).Scan(&left)
	if left < 59 {
		t.Fatal("應該延長回 60 天", left)
	}
	a.DB.Exec(`UPDATE sessions SET expires_at = datetime('now','localtime','-1 minutes')`)
	if code, _, _ := boss.do("GET", "/api/me", nil); code != 401 {
		t.Fatal("過期了就不能延長", code)
	}
}

func TestNightWindow(t *testing.T) {
	at := func(h int) time.Time { return time.Date(2026, 11, 10, h, 30, 0, 0, time.Local) }
	if !inNightWindow(at(2)) || !inNightWindow(at(4)) || inNightWindow(at(5)) || inNightWindow(at(1)) || inNightWindow(at(14)) {
		t.Fatal("半夜 2～5 點才自動更新")
	}
	a, _ := setup(t)
	if a.autoUpdateOn() {
		t.Fatal("不能自己更新的版本不能開")
	}
	a.ExePath, a.Restart = "x", func() {}
	if !a.autoUpdateOn() {
		t.Fatal("預設要開")
	}
	a.DB.Exec(`INSERT INTO settings (key, value) VALUES ('auto_update', 'off')`)
	if a.autoUpdateOn() {
		t.Fatal("設定關掉就不更新")
	}
}

// 第 4 版的舊資料庫升到第 5 版：舊訂單補上預約日期
func TestUpgradeDispatchV5(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO customers (name, address) VALUES ('甲', 'a')`,
		`DROP TRIGGER trg_orders_delivery_date`, `DROP TABLE driver_shifts`, `DROP INDEX idx_orders_delivery`,
		`ALTER TABLE orders DROP COLUMN delivery_date`, `ALTER TABLE orders DROP COLUMN delivery_slot`,
		`ALTER TABLE call_logs DROP COLUMN handled_at`, `ALTER TABLE call_logs DROP COLUMN order_id`, `ALTER TABLE customers DROP COLUMN order_note`,
		`INSERT INTO orders (order_no, customer_id, billing_type, address, ordered_at) VALUES ('A1', 1, 'cash', 'a', '2026-10-01 09:00:00')`,
		`UPDATE schema_version SET version = 4`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	db.Close()
	if db, err = OpenDB(path); err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var dd string
	var ver int
	db.QueryRow(`SELECT delivery_date FROM orders WHERE order_no = 'A1'`).Scan(&dd)
	db.QueryRow(`SELECT version FROM schema_version`).Scan(&ver)
	if dd != "2026-10-01" || ver != len(upgrades) {
		t.Fatal("升級第 5 版", dd, ver)
	}
}
