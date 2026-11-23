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
	r1 := ming.ok("POST", "/api/meter/readings", map[string]any{"customer_id": cid, "reading": 120.5, "collected_amount": 600, "photo": "data:image/jpeg;base64,/9j/4AAQ"})
	if r1["usage"] != float64(20) || r1["amount"] != float64(1000) || r1["has_photo"] != float64(1) {
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
