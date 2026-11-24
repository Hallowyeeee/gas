package app

import (
	"fmt"
	"testing"
	"time"
)

// 抄錶：第一次當起始度數、之後算度數和金額、算進欠款、月結帳單、作廢、統計
func TestMeterReadings(t *testing.T) {
	_, boss := bossClient(t)
	boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"})
	ming := boss.as(boss.ok("POST", "/api/login", map[string]any{"login_name": "ming", "password": "pass1234"})["token"].(string))
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "大樓 A", "address": "中正路 1 號", "charge_type": "meter", "meter_no": "M-01"}))
	boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "address": "中正路 2 號"})

	l := ming.ok("GET", "/api/meter/customers", nil)
	if cs := l["customers"].([]any); len(cs) != 1 || l["mode"] != "both" {
		t.Fatal("只列計流量客人", l)
	}
	if code, _, _ := ming.do("POST", "/api/meter/readings", map[string]any{"customer_id": cid, "reading": 100}); code != 400 {
		t.Fatal("沒單價不能抄", code)
	}
	boss.ok("PUT", fmt.Sprint("/api/customers/", cid), map[string]any{"meter_price": 50, "meter_start": "100.5"})
	if code, _, _ := ming.do("POST", "/api/meter/readings", map[string]any{"customer_id": cid, "reading": 120.5}); code != 400 {
		t.Fatal("沒填流量係數不能記", code)
	}
	boss.ok("PUT", fmt.Sprint("/api/customers/", cid), map[string]any{"meter_coef": 1})
	r1 := ming.ok("POST", "/api/meter/readings", map[string]any{"customer_id": cid, "reading": 120.5, "collected_amount": 600, "photo": "data:image/jpeg;base64,/9j/4AAQ"})
	if r1["usage"] != float64(20) || r1["kg"] != float64(20) || r1["amount"] != float64(1000) || r1["has_photo"] != float64(1) {
		t.Fatal("度數、金額", r1)
	}
	if code, _, _ := ming.do("POST", "/api/meter/readings", map[string]any{"customer_id": cid, "reading": 110}); code != 400 {
		t.Fatal("比上次小要擋", code)
	}
	c := boss.ok("GET", fmt.Sprint("/api/customers/", cid), nil)
	if c["balance"] != float64(400) {
		t.Fatal("抄錶金額 − 收款 = 欠款", c["balance"])
	}
	r2 := ming.ok("POST", "/api/meter/readings", map[string]any{"customer_id": cid, "reading": 125.5})
	boss.ok("POST", fmt.Sprint("/api/meter/readings/", id(r2), "/cancel"), map[string]any{})
	month := time.Now().Format("2006-01")
	st := boss.ok("GET", "/api/usage?month="+month, nil)
	m := st["meter"].([]any)[0].(map[string]any)
	if m["usage"] != float64(20) || m["amount"] != float64(1000) || m["from_reading"] != 100.5 || m["to_reading"] != 120.5 {
		t.Fatal("統計（作廢的不算）", m)
	}
	if code, _, _ := ming.do("GET", "/api/usage", nil); code != 403 {
		t.Fatal("師傅不能看統計", code)
	}
}

// 照舊系統請款單的例子：80 度 × 係數 5.12 = 409.60 公斤，× 32.9 = 13475.84 → 無條件進位 13476
func TestMeterKgAndBill(t *testing.T) {
	if kg, amt := meterAmount(80, 5.12, 32.9); kg != 409.6 || amt != 13476 {
		t.Fatal(kg, amt)
	}
	if _, amt := meterAmount(10, 1, 30); amt != 300 {
		t.Fatal("剛好整數不進位", amt)
	}
	_, boss := bossClient(t)
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "新福", "phone": "0953224001", "address": "慶門街 73 號",
		"charge_type": "meter", "meter_price": "32.9", "meter_coef": "5.12", "meter_start": "6079"}))
	r0 := boss.ok("POST", "/api/meter/readings", map[string]any{"customer_id": cid, "reading": 6079})
	r1 := boss.ok("POST", "/api/meter/readings", map[string]any{"customer_id": cid, "reading": 6159})
	if r1["usage"] != float64(80) || r1["kg"] != 409.6 || r1["unit_price"] != 32.9 || r1["amount"] != float64(13476) {
		t.Fatal("公斤、金額", r1)
	}
	b := boss.ok("GET", fmt.Sprint("/api/meter/readings/", id(r1), "/bill"), nil)
	if b["customer_name"] != "新福" || b["period_from"] != r0["read_at"] || b["notice"] == "" || b["made_by"] == "" || b["stove_amount"] != float64(0) {
		t.Fatal("請款單", b)
	}
	boss.ok("PUT", "/api/settings", map[string]any{"bill_notice": "自己的注意事項"})
	if b := boss.ok("GET", fmt.Sprint("/api/meter/readings/", id(r1), "/bill"), nil); b["notice"] != "自己的注意事項" {
		t.Fatal("注意事項用設定", b["notice"])
	}
}
