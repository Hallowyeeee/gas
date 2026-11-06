package app

import (
	"path/filepath"
	"testing"
)

func TestNormalizePhone(t *testing.T) {
	cases := map[string]string{"0912-345-678": "0912345678", "+886 912 345 678": "0912345678", "(02)2345-6789": "0223456789"}
	for in, want := range cases {
		if got := normalizePhone(in); got != want {
			t.Errorf("%s → %s，應該是 %s", in, got, want)
		}
	}
	if !samePhone("23456789", "0223456789") || samePhone("123", "0223456123") || samePhone("0912345678", "0912345679") {
		t.Error("號碼比對錯誤")
	}
}

func TestIncomingCall(t *testing.T) {
	_, c := setup(t)
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	boss := c.as(res["token"].(string))
	wang := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "phone": "0912-000-001", "address": "中正路 1 號"}))
	boss.ok("POST", "/api/customers", map[string]any{"name": "林太太", "phone2": "2345-6789", "address": "民生路 3 號"})
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 900}))
	boss.ok("POST", "/api/orders", map[string]any{"customer_id": wang, "items": []any{map[string]any{"product_id": gas, "qty": 2}}})

	call := boss.ok("POST", "/api/calls/test", map[string]any{"phone": "0912000001"})
	m := call["matches"].([]any)
	if len(m) != 1 {
		t.Fatal("應該對到王小明", call)
	}
	w := m[0].(map[string]any)
	if w["name"] != "王小明" || n(w["open_orders"]) != 1 || w["last_order"].(map[string]any)["items"] != "20kg 瓦斯 ×2" {
		t.Fatal("來電資料不對", w)
	}
	// 沒打區碼也對得到
	if m := boss.ok("POST", "/api/calls/test", map[string]any{"phone": "02-2345-6789"})["matches"].([]any); len(m) != 1 || m[0].(map[string]any)["name"] != "林太太" {
		t.Fatal("林太太應該對得到", m)
	}
	// 新客人
	if m := boss.ok("POST", "/api/calls/test", map[string]any{"phone": "0988777666"})["matches"].([]any); len(m) != 0 {
		t.Fatal("不該對到", m)
	}
	if l := boss.list("/api/calls"); len(l) != 3 || l[2].(map[string]any)["customer_name"] != "王小明" {
		t.Fatal("來電紀錄不對", l)
	}
	// 本機程式通知來電
	if code, _, _ := c.do("GET", "/api/local/call?phone=0912000001", nil); code != 200 {
		t.Fatal("本機通知來電失敗", code)
	}
}

// 舊版（第 1 版）資料庫開啟時要自動加上來電紀錄表
func TestUpgradeFromV1(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	db, err := OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	db.Exec(`DROP TABLE call_logs`)
	db.Exec(`UPDATE schema_version SET version = 1`)
	db.Close()
	db, err = OpenDB(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var v int
	db.QueryRow(`SELECT version FROM schema_version`).Scan(&v)
	if _, err := db.Exec(`INSERT INTO call_logs (phone) VALUES ('0912')`); err != nil || v != 2 {
		t.Fatal("升級失敗", v, err)
	}
}
