package app

import (
	"context"
	"encoding/json"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client 是「分機」：這台電腦不放資料庫，程式視窗打開 http://localhost:8080，
// 這裡把每個請求轉給主電腦。店裡優先直連主電腦（快），連不到才走外面的網址。
// 主電腦整個斷線時，畫面用程式內建的網頁照樣打開，動作會排隊等連上再送。
type Client struct {
	St      *Station
	File    string // station.json
	Static  fs.FS
	Show    func()
	OpenURL func(string)
	Restart func()

	mu      sync.Mutex
	up      *url.URL
	via     string // "lan" 店內直連；"public" 外面網址；"" 連不到
	lastErr string
	checked time.Time
	kick    chan struct{}

	lanTimeout, publicTimeout time.Duration
	transport                 http.RoundTripper
}

type stationInfo struct {
	ID      string   `json:"id"`
	LAN     []string `json:"lan"`
	Version string   `json:"version"`
}

func (c *Client) init() {
	if c.kick == nil {
		c.kick = make(chan struct{}, 1)
	}
	if c.lanTimeout == 0 {
		c.lanTimeout = 1500 * time.Millisecond
	}
	if c.publicTimeout == 0 {
		c.publicTimeout = 8 * time.Second
	}
	if c.transport == nil {
		c.transport = &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: 4 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout:   10 * time.Second,
			MaxIdleConnsPerHost:   16,
			IdleConnTimeout:       90 * time.Second,
			ResponseHeaderTimeout: 60 * time.Second,
		}
	}
}

// Run 一直檢查要走哪一條路連主電腦。
func (c *Client) Run(ctx context.Context) {
	c.init()
	for {
		c.Pick(ctx)
		wait := 30 * time.Second // 店內直連：30 秒確認一次
		switch c.Via() {
		case "public":
			wait = 15 * time.Second // 走外面：常常試試看能不能改回店內直連
		case "":
			wait = 3 * time.Second // 連不到：很快再試
		}
		select {
		case <-ctx.Done():
			return
		case <-c.kick:
		case <-time.After(wait):
		}
	}
}

// Kick 請馬上重新找主電腦（例如剛剛連線失敗）。
func (c *Client) Kick() {
	c.init()
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

func (c *Client) Via() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.via
}

func (c *Client) current() *url.URL {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.up
}

func (c *Client) fetchInfo(ctx context.Context, base string, timeout time.Duration) (*stationInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/api/station", nil)
	if err != nil {
		return nil, err
	}
	resp, err := (&http.Client{Transport: c.transport}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var in stationInfo
	if resp.StatusCode != 200 || json.NewDecoder(resp.Body).Decode(&in) != nil || in.ID == "" {
		return nil, errNotGas
	}
	return &in, nil
}

var errNotGas = &apiError{http.StatusBadGateway, "這個位址不是瓦斯行主電腦"}

// lanCandidates 店內可以試的位址：手動填的優先，再來是主電腦告訴我們的。
func (c *Client) lanCandidates() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	if m := c.St.ManualIP; m != "" {
		if !strings.Contains(m, "://") {
			m = "http://" + m
		}
		if u, err := url.Parse(m); err == nil && u.Port() == "" {
			m = strings.TrimRight(m, "/") + ":8080"
		}
		out = append(out, m)
	}
	return append(out, c.St.LAN...)
}

// tryLAN 依序試店內位址，第一個回應而且身分碼對的就用它。
func (c *Client) tryLAN(ctx context.Context, cands []string, wantID string) (string, *stationInfo) {
	type res struct {
		i    int
		info *stationInfo
	}
	ch := make(chan res, len(cands))
	for i, base := range cands {
		go func(i int, base string) {
			info, err := c.fetchInfo(ctx, base, c.lanTimeout)
			if err != nil || (wantID != "" && info.ID != wantID) {
				info = nil
			}
			ch <- res{i, info}
		}(i, base)
	}
	best, bestInfo := -1, (*stationInfo)(nil)
	for range cands {
		r := <-ch
		if r.info != nil && (best < 0 || r.i < best) {
			best, bestInfo = r.i, r.info
		}
	}
	if best < 0 {
		return "", nil
	}
	return cands[best], bestInfo
}

// Pick 找一條連得到主電腦的路。
func (c *Client) Pick(ctx context.Context) {
	c.init()
	c.mu.Lock()
	wantID, server := c.St.ServerID, c.St.Server
	c.mu.Unlock()

	if base, info := c.tryLAN(ctx, c.lanCandidates(), wantID); base != "" {
		c.learn(info, base)
		c.set(base, "lan", "")
		return
	}
	info, err := c.fetchInfo(ctx, server, c.publicTimeout)
	if err != nil {
		c.set("", "", "連不到主電腦："+shortErr(err))
		return
	}
	if wantID != "" && info.ID != wantID {
		log.Println("主電腦身分碼變了，改用新的：", info.ID)
	}
	c.learn(info, "")
	// 主電腦剛告訴我們新的店內位址，再試一次直連
	if base, info2 := c.tryLAN(ctx, info.LAN, info.ID); base != "" {
		c.learn(info2, base)
		c.set(base, "lan", "")
		return
	}
	c.set(server, "public", "")
}

// learn 記下主電腦的身分碼和店內位址，下次開機直接用。
func (c *Client) learn(info *stationInfo, _ string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	changed := c.St.ServerID != info.ID
	c.St.ServerID = info.ID
	if len(info.LAN) > 0 && strings.Join(info.LAN, ",") != strings.Join(c.St.LAN, ",") {
		c.St.LAN = append([]string(nil), info.LAN...)
		changed = true
	}
	if changed && c.File != "" {
		c.St.Save(c.File)
	}
}

func (c *Client) set(base, via, errMsg string) {
	var u *url.URL
	if base != "" {
		u, _ = url.Parse(strings.TrimRight(base, "/"))
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.via != via || (c.up == nil) != (u == nil) || (u != nil && c.up.String() != u.String()) {
		switch via {
		case "lan":
			log.Println("分機：店內直連主電腦", base)
		case "public":
			log.Println("分機：走外面網址連主電腦", base)
		default:
			log.Println("分機：", errMsg)
		}
	}
	c.up, c.via, c.lastErr, c.checked = u, via, errMsg, time.Now()
}

func (c *Client) status() map[string]any {
	c.mu.Lock()
	defer c.mu.Unlock()
	v := c.St.view()
	addr := ""
	if c.up != nil {
		addr = c.up.Host
	}
	v["via"], v["address"], v["error"] = c.via, addr, c.lastErr
	return v
}

// Handler 程式視窗看到的整個網站。
func (c *Client) Handler() http.Handler {
	c.init()
	m := http.NewServeMux()
	m.HandleFunc("GET /api/local/station", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, c.status()) })
	m.HandleFunc("PUT /api/local/station", c.putStation)
	m.HandleFunc("POST /api/local/show", func(w http.ResponseWriter, r *http.Request) {
		if c.Show != nil {
			go c.Show()
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})
	m.HandleFunc("POST /api/local/open-line", func(w http.ResponseWriter, r *http.Request) {
		ok := c.OpenURL != nil
		if ok {
			c.OpenURL("line://")
		}
		writeJSON(w, 200, map[string]bool{"ok": ok})
	})
	m.HandleFunc("/api/local/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "這台是分機，這個功能要在主電腦上用"})
	})
	m.HandleFunc("/", c.forward)
	return securityHeaders(onlyLocal(m))
}

// onlyLocal 分機的程式只給自己這台電腦用。
func onlyLocal(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, _ := net.SplitHostPort(r.RemoteAddr)
		if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
			writeErr(w, forbidden())
			return
		}
		h.ServeHTTP(w, r)
	})
}

func (c *Client) putStation(w http.ResponseWriter, r *http.Request) {
	var in stationInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, err)
		return
	}
	c.mu.Lock()
	changed, err := c.St.apply(in)
	if err == nil && c.File != "" {
		err = c.St.Save(c.File)
	}
	c.mu.Unlock()
	if err != nil {
		writeErr(w, err)
		return
	}
	if changed && c.Restart != nil {
		go c.Restart()
	} else if in.Server != nil || in.ManualIP != nil {
		c.Pick(r.Context())
	}
	writeJSON(w, 200, map[string]any{"ok": true, "restart": changed, "status": c.status()})
}

func (c *Client) forward(w http.ResponseWriter, r *http.Request) {
	isAPI := strings.HasPrefix(r.URL.Path, "/api/")
	up := c.current()
	if up == nil {
		c.Kick()
		c.offline(w, r, isAPI)
		return
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(up)
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Del("Referer")
		},
		Transport:     c.transport,
		FlushInterval: -1, // 即時更新（來電、派單）馬上送到
		ModifyResponse: func(resp *http.Response) error {
			// 外面網址回來的登入 cookie 有 Secure，程式視窗是 http://localhost，拿掉才存得住
			if sc := resp.Header.Values("Set-Cookie"); len(sc) > 0 {
				resp.Header.Del("Set-Cookie")
				for _, v := range sc {
					resp.Header.Add("Set-Cookie", strings.Replace(strings.Replace(v, "; Secure", "", 1), "; secure", "", 1))
				}
			}
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			if r.Context().Err() != nil {
				return // 畫面自己關掉連線（例如換頁），不是主電腦的問題
			}
			log.Println("分機轉送失敗：", shortErr(err))
			c.Kick()
			c.offline(w, r, isAPI)
		},
	}
	rp.ServeHTTP(w, r)
}

// offline 連不到主電腦：網頁用程式內建的；資料請求直接斷線，畫面就會進入「離線排隊」。
func (c *Client) offline(w http.ResponseWriter, r *http.Request, isAPI bool) {
	if isAPI || c.Static == nil || r.Method != http.MethodGet {
		panic(http.ErrAbortHandler)
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if _, err := fs.Stat(c.Static, p); err != nil {
		r.URL.Path = "/"
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.FileServer(http.FS(c.Static)).ServeHTTP(w, r)
}
