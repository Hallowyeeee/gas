package app

import (
	"database/sql"
	"encoding/json"
	"strings"
)

// 瓦斯價格改成「每位客人自己一個價格」：瓦斯商品不用填售價，
// 客人沒填價格的瓦斯可以先記單，但不能派單。

// ensurePriceColumns 加「調漲日期」欄位。不用資料庫版本號，
// 欄位還沒有才加，避免跟其他功能的升級版本撞號。
func ensurePriceColumns(db *sql.DB) error {
	return ensureColumn(db, "customers", "price_raised_on", "TEXT")
}

// seedCustomerPrices 第一次開新版時做一次：幫已經有的客人帶入瓦斯價格。
//  1. 用 F12 抓過的舊系統價格（依規格對到瓦斯商品）
//  2. 沒有的話，用這位客人上一張單（沒取消、價格大於 0）的單價
//
// 已經有價格的不會蓋掉。做過就在 settings 記一筆，之後不再做。
func seedCustomerPrices(db *sql.DB) {
	var done string
	db.QueryRow(`SELECT value FROM settings WHERE key = 'customer_prices_seeded'`).Scan(&done)
	if done != "" {
		return
	}
	gas := map[int64]string{}
	if rs, err := db.Query(`SELECT id, name FROM products WHERE category = 'gas' AND active = 1`); err == nil {
		for rs.Next() {
			var id int64
			var name string
			if rs.Scan(&id, &name) == nil {
				gas[id] = strings.ToLower(squash(name))
			}
		}
		rs.Close()
	}
	type fill struct{ cid, pid, price int64 }
	var fills []fill
	if rs, err := db.Query(`SELECT customer_id, prices FROM legacy_customer_info WHERE prices IS NOT NULL AND prices <> ''`); err == nil {
		for rs.Next() {
			var cid int64
			var raw string
			if rs.Scan(&cid, &raw) != nil {
				continue
			}
			var prices map[string]int
			if json.Unmarshal([]byte(raw), &prices) != nil {
				continue
			}
			for size, price := range prices {
				if price <= 0 {
					continue
				}
				key := strings.ToLower(size)
				var hit int64
				n := 0
				for id, name := range gas {
					if strings.Contains(name, key) {
						hit = id
						n++
					}
				}
				if n == 1 {
					fills = append(fills, fill{cid, hit, int64(price)})
				}
			}
		}
		rs.Close()
	}
	for _, f := range fills {
		db.Exec(`INSERT OR IGNORE INTO customer_prices (customer_id, product_id, price) VALUES (?, ?, ?)`, f.cid, f.pid, f.price)
	}
	db.Exec(`INSERT OR IGNORE INTO customer_prices (customer_id, product_id, price)
		SELECT o.customer_id, oi.product_id,
		  (SELECT oi2.unit_price FROM order_items oi2 JOIN orders o2 ON o2.id = oi2.order_id
		   WHERE o2.customer_id = o.customer_id AND oi2.product_id = oi.product_id AND o2.status <> 'cancelled' AND oi2.unit_price > 0
		   ORDER BY o2.ordered_at DESC, o2.id DESC LIMIT 1)
		FROM order_items oi JOIN orders o ON o.id = oi.order_id JOIN products p ON p.id = oi.product_id
		WHERE p.category = 'gas' AND o.status <> 'cancelled' AND oi.unit_price > 0
		GROUP BY o.customer_id, oi.product_id`)
	db.Exec(`INSERT INTO settings (key, value) VALUES ('customer_prices_seeded', datetime('now','localtime'))
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
}

// missingGasPrice 這張單有沒有瓦斯還沒填價格（單價 0）。
func missingGasPrice(q interface {
	QueryRow(string, ...any) *sql.Row
}, orderID int64) bool {
	var n int
	q.QueryRow(`SELECT COUNT(*) FROM order_items oi JOIN products p ON p.id = oi.product_id
		WHERE oi.order_id = ? AND p.category = 'gas' AND oi.unit_price = 0`, orderID).Scan(&n)
	return n > 0
}

const msgNoPrice = "這張單的瓦斯還沒填這位客人的價格，填好價格才能派單"
