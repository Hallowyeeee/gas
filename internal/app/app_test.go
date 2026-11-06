package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type client struct {
	t     *testing.T
	srv   *httptest.Server
	token string
}

func (c *client) do(method, path string, body any) (int, map[string]any, []any) {
	c.t.Helper()
	var buf bytes.Buffer
	if body != nil {
		json.NewEncoder(&buf).Encode(body)
	}
	req, _ := http.NewRequest(method, c.srv.URL+path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		c.t.Fatal(err)
	}
	defer resp.Body.Close()
	var raw any
	json.NewDecoder(resp.Body).Decode(&raw)
	m, _ := raw.(map[string]any)
	l, _ := raw.([]any)
	return resp.StatusCode, m, l
}

func (c *client) ok(method, path string, body any) map[string]any {
	c.t.Helper()
	code, m, _ := c.do(method, path, body)
	if code != 200 {
		c.t.Fatalf("%s %s → %d %v", method, path, code, m)
	}
	return m
}

func (c *client) list(path string) []any {
	c.t.Helper()
	code, m, l := c.do("GET", path, nil)
	if code != 200 {
		c.t.Fatalf("GET %s → %d %v", path, code, m)
	}
	return l
}

func (c *client) as(token string) *client { return &client{c.t, c.srv, token} }

func id(m map[string]any) int64 { return int64(m["id"].(float64)) }

func n(v any) int64 { return int64(v.(float64)) }

func setup(t *testing.T) (*App, *client) {
	db, err := OpenDB(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	a := &App{DB: db, Backup: &Backup{DB: db, DefaultDir: filepath.Join(dir, "backups")}}
	srv := httptest.NewServer(a.Handler())
	t.Cleanup(func() { srv.Close(); db.Close() })
	return a, &client{t: t, srv: srv}
}

func TestFullFlow(t *testing.T) {
	_, c := setup(t)

	// 沒登入不能用
	if code, _, _ := c.do("GET", "/api/customers", nil); code != 401 {
		t.Fatal("沒登入應該被擋", code)
	}
	// 第一次設定老闆帳號（測試伺服器是本機連線）
	st := c.ok("GET", "/api/setup", nil)
	if st["needed"] != true {
		t.Fatal("應該要設定")
	}
	res := c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	boss := c.as(res["token"].(string))
	if code, _, _ := c.do("POST", "/api/setup", map[string]any{"name": "x", "login_name": "x", "password": "secret123"}); code != 400 {
		t.Fatal("不能設定第二次", code)
	}
	// 經過 Cloudflare 的請求不能做第一次設定
	if isLocal(&http.Request{RemoteAddr: "127.0.0.1:1", Header: http.Header{"Cf-Connecting-Ip": {"1.2.3.4"}}}) {
		t.Fatal("通道來的不算本機")
	}

	// 密碼錯誤
	if code, _, _ := c.do("POST", "/api/login", map[string]any{"login_name": "boss", "password": "wrong"}); code != 401 {
		t.Fatal("密碼錯誤應該 401")
	}

	// 建師傅
	ming := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"}))
	chiang := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿強", "role": "driver", "login_name": "chiang", "password": "pass1234"}))
	mingC := c.as(c.ok("POST", "/api/login", map[string]any{"login_name": "ming", "password": "pass1234"})["token"].(string))
	chiangC := c.as(c.ok("POST", "/api/login", map[string]any{"login_name": "chiang", "password": "pass1234"})["token"].(string))

	// 師傅不能看客戶資料
	if code, _, _ := mingC.do("GET", "/api/customers", nil); code != 403 {
		t.Fatal("師傅不能看客戶", code)
	}

	// 商品
	empty := id(boss.ok("POST", "/api/products", map[string]any{"sku": "EMPTY20", "name": "20kg 空瓶", "category": "empty_cylinder"}))
	gas := id(boss.ok("POST", "/api/products", map[string]any{"sku": "GAS20", "name": "20kg 瓦斯", "category": "gas", "sale_price": 900, "empty_product_id": empty}))
	hose := id(boss.ok("POST", "/api/products", map[string]any{"sku": "HOSE", "name": "瓦斯管", "category": "consumable", "sale_price": 450}))

	// 進貨 50 瓶、還 30 個空瓶；盤點空瓶 40
	sup := id(boss.ok("POST", "/api/suppliers", map[string]any{"name": "分裝場"}))
	boss.ok("POST", "/api/stock/adjust", map[string]any{"product_id": empty, "qty_change": 40, "note": "期初"})
	boss.ok("POST", "/api/purchases", map[string]any{"supplier_id": sup, "items": []any{
		map[string]any{"product_id": gas, "qty": 50, "unit_cost": 700, "empties_returned": 30},
		map[string]any{"product_id": hose, "qty": 10, "unit_cost": 200},
	}})
	boss.ok("POST", "/api/supplier-payments", map[string]any{"supplier_id": sup, "amount": 30000})

	// 客戶
	wang := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "phone": "0912000001", "address": "中正路 1 號", "floor": "5F", "has_elevator": false}))
	chen := id(boss.ok("POST", "/api/customers", map[string]any{"name": "陳記餐廳", "address": "中山路 2 號", "billing_type": "monthly", "needs_invoice": true, "tax_id": "12345678"}))
	if code, m, _ := boss.do("POST", "/api/customers", map[string]any{"name": "錯統編", "address": "x", "tax_id": "123"}); code != 400 {
		t.Fatal("統編錯誤要擋", code, m)
	}
	boss.ok("PUT", fmt.Sprintf("/api/customers/%d/prices", chen), map[string]any{"product_id": gas, "price": 850})

	// 搜尋客戶
	if l := boss.list("/api/customers?q=0912000001"); len(l) != 1 {
		t.Fatal("用電話搜尋客戶", l)
	}

	// 下單：王小明 2 瓶 + 1 條管子，直接派給阿明
	o1 := boss.ok("POST", "/api/orders", map[string]any{"customer_id": wang, "driver_id": ming, "items": []any{
		map[string]any{"product_id": gas, "qty": 2},
		map[string]any{"product_id": hose, "qty": 1},
	}})
	if n(o1["total_amount"]) != 2250 || o1["status"] != "dispatched" {
		t.Fatal("訂單 1", o1)
	}
	// 陳記：月結、特價 850
	o2 := boss.ok("POST", "/api/orders", map[string]any{"customer_id": chen, "items": []any{map[string]any{"product_id": gas, "qty": 2}}})
	if n(o2["total_amount"]) != 1700 || o2["billing_type"] != "monthly" || o2["tax_id"] != "12345678" {
		t.Fatal("訂單 2 特價／月結／統編", o2)
	}
	boss.ok("POST", fmt.Sprintf("/api/orders/%d/dispatch", id(o2)), map[string]any{"driver_id": ming})

	// 阿強看不到阿明的單
	if code, _, _ := chiangC.do("GET", fmt.Sprintf("/api/orders/%d", id(o1)), nil); code != 403 {
		t.Fatal("阿強不能看阿明的單", code)
	}
	if l := chiangC.list("/api/orders"); len(l) != 0 {
		t.Fatal("阿強的單應該是空的", l)
	}
	if l := mingC.list("/api/orders"); len(l) != 2 {
		t.Fatal("阿明應該有 2 張單", l)
	}

	// 阿明送達：訂單 1 收 2200（少收 50），回收 2 空瓶，順便押瓶 1 支收 1500
	items := o1["items"].([]any)
	gasItem := items[0].(map[string]any)
	d1 := mingC.ok("POST", fmt.Sprintf("/api/orders/%d/deliver", id(o1)), map[string]any{
		"collected_amount": 2200,
		"empties":          []any{map[string]any{"item_id": id(gasItem), "empties_returned": 2}},
		"deposit":          map[string]any{"product_id": empty, "qty_change": 1, "amount": 1500},
	})
	if d1["status"] != "delivered" || n(d1["paid_amount"]) != 2200 {
		t.Fatal("送達", d1)
	}
	// 不能重複送達
	if code, _, _ := mingC.do("POST", fmt.Sprintf("/api/orders/%d/deliver", id(o1)), map[string]any{"collected_amount": 1}); code != 400 {
		t.Fatal("重複送達要擋", code)
	}
	// 月結單送達不收錢
	mingC.ok("POST", fmt.Sprintf("/api/orders/%d/deliver", id(o2)), map[string]any{"collected_amount": 0})
	// 已送達不能改明細
	if code, _, _ := boss.do("PUT", fmt.Sprintf("/api/orders/%d", id(o1)), map[string]any{"items": []any{map[string]any{"product_id": gas, "qty": 1}}}); code != 400 {
		t.Fatal("已送達不能改明細", code)
	}

	// 阿明交回 3700（2200 貨款 + 1500 押金）
	boss.ok("POST", "/api/handovers", map[string]any{"driver_id": ming, "amount": 3700})

	// 對帳
	daily := boss.list("/api/reports/driver-daily")
	var mingRow map[string]any
	for _, r := range daily {
		if m := r.(map[string]any); m["driver_name"] == "阿明" {
			mingRow = m
		}
	}
	if mingRow == nil || n(mingRow["expected_cash"]) != 2250 || n(mingRow["reported_cash"]) != 2200 ||
		n(mingRow["deposit_cash"]) != 1500 || n(mingRow["handed_cash"]) != 3700 {
		t.Fatal("阿明對帳", mingRow)
	}
	mis := boss.list("/api/reports/mismatch")
	if len(mis) != 1 || n(mis[0].(map[string]any)["diff_reported_vs_expected"]) != -50 {
		t.Fatal("對不上清單應該有阿明少 50", mis)
	}
	unpaid := boss.list("/api/reports/unpaid-orders")
	if len(unpaid) != 1 || n(unpaid[0].(map[string]any)["unpaid_amount"]) != 50 {
		t.Fatal("未收齊的單", unpaid)
	}
	monthly := boss.list("/api/reports/monthly")
	if len(monthly) != 1 || n(monthly[0].(map[string]any)["amount"]) != 1700 {
		t.Fatal("月結", monthly)
	}

	// 庫存：50 - 4 = 46 瓶；空瓶 40 - 30 + 2 = 12；管子 9
	stock := map[string]int64{}
	for _, r := range boss.list("/api/stock") {
		m := r.(map[string]any)
		stock[m["sku"].(string)] = n(m["qty"])
	}
	if stock["GAS20"] != 46 || stock["EMPTY20"] != 12 || stock["HOSE"] != 9 {
		t.Fatal("庫存", stock)
	}
	// 供應商應付 35000 + 2000 - 30000
	if sb := boss.list("/api/reports/supplier-balance"); n(sb[0].(map[string]any)["balance"]) != 7000 {
		t.Fatal("供應商應付", sb)
	}
	// 押金
	if dt := boss.ok("GET", "/api/reports/deposit-total", nil); n(dt["total_deposit"]) != 1500 {
		t.Fatal("押金總額", dt)
	}

	// 月結客戶付款
	boss.ok("POST", "/api/payments", map[string]any{"customer_id": chen, "amount": 1700, "method": "transfer"})
	cust := boss.ok("GET", fmt.Sprintf("/api/customers/%d", chen), nil)
	if n(cust["balance"]) != 0 {
		t.Fatal("陳記付清", cust)
	}

	// 取消、改回未送達
	o3 := boss.ok("POST", "/api/orders", map[string]any{"customer_id": wang, "items": []any{map[string]any{"product_id": gas, "qty": 1}}})
	boss.ok("POST", fmt.Sprintf("/api/orders/%d/cancel", id(o3)), nil)
	boss.ok("POST", fmt.Sprintf("/api/orders/%d/undeliver", id(o2)), nil)
	stock = map[string]int64{}
	for _, r := range boss.list("/api/stock") {
		m := r.(map[string]any)
		stock[m["sku"].(string)] = n(m["qty"])
	}
	if stock["GAS20"] != 48 {
		t.Fatal("改回未送達後庫存", stock)
	}
	// 只有老闆能看金額異動紀錄、改回未送達
	if audit := boss.list("/api/reports/audit"); len(audit) == 0 {
		t.Fatal("應該有異動紀錄")
	}

	// 單號連號
	o4 := boss.ok("POST", "/api/orders", map[string]any{"customer_id": wang, "items": []any{map[string]any{"product_id": gas, "qty": 1}}})
	if o4["order_no"].(string)[9:] != "004" {
		t.Fatal("單號", o4["order_no"])
	}

	// 停用師傅後登入失效
	boss.ok("PUT", fmt.Sprintf("/api/users/%d", chiang), map[string]any{"active": false})
	if code, _, _ := chiangC.do("GET", "/api/orders", nil); code != 401 {
		t.Fatal("停用後應該登出", code)
	}
}

func TestBackup(t *testing.T) {
	a, _ := setup(t)
	p, err := a.Backup.Now()
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Size() == 0 {
		t.Fatal("備份檔不存在", err)
	}
	db2, err := OpenDB(p)
	if err != nil {
		t.Fatal("備份檔打不開", err)
	}
	db2.Close()
}

func TestNonJSONPostRejected(t *testing.T) {
	_, c := setup(t)
	resp, err := http.Post(c.srv.URL+"/api/login", "application/x-www-form-urlencoded", bytes.NewBufferString("a=b"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 400 {
		t.Fatal("非 JSON 要擋（防跨站）", resp.StatusCode)
	}
}

func TestFindTunnelURL(t *testing.T) {
	if u := findTunnelURL(`ERR failed to request quick Tunnel: Post "https://api.trycloudflare.com/tunnel": dial tcp`); u != "" {
		t.Fatal("錯誤訊息不能當網址", u)
	}
	if u := findTunnelURL(`INF |  https://happy-cat-blue-moon.trycloudflare.com  |`); u != "https://happy-cat-blue-moon.trycloudflare.com" {
		t.Fatal("抓不到網址", u)
	}
}

func TestDispatchEventReachesDriver(t *testing.T) {
	_, c := setup(t)
	boss := c.as(c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})["token"].(string))
	ming := id(boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"}))
	tok := c.ok("POST", "/api/login", map[string]any{"login_name": "ming", "password": "pass1234"})["token"].(string)
	gas := id(boss.ok("POST", "/api/products", map[string]any{"name": "20kg", "category": "gas", "sale_price": 900}))
	cust := id(boss.ok("POST", "/api/customers", map[string]any{"name": "王小明", "address": "中正路 1 號"}))

	req, _ := http.NewRequest("GET", c.srv.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := make(chan string, 10)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				got <- string(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()
	<-got // retry 行
	boss.ok("POST", "/api/orders", map[string]any{"customer_id": cust, "driver_id": ming, "items": []any{map[string]any{"product_id": gas, "qty": 1}}})
	all := ""
	timeout := time.After(3 * time.Second)
	for !strings.Contains(all, `"dispatch"`) {
		select {
		case s := <-got:
			all += s
		case <-timeout:
			t.Fatal("師傅沒收到派單通知:", all)
		}
	}
	if !strings.Contains(all, "王小明") {
		t.Fatal("通知內容要有客戶名", all)
	}
}

func TestUpdateCheck(t *testing.T) {
	if !newerVersion("2026.11.06.0815-abc", "2026.11.06-ffc66d5") || newerVersion("2026.11.06.0815-abc", "2026.11.06.0815-abc") || !newerVersion("x", "dev") {
		t.Fatal("版本比較錯誤")
	}
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"published_at":"2026-11-06T08:00:00Z","body":"version: 2099.01.01.0000-new\n\n更新內容：\n- 加了更新按鈕","assets":[{"name":"gas.exe","browser_download_url":"https://example.invalid/gas.exe"}]}`)
	}))
	defer fake.Close()
	old := releaseAPI
	releaseAPI = fake.URL
	defer func() { releaseAPI = old }()
	_, c := setup(t)
	boss := c.as(c.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})["token"].(string))
	info := boss.ok("POST", "/api/update/check", nil)
	if info["available"] != true || info["latest"] != "2099.01.01.0000-new" || !strings.Contains(info["notes"].(string), "更新按鈕") {
		t.Fatal("檢查更新", info)
	}
	// 師傅不能更新
	ming := boss.ok("POST", "/api/users", map[string]any{"name": "阿明", "role": "driver", "login_name": "ming", "password": "pass1234"})
	_ = ming
	mc := c.as(c.ok("POST", "/api/login", map[string]any{"login_name": "ming", "password": "pass1234"})["token"].(string))
	if code, _, _ := mc.do("POST", "/api/update/apply", nil); code != 403 {
		t.Fatal("師傅不能按更新", code)
	}
}

func TestOpenLINE(t *testing.T) {
	a, c := setup(t)
	var opened string
	a.OpenURL = func(u string) { opened = u }
	if m := c.ok("POST", "/api/local/open-line", map[string]any{}); m["ok"] != true || opened != "line://" {
		t.Fatal("店裡電腦要能打開 LINE", m, opened)
	}
	req, _ := http.NewRequest("POST", c.srv.URL+"/api/local/open-line", strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cf-Connecting-Ip", "1.2.3.4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatal("外面連進來不能叫店裡電腦開程式", resp.StatusCode)
	}
}
