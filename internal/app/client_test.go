package app

import (
	"context"
	"fmt"
	"io"
	"net"
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
	cl := &Client{St: st, File: filepath.Join(t.TempDir(), "station.json"), lanTimeout: time.Second, publicTimeout: time.Second, discoverTargets: []string{}}
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
	cl := &Client{St: st, lanTimeout: time.Second, publicTimeout: time.Second, discoverTargets: []string{}}
	cl.Pick(context.Background())
	if cl.Via() != "" {
		t.Fatalf("身分碼不對不該連，結果 %q", cl.Via())
	}
}

// 主電腦連不到：網頁用內建的打開，API 斷線（畫面進入離線排隊）。
func TestClientOffline(t *testing.T) {
	st := &Station{Mode: "client", Server: "http://127.0.0.1:1"}
	cl := &Client{St: st, Static: fstest.MapFS{"index.html": {Data: []byte("<h1>瓦斯行</h1>")}}, lanTimeout: 300 * time.Millisecond, publicTimeout: 300 * time.Millisecond, discoverTargets: []string{}}
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

// 主電腦還是舊版（沒有 /api/station）：分機照樣走外面網址。
func TestClientOldMain(t *testing.T) {
	_, c := setup(t)
	old := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/station" {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "沒有這個功能"})
			return
		}
		c.srv.Config.Handler.ServeHTTP(w, r)
	}))
	defer old.Close()
	cl := &Client{St: &Station{Mode: "client", Server: old.URL}, lanTimeout: time.Second, publicTimeout: time.Second, discoverTargets: []string{}}
	cl.Pick(context.Background())
	if cl.Via() != "public" {
		t.Fatalf("舊版主電腦也要能連，結果 %q %v", cl.Via(), cl.status())
	}
}

// 店裡新電腦：什麼都沒填，廣播找到主電腦，記下身分碼和主電腦的外面網址。
func TestClientDiscoversMain(t *testing.T) {
	a, c := setup(t)
	port := c.srv.Listener.Addr().(*net.TCPAddr).Port
	a.Port = port
	a.Tunnel = &Tunnel{url: "https://shop1.example.com"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.ServeDiscovery(ctx)
	time.Sleep(100 * time.Millisecond)

	st := &Station{Mode: "client"}
	cl := &Client{St: st, File: filepath.Join(t.TempDir(), "station.json"), lanTimeout: time.Second, publicTimeout: time.Second,
		discoverTargets: []string{fmt.Sprintf("127.0.0.1:%d", discoveryPort(port))}}
	cl.Pick(context.Background())
	if cl.Via() != "lan" {
		t.Fatalf("應該廣播找到主電腦，結果 %q %v", cl.Via(), cl.status())
	}
	saved := LoadStation(cl.File)
	if saved.ServerID != a.serverID() || saved.Server != "https://shop1.example.com" {
		t.Fatalf("沒記下主電腦：%+v", saved)
	}
}

// 連線碼：主電腦產生，分機看得懂；也接受直接打網址。
func TestConnCode(t *testing.T) {
	code := makeCode("https://shop1.example.com", "abc123")
	u, id, err := parseCode(code)
	if err != nil || u != "https://shop1.example.com" || id != "abc123" {
		t.Fatalf("連線碼解不回來：%q %q %v", u, id, err)
	}
	if u, _, _ := parseCode("shop2.example.com/"); u != "https://shop2.example.com" {
		t.Fatalf("網址：%q", u)
	}
	if _, _, err := parseCode("GAS-亂打"); err == nil {
		t.Fatal("亂打的連線碼應該報錯")
	}
	st := &Station{Mode: "client"}
	in := code
	if _, err := st.apply(stationInput{Server: &in}); err != nil || st.Server != "https://shop1.example.com" || st.ServerID != "abc123" {
		t.Fatalf("套用連線碼：%+v %v", st, err)
	}
}

// 全新安裝（設定精靈）選分機：不用重開，馬上開始找主電腦。
func TestSetupChooseClient(t *testing.T) {
	st := &Station{Mode: "setup"}
	restarted := false
	cl := &Client{St: st, File: filepath.Join(t.TempDir(), "station.json"), Restart: func() { restarted = true },
		lanTimeout: 200 * time.Millisecond, publicTimeout: 200 * time.Millisecond, discoverTargets: []string{}}
	front := httptest.NewServer(cl.Handler())
	defer front.Close()
	fc := &client{t: t, srv: front}
	m := fc.ok("PUT", "/api/local/station", map[string]any{"mode": "client"})
	if m["restart"] != false || restarted {
		t.Fatalf("選分機不該重開：%v", m)
	}
	if LoadStation(cl.File).Mode != "client" {
		t.Fatal("沒存成分機")
	}
}

// 外面網址回 502（Cloudflare 通、主電腦程式沒開）：寫清楚原因。
func TestClientMainDown(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(502) }))
	defer down.Close()
	cl := &Client{St: &Station{Mode: "client", Server: down.URL}, lanTimeout: time.Second, publicTimeout: time.Second, discoverTargets: []string{}}
	cl.Pick(context.Background())
	if e, _ := cl.status()["error"].(string); !strings.Contains(e, "沒有回應") {
		t.Fatalf("原因不清楚：%q", e)
	}
}
