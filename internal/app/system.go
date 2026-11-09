package app

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ---------------------------------------------------------------------
// Cloudflare 免費通道：手機在外面也能連進來
// 免費版每次重開會換網址，買網域後改成固定網址。
// ---------------------------------------------------------------------

type Tunnel struct {
	Port     int
	Dir      string // 放 cloudflared.exe 的資料夾
	Download bool   // 找不到時自動下載
	// Config 回傳固定網址用的通道金鑰和網址（沒設金鑰就用免費臨時網址）
	Config func() (token, host string)

	mu     sync.Mutex
	url    string
	status string
	once   sync.Once
	reload chan struct{}
}

func (t *Tunnel) reloadCh() chan struct{} {
	t.once.Do(func() { t.reload = make(chan struct{}, 1) })
	return t.reload
}

// Reload 設定改了，重新連線。
func (t *Tunnel) Reload() {
	if t == nil {
		return
	}
	select {
	case t.reloadCh() <- struct{}{}:
	default:
	}
}

func (t *Tunnel) config() (string, string) {
	if t.Config == nil {
		return "", ""
	}
	token, host := t.Config()
	host = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(host), "https://"), "http://"), "/")
	return strings.TrimSpace(token), host
}

// cleanTunnelToken 從貼上的文字找出金鑰：可以貼整段 Cloudflare 指令，或只貼 eyJ 開頭的金鑰。
func cleanTunnelToken(v string) (string, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return "", nil
	}
	for _, f := range strings.Fields(v) {
		f = strings.Trim(f, `"'`)
		if strings.HasPrefix(f, "eyJ") && len(f) > 50 {
			return f, nil
		}
	}
	return "", bad("金鑰格式不對：請貼 Cloudflare 給的那段指令，或 eyJ 開頭的那一長串")
}

var tunnelURL = regexp.MustCompile(`https://[a-z0-9-]+\.trycloudflare\.com`)

func (t *Tunnel) set(url, status string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if url != "-" {
		t.url = url
	}
	t.status = status
}

// Info 回傳目前網址和狀態。
func (t *Tunnel) Info() (string, string) {
	if t == nil {
		return "", "未啟用"
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.url, t.status
}

func (t *Tunnel) exePath() string {
	name := "cloudflared"
	if runtime.GOOS == "windows" {
		name = "cloudflared.exe"
	}
	local := filepath.Join(t.Dir, name)
	if _, err := os.Stat(local); err == nil {
		return local
	}
	if p, err := exec.LookPath("cloudflared"); err == nil {
		return p
	}
	if t.Download && runtime.GOOS == "windows" {
		t.set("-", "第一次使用，正在下載 Cloudflare 通道程式…")
		if err := download("https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-windows-amd64.exe", local); err != nil {
			log.Println("下載 cloudflared 失敗（可手動下載 cloudflared-windows-amd64.exe，改名 cloudflared.exe 放到 gas.exe 旁邊）:", shortErr(err))
			return ""
		}
		return local
	}
	return ""
}

func download(url, dst string) error {
	client := &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			Proxy:               http.ProxyFromEnvironment,
			TLSHandshakeTimeout: 60 * time.Second,
		},
	}
	var lastErr error
	for try := 1; try <= 3; try++ {
		if lastErr = downloadOnce(client, url, dst); lastErr == nil {
			return nil
		}
		log.Printf("下載第 %d 次失敗：%v", try, lastErr)
		time.Sleep(time.Duration(try*5) * time.Second)
	}
	return lastErr
}

func downloadOnce(client *http.Client, url, dst string) error {
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".download"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	return os.Rename(tmp, dst)
}

// findTunnelURL 從 cloudflared 的輸出找出手機網址（api.trycloudflare.com 是錯誤訊息裡的，不算）。
func findTunnelURL(line string) string {
	for _, u := range tunnelURL.FindAllString(line, -1) {
		if u != "https://api.trycloudflare.com" {
			return u
		}
	}
	return ""
}

// Run 在背景一直維持通道，斷了就重開。
func (t *Tunnel) Run(ctx context.Context) {
	for ctx.Err() == nil {
		exe := t.exePath()
		if exe == "" {
			t.set("", "找不到 Cloudflare 通道程式（cloudflared），30 秒後重試；手機暫時只能在店裡 WiFi 使用")
			select {
			case <-ctx.Done():
				return
			case <-time.After(30 * time.Second):
				continue
			}
		}
		token, host := t.config()
		cctx, cancel := context.WithCancel(ctx)
		var reloaded atomic.Bool
		go func() {
			select {
			case <-t.reloadCh():
				reloaded.Store(true)
				cancel()
			case <-cctx.Done():
			}
		}()
		var cmd *exec.Cmd
		if token != "" {
			t.set("", "固定網址連線中…")
			cmd = exec.CommandContext(cctx, exe, "tunnel", "--no-autoupdate", "run")
			cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+token) // 金鑰不放在指令列，比較安全
		} else {
			t.set("", "通道連線中…")
			cmd = exec.CommandContext(cctx, exe, "tunnel", "--no-autoupdate", "--url", fmt.Sprintf("http://localhost:%d", t.Port))
		}
		hideWindow(cmd)
		stderr, err := cmd.StderrPipe()
		if err == nil {
			err = cmd.Start()
		}
		if err != nil {
			t.set("", "通道啟動失敗："+err.Error())
		} else {
			sc := bufio.NewScanner(stderr)
			for sc.Scan() {
				line := sc.Text()
				if token != "" {
					if strings.Contains(line, "Registered tunnel connection") {
						if host == "" {
							t.set("", "已連線，但還沒填網址（到下面「固定網址設定」填）")
						} else {
							t.set("https://"+host, "已連線（固定網址）")
						}
					} else if strings.Contains(line, " ERR ") {
						log.Println("通道:", line)
						if strings.Contains(strings.ToLower(line), "unauthorized") || strings.Contains(strings.ToLower(line), "invalid") {
							t.set("", "Cloudflare 不接受這個金鑰，請到設定重新貼上")
						}
					}
				} else if u := findTunnelURL(line); u != "" {
					t.set(u, "已連線")
					log.Println("手機網址:", u)
				} else if strings.Contains(line, " ERR ") {
					log.Println("通道:", line)
				}
			}
			cmd.Wait()
			if !reloaded.Load() {
				_, st := t.Info()
				if !strings.Contains(st, "金鑰") {
					t.set("", "通道中斷，10 秒後重新連線")
				}
			}
		}
		cancel()
		if reloaded.Load() {
			continue
		}
		select {
		case <-t.reloadCh():
			continue
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
	}
}

// ---------------------------------------------------------------------
// 每日備份：複製一份資料庫到備份資料夾（可設成 Google 雲端硬碟資料夾）
// ---------------------------------------------------------------------

type Backup struct {
	DB         *sql.DB
	DefaultDir string
	Keep       int

	mu       sync.Mutex
	lastTime time.Time
	lastPath string
	lastErr  string
}

func (b *Backup) dir() string {
	var d string
	if err := b.DB.QueryRow(`SELECT value FROM settings WHERE key = 'backup_dir'`).Scan(&d); err == nil && strings.TrimSpace(d) != "" {
		return strings.TrimSpace(d)
	}
	return b.DefaultDir
}

// Now 立刻備份一次。
func (b *Backup) Now() (string, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	dir := b.dir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		b.lastErr = err.Error()
		return "", err
	}
	path := filepath.Join(dir, "gas-"+time.Now().Format("20060102-150405")+".db")
	if _, err := b.DB.Exec(`VACUUM INTO ?`, path); err != nil {
		b.lastErr = err.Error()
		return "", err
	}
	b.lastTime, b.lastPath, b.lastErr = time.Now(), path, ""
	b.prune(dir)
	return path, nil
}

func (b *Backup) prune(dir string) {
	keep := b.Keep
	if keep <= 0 {
		keep = 60
	}
	files, _ := filepath.Glob(filepath.Join(dir, "gas-*.db"))
	sort.Strings(files)
	for len(files) > keep {
		os.Remove(files[0])
		files = files[1:]
	}
}

func (b *Backup) newest() time.Time {
	files, _ := filepath.Glob(filepath.Join(b.dir(), "gas-*.db"))
	var t time.Time
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.ModTime().After(t) {
			t = st.ModTime()
		}
	}
	return t
}

// Run 每小時檢查一次，超過 20 小時沒備份就備份。
func (b *Backup) Run(ctx context.Context) {
	for {
		if time.Since(b.newest()) > 20*time.Hour {
			if p, err := b.Now(); err != nil {
				log.Println("備份失敗:", err)
			} else {
				log.Println("已備份:", p)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Hour):
		}
	}
}

func (b *Backup) Info() map[string]any {
	if b == nil {
		return map[string]any{"enabled": false}
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	last := ""
	if !b.lastTime.IsZero() {
		last = b.lastTime.Format("2006-01-02 15:04:05")
	} else if t := b.newest(); !t.IsZero() {
		last = t.Format("2006-01-02 15:04:05")
	}
	return map[string]any{"enabled": true, "dir": b.dir(), "last_time": last, "last_path": b.lastPath, "last_error": b.lastErr}
}

// ---------------------------------------------------------------------

func lanAddresses(port int) []string {
	out := []string{}
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ip, ok := a.(*net.IPNet); ok && ip.IP.To4() != nil && !ip.IP.IsLoopback() && ip.IP.IsPrivate() {
			out = append(out, fmt.Sprintf("http://%s:%d", ip.IP, port))
		}
	}
	return out
}

func (a *App) systemStatus(r *http.Request, u *User) (any, error) {
	url, status := a.Tunnel.Info()
	port := 0
	if a.Tunnel != nil {
		port = a.Tunnel.Port
	}
	return map[string]any{
		"version":       Version,
		"tunnel_url":    url,
		"tunnel_status": status,
		"lan_urls":      lanAddresses(port),
		"backup":        a.Backup.Info(),
		"update":        a.updateStatus(),
		"local":         isLocal(r),
	}, nil
}

func (a *App) backupNow(r *http.Request, u *User) (any, error) {
	if a.Backup == nil {
		return nil, bad("備份未啟用")
	}
	p, err := a.Backup.Now()
	if err != nil {
		return nil, err
	}
	return map[string]any{"path": p}, nil
}

// shortErr 把很長的下載網址從錯誤訊息拿掉，畫面比較好讀。
func shortErr(err error) string {
	msg := err.Error()
	if i := strings.Index(msg, "\": "); i >= 0 {
		return msg[i+3:]
	}
	return msg
}
