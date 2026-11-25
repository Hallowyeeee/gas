package app

import (
	"fmt"
	"testing"
	"time"
)

// 鋼瓶清單：登記、掃條碼找、換貼紙（舊條碼還找得到）、到期提醒、報廢
func TestCylinders(t *testing.T) {
	_, boss := bossClient(t)
	boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"})
	ming := boss.as(boss.ok("POST", "/api/login", map[string]any{"login_name": "ming", "password": "pass1234"})["token"].(string))

	day := func(n int) string { return time.Now().AddDate(0, 0, n).Format("2006-01-02") }
	c1 := ming.ok("POST", "/api/cylinders", map[string]any{"serial": " 707060 ", "label_code": "aw16586603", "size_kg": 20, "tare_kg": 22.22,
		"made_on": "1999-01", "inspected_on": "2024-01-11", "next_due": day(-300), "photo": "data:image/jpeg;base64,/9j/4AAQ"})
	if c1["serial"] != "707060" || c1["label_code"] != "AW16586603" || c1["due_state"] != "overdue" || c1["has_photo"] != float64(1) {
		t.Fatal("登記", c1)
	}
	ming.ok("POST", "/api/cylinders", map[string]any{"serial": "A1", "next_due": day(10)})
	ming.ok("POST", "/api/cylinders", map[string]any{"serial": "A2", "next_due": day(400)})
	if code, _, _ := ming.do("POST", "/api/cylinders", map[string]any{"serial": "707060", "next_due": day(1)}); code != 400 {
		t.Fatal("容器號碼重複要擋", code)
	}
	if code, _, _ := ming.do("POST", "/api/cylinders", map[string]any{"serial": "B9", "label_code": "AW16586603", "next_due": day(1)}); code != 400 {
		t.Fatal("條碼重複要擋", code)
	}
	if code, _, _ := ming.do("POST", "/api/cylinders", map[string]any{"serial": "B9", "next_due": "115-01-11"}); code != 400 {
		t.Fatal("日期格式要擋", code)
	}

	s := boss.ok("GET", "/api/cylinders/summary", nil)
	if s["total"] != float64(3) || s["overdue"] != float64(1) || s["soon"] != float64(1) {
		t.Fatal("提醒數字", s)
	}
	if l := boss.list("/api/cylinders?filter=soon"); len(l) != 1 || l[0].(map[string]any)["serial"] != "A1" {
		t.Fatal("快到期清單", l)
	}

	f := ming.ok("GET", "/api/cylinders/lookup?code=AW16586603", nil)
	if f["found"] != true || f["by"] != "label" {
		t.Fatal("掃條碼找", f)
	}
	cid := id(c1)
	ming.ok("POST", fmt.Sprint("/api/cylinders/", cid, "/relabel"), map[string]any{"label_code": "AX34299670", "next_due": day(1800), "inspected_on": day(0)})
	f = ming.ok("GET", "/api/cylinders/lookup?code=AW16586603", nil)
	if f["by"] != "old_label" || f["cylinder"].(map[string]any)["label_code"] != "AX34299670" {
		t.Fatal("舊條碼還找得到", f)
	}
	if f = ming.ok("GET", "/api/cylinders/lookup?code=nope", nil); f["found"] != false {
		t.Fatal("找不到", f)
	}
	d := boss.ok("GET", fmt.Sprint("/api/cylinders/", cid), nil)
	if d["due_state"] != "ok" || len(d["old_labels"].([]any)) != 1 {
		t.Fatal("換貼紙後", d)
	}

	cust := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "address": "中正路 2 號"}))
	u := ming.ok("PUT", fmt.Sprint("/api/cylinders/", cid), map[string]any{"status": "customer", "customer_id": cust})
	if u["customer_name"] != "王小明" {
		t.Fatal("在客人家", u)
	}
	boss.ok("PUT", fmt.Sprint("/api/cylinders/", cid), map[string]any{"status": "scrapped"})
	if s = boss.ok("GET", "/api/cylinders/summary", nil); s["total"] != float64(2) {
		t.Fatal("報廢的不算", s)
	}
}
