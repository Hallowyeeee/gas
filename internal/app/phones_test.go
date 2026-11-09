package app

import (
	"fmt"
	"path/filepath"
	"testing"
)

// 一位客戶可以有很多支電話：任何一支打來都認得、搜尋找得到、可以換主要號碼
func TestCustomerPhones(t *testing.T) {
	_, c := setup(t)
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	boss := c.as(res["token"].(string))
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "大公司", "phone": "0912000001", "phone2": "0912000002", "address": "中正路 1 號"}))
	other := id(boss.ok("POST", "/api/customers", map[string]any{"name": "別人", "phone": "0955000000", "address": "民生路 3 號"}))
	path := fmt.Sprint("/api/customers/", cid)

	// 電話 2 自動收進「其他電話」
	cust := boss.ok("GET", path, nil)
	if cust["phone2"] != nil || len(cust["phones"].([]any)) != 1 {
		t.Fatal("電話 2 應該搬進其他電話", cust)
	}
	for i := 3; i <= 25; i++ {
		boss.ok("POST", path+"/phones", map[string]any{"phone": fmt.Sprintf("09120000%02d", i), "note": "分機"})
	}
	if code, _, _ := boss.do("POST", path+"/phones", map[string]any{"phone": "0912000003"}); code != 400 {
		t.Fatal("重複電話應該擋下來", code)
	}
	if code, _, _ := boss.do("POST", path+"/phones", map[string]any{"phone": "0912-000-001"}); code != 400 {
		t.Fatal("跟主要號碼一樣應該擋下來", code)
	}
	phones := boss.ok("GET", path, nil)["phones"].([]any)
	if len(phones) != 24 {
		t.Fatal("應該有 24 支其他電話", len(phones))
	}

	// 卡片裡的號碼打來也認得
	m := boss.ok("POST", "/api/calls/test", map[string]any{"phone": "0912000020"})["matches"].([]any)
	if len(m) != 1 || n(m[0].(map[string]any)["id"]) != cid {
		t.Fatal("其他電話打來應該對到大公司", m)
	}
	// 搜尋找得到
	if l := boss.list("/api/customers?q=0912000017"); len(l) != 1 || n(l[0].(map[string]any)["id"]) != cid {
		t.Fatal("搜尋其他電話應該找到", l)
	}

	// 換主要號碼：原本的收進卡片
	pid := n(phones[5].(map[string]any)["id"])
	newMain := phones[5].(map[string]any)["phone"]
	boss.ok("POST", fmt.Sprint(path, "/phones/", pid, "/main"), map[string]any{})
	cust = boss.ok("GET", path, nil)
	if cust["phone"] != newMain || len(cust["phones"].([]any)) != 24 {
		t.Fatal("換主要號碼不對", cust["phone"], len(cust["phones"].([]any)))
	}
	if l := boss.list("/api/customers?q=0912000001"); len(l) != 1 {
		t.Fatal("舊主要號碼應該還找得到", l)
	}

	// 刪除；別的客戶的電話不能從這裡刪
	first := n(cust["phones"].([]any)[0].(map[string]any)["id"])
	boss.ok("DELETE", fmt.Sprint("/api/customers/", other, "/phones/", first), nil)
	if len(boss.ok("GET", path, nil)["phones"].([]any)) != 24 {
		t.Fatal("不該刪到別人的電話")
	}
	boss.ok("DELETE", fmt.Sprint(path, "/phones/", first), nil)
	if len(boss.ok("GET", path, nil)["phones"].([]any)) != 23 {
		t.Fatal("刪除失敗")
	}

	// 沒有主要號碼的客戶，加的第一支直接當主要號碼
	nobody := id(boss.ok("POST", "/api/customers", map[string]any{"name": "沒電話", "address": "某路"}))
	boss.ok("POST", fmt.Sprint("/api/customers/", nobody, "/phones"), map[string]any{"phone": "0977000000"})
	if c := boss.ok("GET", fmt.Sprint("/api/customers/", nobody), nil); c["phone"] != "0977000000" {
		t.Fatal("應該變成主要號碼", c)
	}
}

// 第 3 版升到第 4 版：原本的電話 2 搬進其他電話
func TestUpgradePhonesV4(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DROP TRIGGER trg_customers_phone2_ins`, `DROP TRIGGER trg_customers_phone2_upd`, `DROP TABLE customer_phones`,
		`INSERT INTO customers (name, phone, phone2, address) VALUES ('甲', '0911', '0922', 'a'), ('乙', '0933', '0933', 'b')`,
		`UPDATE schema_version SET version = 3`,
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
	var cnt, left int
	db.QueryRow(`SELECT COUNT(*) FROM customer_phones`).Scan(&cnt)
	db.QueryRow(`SELECT COUNT(*) FROM customers WHERE phone2 IS NOT NULL`).Scan(&left)
	if cnt != 1 || left != 0 {
		t.Fatal("升級搬電話不對", cnt, left)
	}
}

// 記單時可以選現金／轉帳，沒選就是現金
func TestOrderPayMethod(t *testing.T) {
	_, c := setup(t)
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	boss := c.as(res["token"].(string))
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "phone": "0912000001", "address": "中正路 1 號"}))
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 900}))
	items := []any{map[string]any{"product_id": gas, "qty": 1}}
	o1 := boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items})
	o2 := boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items, "pay_method": "transfer"})
	if o1["pay_method"] != "cash" || o2["pay_method"] != "transfer" {
		t.Fatal("付款方式不對", o1["pay_method"], o2["pay_method"])
	}
	if code, _, _ := boss.do("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items, "pay_method": "card"}); code != 400 {
		t.Fatal("不認得的付款方式應該擋下來", code)
	}
	l := boss.list("/api/orders")
	if len(l) != 2 || l[0].(map[string]any)["pay_method"] != "transfer" {
		t.Fatal("訂單列表要有付款方式", l)
	}
	boss.ok("PUT", fmt.Sprint("/api/orders/", id(o2)), map[string]any{"pay_method": "cash"})
	if o := boss.ok("GET", fmt.Sprint("/api/orders/", id(o2)), nil); o["pay_method"] != "cash" {
		t.Fatal("改付款方式失敗", o["pay_method"])
	}
}
