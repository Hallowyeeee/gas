package app

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// fakePush 把推播換成假的：回傳指定的狀態碼，記下送了幾次。
func fakePush(t *testing.T, code *int) *int {
	var mu sync.Mutex
	count := 0
	old := sendWebPush
	sendWebPush = func(b []byte, s *webpush.Subscription, o *webpush.Options) (*http.Response, error) {
		mu.Lock()
		defer mu.Unlock()
		count++
		return &http.Response{StatusCode: *code, Body: io.NopCloser(strings.NewReader(""))}, nil
	}
	t.Cleanup(func() { sendWebPush = old })
	return &count
}

func driverStatus(t *testing.T, boss *client, name string) string {
	st := boss.ok("GET", "/api/push/status", nil)
	for _, d := range st["drivers"].([]any) {
		m := d.(map[string]any)
		if m["name"] == name {
			return m["status"].(string)
		}
	}
	t.Fatal("找不到師傅", name)
	return ""
}

// 通知燈號：沒開通知紅燈 → 開了黃燈 → 送出手機回報收到綠燈 → 送了沒回報紅燈 → 手機取消就刪掉
func TestPushStatusLights(t *testing.T) {
	a, boss := bossClient(t)
	code := 201
	fakePush(t, &code)
	boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"})
	ming := boss.as(boss.ok("POST", "/api/login", map[string]any{"login_name": "ming", "password": "pass1234"})["token"].(string))

	if s := driverStatus(t, boss, "阿明"); s != "bad" {
		t.Fatal("沒開通知要紅燈", s)
	}
	if c, _, _ := ming.do("POST", "/api/push/test", map[string]any{}); c != 400 {
		t.Fatal("沒開通知不能測試", c)
	}
	sub := map[string]any{"endpoint": "https://fcm.example/abc", "keys": map[string]any{"p256dh": "k1", "auth": "a1"}, "platform": "samsung"}
	ming.ok("POST", "/api/push/subscribe", sub)
	if s := driverStatus(t, boss, "阿明"); s != "stale" {
		t.Fatal("開了還沒測要黃燈", s)
	}
	// 店裡對師傅送測試
	if r := boss.ok("POST", "/api/push/test", map[string]any{"driver_id": 2}); r["sent"] != float64(1) {
		t.Fatal("測試通知要送出", r)
	}
	if s := driverStatus(t, boss, "阿明"); s != "stale" {
		t.Fatal("剛送出還在等回報，黃燈", s)
	}
	// 手機回報收到（不用登入）
	anon := &client{t: t, srv: boss.srv}
	anon.ok("POST", "/api/push/ack", map[string]any{"endpoint": "https://fcm.example/abc"})
	if s := driverStatus(t, boss, "阿明"); s != "ok" {
		t.Fatal("收到要綠燈", s)
	}
	// 再送一次，3 分鐘都沒回報 → 紅燈
	a.DB.Exec(`UPDATE push_subscriptions SET last_ack_at = datetime('now','localtime','-10 minutes'), last_sent_at = datetime('now','localtime','-3 minutes')`)
	if s := driverStatus(t, boss, "阿明"); s != "bad" {
		t.Fatal("送了沒收到要紅燈", s)
	}
	// 很久以前收到過、之後沒再送 → 黃燈
	a.DB.Exec(`UPDATE push_subscriptions SET last_ack_at = datetime('now','localtime','-8 days'), last_sent_at = datetime('now','localtime','-8 days','-1 minutes')`)
	if s := driverStatus(t, boss, "阿明"); s != "stale" {
		t.Fatal("太久沒確認要黃燈", s)
	}
	// 通知伺服器說這支手機不能用了 → 刪掉、紅燈
	code = 410
	if c, _, _ := ming.do("POST", "/api/push/test", map[string]any{}); c != 400 {
		t.Fatal("手機取消了要告訴師傅重新開", c)
	}
	if s := driverStatus(t, boss, "阿明"); s != "bad" {
		t.Fatal("手機取消要紅燈", s)
	}
	if c, _, _ := ming.do("GET", "/api/push/status", nil); c != 403 {
		t.Fatal("師傅不能看大家的燈號", c)
	}
}

// 派單已讀、換師傅重算、沒看到再提醒
func TestSeenAndRemind(t *testing.T) {
	a, boss := bossClient(t)
	code := 201
	sent := fakePush(t, &code)
	ming := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"}))
	hua := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿華", "role": "driver", "login_name": "hua", "password": "pass1234"}))
	mingC := boss.as(boss.ok("POST", "/api/login", map[string]any{"login_name": "ming", "password": "pass1234"})["token"].(string))
	huaC := boss.as(boss.ok("POST", "/api/login", map[string]any{"login_name": "hua", "password": "pass1234"})["token"].(string))
	cid := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "address": "中正路 1 號"}))
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 900}))
	o := id(boss.ok("POST", "/api/orders", map[string]any{"customer_id": cid, "items": []any{map[string]any{"product_id": gas, "qty": 1}}}))
	dispatch := func(d int64, slot string) {
		boss.ok("POST", fmt.Sprint("/api/orders/", o, "/dispatch"), map[string]any{"driver_id": d, "delivery_slot": slot})
	}
	seenAt := func() any {
		var v any
		a.DB.QueryRow(`SELECT seen_at FROM orders WHERE id = ?`, o).Scan(&v)
		return v
	}
	dispatch(ming, "am")
	if l := boss.list("/api/orders?status=dispatched"); l[0].(map[string]any)["seen_at"] != nil {
		t.Fatal("剛派還沒看")
	}
	if r := huaC.ok("POST", "/api/orders/seen", map[string]any{"ids": []int64{o}}); r["seen"] != float64(0) {
		t.Fatal("別的師傅不能標已看", r)
	}
	if c, _, _ := boss.do("POST", "/api/orders/seen", map[string]any{"ids": []int64{o}}); c != 403 {
		t.Fatal("只有師傅能標已看", c)
	}
	if r := mingC.ok("POST", "/api/orders/seen", map[string]any{"ids": []int64{o}}); r["seen"] != float64(1) {
		t.Fatal("師傅看到", r)
	}
	if l := boss.list("/api/orders?status=dispatched"); l[0].(map[string]any)["seen_at"] == nil {
		t.Fatal("電腦要看得到已看")
	}
	// 同一位師傅改時段：已看不變
	dispatch(ming, "pm")
	if seenAt() == nil {
		t.Fatal("只改時段不用重看")
	}
	// 換給阿華：要重新看
	dispatch(hua, "pm")
	if seenAt() != nil {
		t.Fatal("換師傅要重新算")
	}
	// 還沒到 3 分鐘不提醒
	if n := a.RemindUnseen(); n != 0 {
		t.Fatal("還沒到時間", n)
	}
	a.DB.Exec(`UPDATE orders SET dispatched_at = datetime('now','localtime','-4 minutes') WHERE id = ?`, o)
	if n := a.RemindUnseen(); n != 1 {
		t.Fatal("超過 3 分鐘要提醒", n)
	}
	if n := a.RemindUnseen(); n != 0 {
		t.Fatal("只提醒一次", n)
	}
	// 設定改 10 分鐘：重新派一張還沒到時間
	boss.ok("PUT", "/api/settings", map[string]any{"remind_minutes": "10"})
	a.DB.Exec(`UPDATE orders SET reminded_at = NULL WHERE id = ?`, o)
	if n := a.RemindUnseen(); n != 0 {
		t.Fatal("改成 10 分鐘", n)
	}
	boss.ok("PUT", "/api/settings", map[string]any{"remind_minutes": "0"})
	a.DB.Exec(`UPDATE orders SET dispatched_at = datetime('now','localtime','-60 minutes') WHERE id = ?`, o)
	if n := a.RemindUnseen(); n != 0 {
		t.Fatal("0 = 不提醒", n)
	}
	// 收回派單：已讀、提醒都清掉
	huaC.ok("POST", "/api/orders/seen", map[string]any{"ids": []int64{o}})
	boss.ok("POST", fmt.Sprint("/api/orders/", o, "/undispatch"), map[string]any{})
	if seenAt() != nil {
		t.Fatal("收回要清掉已看")
	}
	_ = sent
}
