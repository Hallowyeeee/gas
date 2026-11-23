package app

import (
	"fmt"
	"testing"
)

// 瓦斯沒填客人價格：可以先記單，不能派單；填了價格後單子自動補價、可以派
func TestGasPriceRequiredForDispatch(t *testing.T) {
	a, boss := bossClient(t)
	ming := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"}))
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "address": "中正路 1 號"}))
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas"}))
	hose := id(boss.ok("POST", "/api/products", map[string]any{"name": "瓦斯管", "category": "consumable", "sale_price": 450}))
	items := []any{map[string]any{"product_id": gas, "qty": 2}, map[string]any{"product_id": hose, "qty": 1}}

	if code, _, _ := boss.do("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items, "driver_id": ming}); code != 400 {
		t.Fatal("沒價格直接派單要擋", code)
	}
	o := boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": items})
	if o["total_amount"] != float64(450) {
		t.Fatal("瓦斯沒價格算 0、管子照售價", o["total_amount"])
	}
	if code, _, _ := boss.do("POST", fmt.Sprint("/api/orders/", id(o), "/dispatch"), map[string]any{"driver_id": ming}); code != 400 {
		t.Fatal("沒價格不能派", code)
	}
	boss.ok("PUT", fmt.Sprint("/api/customers/", cid, "/prices"), map[string]any{"product_id": gas, "price": 880})
	o2 := boss.ok("GET", fmt.Sprint("/api/orders/", id(o)), nil)
	if o2["total_amount"] != float64(880*2+450) {
		t.Fatal("填價格後補上還沒送的單", o2["total_amount"])
	}
	boss.ok("POST", fmt.Sprint("/api/orders/", id(o), "/dispatch"), map[string]any{"driver_id": ming})

	// 調漲日期
	boss.ok("PUT", fmt.Sprint("/api/customers/", cid), map[string]any{"price_raised_on": "2026-11-23"})
	if c := boss.ok("GET", fmt.Sprint("/api/customers/", cid), nil); c["price_raised_on"] != "2026-11-23" {
		t.Fatal("調漲日期", c["price_raised_on"])
	}

	// 舊客人帶入價格：用上一張單的單價（第一次開新版做一次）
	cid2 := id(boss.ok("POST", "/api/customers", map[string]any{"name": "李大華", "address": "中山路 2 號"}))
	a.DB.Exec(`INSERT INTO orders (order_no, customer_id, billing_type, address) VALUES ('X1', ?, 'cash', 'x')`, cid2)
	a.DB.Exec(`INSERT INTO order_items (order_id, product_id, qty, unit_price, amount) VALUES ((SELECT id FROM orders WHERE order_no='X1'), ?, 1, 950, 950)`, gas)
	a.DB.Exec(`DELETE FROM settings WHERE key = 'customer_prices_seeded'`)
	seedCustomerPrices(a.DB)
	var p int
	a.DB.QueryRow(`SELECT price FROM customer_prices WHERE customer_id = ? AND product_id = ?`, cid2, gas).Scan(&p)
	if p != 950 {
		t.Fatal("用上一張單帶入價格", p)
	}
	a.DB.QueryRow(`SELECT price FROM customer_prices WHERE customer_id = ? AND product_id = ?`, cid, gas).Scan(&p)
	if p != 880 {
		t.Fatal("已經有價格不能蓋掉", p)
	}
}
