package app

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
)

// 書籤小工具送網頁 → 待確認 → 確認存檔 → 客戶頁看得到舊紀錄；再送一次不會重複
func TestLegacyCaptureFlow(t *testing.T) {
	a, c := setup(t)
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	boss := c.as(res["token"].(string))
	g20 := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg 瓦斯", "category": "gas", "sale_price": 800}))
	// 已經有這位客人（電話一樣），應該對得到
	old := id(boss.ok("POST", "/api/customers", map[string]any{"name": "金百利", "phone": "0912-000-111", "address": "舊地址"}))

	page, err := os.ReadFile("testdata/easy88_order_form.html")
	if err != nil {
		t.Fatal(err)
	}
	send := func(origin string) (int, map[string]any) {
		body, _ := json.Marshal(map[string]string{"url": "http://www.easy88.com.tw:20040/gas_station/order/order_form.asp", "html": string(page)})
		req, _ := http.NewRequest("POST", c.srv.URL+"/api/local/capture", bytes.NewReader(body))
		req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
		req.Header.Set("Origin", origin)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var m map[string]any
		json.NewDecoder(resp.Body).Decode(&m)
		if origin != "" && resp.StatusCode == 200 && resp.Header.Get("Access-Control-Allow-Origin") != origin {
			t.Fatal("要允許舊系統網頁讀回應")
		}
		return resp.StatusCode, m
	}
	// 別的網站不能送
	if code, _ := send("http://evil.example.com"); code != 403 {
		t.Fatal("別的網站應該被擋", code)
	}
	code, m := send("http://www.easy88.com.tw:20040")
	if code != 200 || m["name"] != "測試客戶甲" || n(m["deliveries"]) != 33 {
		t.Fatal("抓取失敗", code, m)
	}
	capID := id(m)
	// 不是客戶頁
	body, _ := json.Marshal(map[string]string{"html": "<html><body>登入</body></html>"})
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/local/capture", bytes.NewReader(body))
	if resp, _ := http.DefaultClient.Do(req); resp.StatusCode != 400 {
		t.Fatal("不是客戶頁應該說明", resp.StatusCode)
	}
	// 還沒確認又按一次：不會多一筆
	send("http://www.easy88.com.tw:20040")
	if l := boss.list("/api/legacy/captures"); len(l) != 1 {
		t.Fatal("待確認應該只有一筆", l)
	}

	got := boss.ok("GET", "/api/legacy/captures/"+itoa(capID), nil)
	matches := got["matches"].([]any)
	if len(matches) != 1 || n(matches[0].(map[string]any)["id"]) != old {
		t.Fatal("應該用電話對到舊客戶", matches)
	}
	if p := got["products"].(map[string]any)["20kg"]; p == nil || n(p.(map[string]any)["id"]) != g20 {
		t.Fatal("20kg 售價應該對到 20kg 瓦斯", got["products"])
	}
	pg := got["page"].(map[string]any)
	save := boss.ok("POST", "/api/legacy/captures/"+itoa(capID)+"/save", map[string]any{
		"customer_id": old,
		"customer":    map[string]any{"name": pg["name"], "phone": pg["phone"], "address": pg["address"], "area": pg["area"], "note": pg["note"], "needs_invoice": false},
		"prices":      []any{map[string]any{"product_id": g20, "price": 770}},
	})
	if n(save["customer_id"]) != old || n(save["deliveries_added"]) != 33 || save["backup"] == "" || n(save["refill_days"]) <= 0 {
		t.Fatal("存檔結果不對", save)
	}
	cust := boss.ok("GET", "/api/customers/"+itoa(old), nil)
	if cust["name"] != "測試客戶甲" || cust["legacy_id"] != "86000" || cust["code"] != "86000" || cust["area"] != "中壢區" {
		t.Fatal("客戶資料沒更新", cust)
	}
	lg := cust["legacy"].(map[string]any)
	if len(lg["deliveries"].([]any)) != 33 || len(lg["cylinders_out"].([]any)) != 4 {
		t.Fatal("舊紀錄不對", lg)
	}
	if b := lg["balance"].([]any); len(b) != 1 || n(b[0].(map[string]any)["qty"]) != -2 {
		t.Fatal("桶子結餘不對", b)
	}
	if p := cust["prices"].([]any); len(p) != 1 || n(p[0].(map[string]any)["price"]) != 770 {
		t.Fatal("特價不對", p)
	}
	// 來電：還沒新訂單，顯示舊系統最後一次
	call := boss.ok("POST", "/api/calls/test", map[string]any{"phone": "0912000111"})
	lo := call["matches"].([]any)[0].(map[string]any)["last_order"].(map[string]any)
	if lo["items"] != "20kg×1(770)" || n(lo["total_amount"]) != 770 {
		t.Fatal("來電應該看到舊系統最後一次送貨", lo)
	}
	// 已經存過的不能再存
	if code, _, _ := boss.do("POST", "/api/legacy/captures/"+itoa(capID)+"/save", map[string]any{"customer": map[string]any{"name": "x", "address": "y"}}); code != 400 {
		t.Fatal("重複存應該被擋", code)
	}
	// 下次來電再按一次：用舊系統編號對到，不會重複送貨紀錄
	_, m = send("http://www.easy88.com.tw:20040")
	got = boss.ok("GET", "/api/legacy/captures/"+itoa(id(m)), nil)
	if mm := got["matches"].([]any); len(mm) != 1 || mm[0].(map[string]any)["match_by"] != "舊系統編號" || n(got["saved_deliveries"]) != 33 {
		t.Fatal("第二次應該用編號對到", got)
	}
	save = boss.ok("POST", "/api/legacy/captures/"+itoa(id(m))+"/save", map[string]any{"customer_id": old, "customer": map[string]any{"name": "測試客戶甲", "address": "地址"}})
	if n(save["deliveries_added"]) != 0 || save["backup"] != "" {
		t.Fatal("第二次不該新增紀錄、今天不用再備份", save)
	}
	var cnt int
	a.DB.QueryRow(`SELECT COUNT(*) FROM legacy_deliveries`).Scan(&cnt)
	if cnt != 33 {
		t.Fatal("送貨紀錄重複了", cnt)
	}
	// 取消
	_, m = send("http://www.easy88.com.tw:20040")
	boss.ok("POST", "/api/legacy/captures/"+itoa(id(m))+"/cancel", map[string]any{})
	if l := boss.list("/api/legacy/captures"); len(l) != 0 {
		t.Fatal("取消後待確認應該是空的", l)
	}
	// 書籤小工具用表單送（開小視窗）：回一個會自己關掉的小頁面
	form := url.Values{"url": {"http://www.easy88.com.tw:20040/x"}, "html": {string(page)}}
	freq, _ := http.NewRequest("POST", c.srv.URL+"/api/local/capture", strings.NewReader(form.Encode()))
	freq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	freq.Header.Set("Origin", "http://www.easy88.com.tw:20040")
	fresp, err := http.DefaultClient.Do(freq)
	if err != nil {
		t.Fatal(err)
	}
	fb, _ := io.ReadAll(fresp.Body)
	fresp.Body.Close()
	if fresp.StatusCode != 200 || !strings.Contains(string(fb), "已送到瓦斯行：測試客戶甲") {
		t.Fatal("表單送出失敗", fresp.StatusCode, string(fb))
	}
	if l := boss.list("/api/legacy/captures"); len(l) != 1 {
		t.Fatal("表單送的應該進待確認", l)
	}
	// 換瓶提醒算得到舊紀錄
	a.DB.Exec(`UPDATE customers SET refill_days = 1 WHERE id = ?`, old)
	var last string
	a.DB.QueryRow(`SELECT last_delivered FROM v_refill_due WHERE customer_id = ?`, old).Scan(&last)
	if last != "2026-10-21" {
		t.Fatal("換瓶提醒應該用舊紀錄最後一次 2026-10-21", last)
	}
}

func TestTypicalInterval(t *testing.T) {
	if d := typicalInterval([]string{"2026-01-01", "2026-01-31", "2026-03-02", "2026-04-11"}); d != 30 {
		t.Fatal(d)
	}
	if d := typicalInterval([]string{"2026-01-01", "2026-01-31"}); d != 0 {
		t.Fatal("紀錄太少應該是 0", d)
	}
}

func itoa(v int64) string { return strconv.FormatInt(v, 10) }

// 舊系統本來就沒有地址：照樣存得進去
func TestLegacySaveWithoutAddress(t *testing.T) {
	_, c := setup(t)
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	boss := c.as(res["token"].(string))
	page, _ := os.ReadFile("testdata/easy88_order_form.html")
	src := strings.Replace(string(page), "中壢區測試路1號※複合桶，範例街2號(送一般)", "", 1)
	src = strings.Replace(src, ">86000<", ">87981 87981<", 1)
	m := boss.ok("POST", "/api/legacy/upload", map[string]any{"html": src})
	if m["legacy_id"] != "87981" {
		t.Fatal("編號應該只留一個", m["legacy_id"])
	}
	save := boss.ok("POST", "/api/legacy/captures/"+itoa(id(m))+"/save", map[string]any{"customer_id": 0, "customer": map[string]any{"name": "春進-三G半夜", "address": ""}})
	cust := boss.ok("GET", "/api/customers/"+itoa(n(save["customer_id"])), nil)
	if cust["address"] != "（舊系統沒有地址）" {
		t.Fatal("沒地址應該先放提示字", cust["address"])
	}
	if cust["code"] != "87981" {
		t.Fatal("客戶編號應該直接用舊編號", cust["code"])
	}
}

// 之前抓過但沒存到電話的客人：開程式時從存下來的網頁補電話
func TestFillLegacyPhones(t *testing.T) {
	a, c := setup(t)
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	boss := c.as(res["token"].(string))
	page, _ := os.ReadFile("testdata/easy88_order_form.html")
	m := boss.ok("POST", "/api/legacy/upload", map[string]any{"html": string(page)})
	save := boss.ok("POST", "/api/legacy/captures/"+itoa(id(m))+"/save", map[string]any{"customer_id": 0, "customer": map[string]any{"name": "沒電話的客人", "phone": "", "phone2": "", "address": "某路1號", "area": "", "tax_id": "", "note": "", "needs_invoice": false}})
	cid := n(save["customer_id"])
	var phone sql.NullString
	a.DB.QueryRow(`SELECT phone FROM customers WHERE id = ?`, cid).Scan(&phone)
	if phone.String != "" {
		t.Fatal("這個測試要從沒電話開始", phone.String)
	}
	fillLegacyPhones(a.DB)
	a.DB.QueryRow(`SELECT phone FROM customers WHERE id = ?`, cid).Scan(&phone)
	if phone.String != "0912000111" {
		t.Fatal("應該補上電話", phone.String)
	}
}

// F12 順便抓回來的電話清單：主要電話以外的存到「其他電話」，空白和重複的略過，再抓一次不會多存
func TestLegacyPhoneList(t *testing.T) {
	a, c := setup(t)
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	boss := c.as(res["token"].(string))
	page, _ := os.ReadFile("testdata/easy88_order_form.html")
	list := `[{"id":"1","tel":"0976000111","remark":"老闆","type":"C"},{"id":"2","tel":"0936000222","remark":"","type":""},{"id":"3","tel":"","remark":"","type":""},{"id":"4","tel":" ","remark":"","type":""},{"id":"5","tel":"0912000111","remark":"","type":""}]`
	src := string(page) + `<div id="gas-phones">` + url.PathEscape(list) + `</div>`
	for round := 0; round < 2; round++ {
		m := boss.ok("POST", "/api/legacy/upload", map[string]any{"html": src})
		got := boss.ok("GET", "/api/legacy/captures/"+itoa(id(m)), nil)
		pg := got["page"].(map[string]any)
		if ph := pg["phones"].([]any); len(ph) != 3 {
			t.Fatal("應該讀到 3 支電話", ph)
		}
		target := int64(0)
		if ms := got["matches"].([]any); len(ms) > 0 {
			target = n(ms[0].(map[string]any)["id"])
		}
		save := boss.ok("POST", "/api/legacy/captures/"+itoa(id(m))+"/save", map[string]any{"customer_id": target,
			"customer": map[string]any{"name": pg["name"], "phone": pg["phone"], "address": pg["address"]}})
		want := int64(2)
		if round == 1 {
			want = 0
		}
		if n(save["phones_added"]) != want {
			t.Fatal("新增的其他電話數不對", round, save["phones_added"])
		}
	}
	var cnt int
	var note sql.NullString
	a.DB.QueryRow(`SELECT COUNT(*) FROM customer_phones`).Scan(&cnt)
	a.DB.QueryRow(`SELECT note FROM customer_phones WHERE phone = '0976000111'`).Scan(&note)
	if cnt != 2 || note.String != "行動 老闆" {
		t.Fatal("其他電話應該 2 支、備註帶過來", cnt, note.String)
	}
}
