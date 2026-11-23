package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// 分機：透過店內直連登入、記單用的 API 都轉到主電腦；cookie 的 Secure 拿掉。
func TestClientForwardsToMain(t *testing.T) {
	a, c0 := setup(t)
	main := c0.srv
	res := c0.ok("POST", "/api/setup", map[string]any{"name": "老闆", "login_name": "boss", "password": "secret123"})
	c := c0.as(res["token"].(string))

	st := &Station{Mode: "client", Server: "http://127.0.0.1:1", ManualIP: main.URL} // 外面網址故意連不到
	cl := &Client{St: st, File: filepath.Join(t.TempDir(), "station.json"), lanTimeout: time.Second, publicTimeout: time.Second}
	cl.Pick(context.Background())
	if cl.Via() != "lan" {
		t.Fatalf("應該店內直連，結果 %q %v", cl.Via(), cl.status())
	}
	if st.ServerID != a.serverID() {
		t.Fatalf("沒記下主電腦身分碼：%q", st.ServerID)
	}
	if LoadStation(cl.File).ServerID != st.ServerID {
		t.Fatal("身分碼沒存到 station.json")
	}

	front := httptest.NewServer(cl.Handler())
	defer front.Close()
	fc := &client{t, front, c.token}
	if m := fc.ok("GET", "/api/me", nil); m["role"] != "admin" {
		t.Fatalf("透過分機讀不到登入的人：%v", m)
	}
	cust := fc.ok("POST", "/api/customers", map[string]any{"name": "分機客", "phone": "0911222333", "address": "測試路1號"})
	if id(cust) == 0 {
		t.Fatal("分機新增客戶失敗")
	}
	// 分機傳過去的請求，主電腦不能當成本機（不能叫主電腦視窗、不能更新）
	if code, _, _ := fc.do("GET", "/api/local/station", nil); code != 200 {
		t.Fatalf("分機自己的設定應該讀得到，%d", code)
	}
	if code, _, _ := fc.do("POST", "/api/local/call?phone=0911222333", nil); code != 403 {
		t.Fatalf("分機不能模擬來電盒，%d", code)
	}

	// 登入 cookie 的 Secure 要拿掉
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "gas_session", Value: "x", Secure: true, HttpOnly: true})
		w.Write([]byte("{}"))
	}))
	defer up.Close()
	cl.set(up.URL, "public", "")
	resp, err := http.Get(front.URL + "/api/whatever")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if sc := resp.Header.Get("Set-Cookie"); sc == "" || strings.Contains(strings.ToLower(sc), "secure") {
		t.Fatalf("cookie 不對：%q", sc)
	}
}

// 身分碼不對的位址不能用（例如店裡另一台不相干的電腦）。
func TestClientRejectsOtherServer(t *testing.T) {
	_, c := setup(t)
	st := &Station{Mode: "client", Server: "http://127.0.0.1:1", ManualIP: c.srv.URL, ServerID: "別台"}
	cl := &Client{St: st, lanTimeout: time.Second, publicTimeout: time.Second}
	cl.Pick(context.Background())
	if cl.Via() != "" {
		t.Fatalf("身分碼不對不該連，結果 %q", cl.Via())
	}
}

// 主電腦連不到：網頁用內建的打開，API 斷線（畫面進入離線排隊）。
func TestClientOffline(t *testing.T) {
	st := &Station{Mode: "client", Server: "http://127.0.0.1:1"}
	cl := &Client{St: st, Static: fstest.MapFS{"index.html": {Data: []byte("<h1>瓦斯行</h1>")}}, lanTimeout: 300 * time.Millisecond, publicTimeout: 300 * time.Millisecond}
	front := httptest.NewServer(cl.Handler())
	defer front.Close()
	resp, err := http.Get(front.URL + "/#/home")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(b), "瓦斯行") {
		t.Fatalf("離線時應該用內建網頁：%s", b)
	}
	if _, err := http.Get(front.URL + "/api/me"); err == nil {
		t.Fatal("離線時 API 應該斷線，畫面才會改成排隊")
	}
}

// 主電腦：分機查身分碼不用登入；這台電腦的設定只有本機能改，而且會存檔。
func TestStationOnMain(t *testing.T) {
	a, c := setup(t)
	a.Station = &Station{Mode: "main"}
	a.Restart = func() {}
	a.StationFile = filepath.Join(t.TempDir(), "station.json")
	anon := c.as("")
	m := anon.ok("GET", "/api/station", nil)
	if m["id"] == "" || m["id"] != a.serverID() {
		t.Fatalf("身分碼不對：%v", m)
	}
	m = c.ok("GET", "/api/local/station", nil)
	if m["mode"] != "main" || m["popup"] != true {
		t.Fatalf("主電腦設定不對：%v", m)
	}
	c.ok("PUT", "/api/local/station", map[string]any{"popup": false})
	if LoadStation(a.StationFile).PopupOn() {
		t.Fatal("來電跳出關掉沒存到")
	}
	// 透過外面網址來的不能改
	req, _ := http.NewRequest("PUT", c.srv.URL+"/api/local/station", strings.NewReader(`{"popup":true}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cf-Connecting-Ip", "1.2.3.4")
	resp, _ := http.DefaultClient.Do(req)
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Fatalf("外面不能改這台電腦設定，%d", resp.StatusCode)
	}
}
