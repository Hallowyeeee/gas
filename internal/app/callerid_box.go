package app

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------
// 來電顯示盒：從 COM 埠讀號碼；也有「偵測」模式，把盒子送出的原始資料秀出來
// ---------------------------------------------------------------------

// 可以換掉（測試用）
var (
	serialPorts = listSerialPorts
	serialOpen  = openSerial
	usbDevices  = listUSBDevices
)

type boxChunk struct {
	At    string `json:"at"`
	Port  string `json:"port"`
	Text  string `json:"text"`  // 看得懂的字（看不懂的變成 ·）
	Hex   string `json:"hex"`   // 原始位元組
	Phone string `json:"phone"` // 從裡面抓到的號碼
}

type detectPort struct {
	Port  string `json:"port"`
	Error string `json:"error,omitempty"`
	Bytes int    `json:"bytes"`
}

// CallerBox 管理來電顯示盒的連線。
type CallerBox struct {
	app *App

	ctl      sync.Mutex // 一次只做一個 restart / startDetect
	mu       sync.Mutex
	status   string
	recent   []boxChunk // 最近收到的資料（偵測與平常都會記）
	run      *boxRun
	lastCall map[string]time.Time

	detecting   bool
	detectUntil time.Time
	detectBaud  int
	detectPorts []*detectPort
	devices     []string
}

func (a *App) box() *CallerBox {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.callerBox == nil {
		a.callerBox = &CallerBox{app: a, status: "還沒設定盒子", lastCall: map[string]time.Time{}}
	}
	return a.callerBox
}

type boxRun struct {
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func (b *CallerBox) stopRun() {
	b.mu.Lock()
	r := b.run
	b.run = nil
	b.mu.Unlock()
	if r != nil {
		r.cancel()
		r.wg.Wait()
	}
}

// StartCallerBox 程式啟動時呼叫：有設定 COM 埠就開始聽。
func (a *App) StartCallerBox() { a.box().restart() }

func (b *CallerBox) setStatus(s string) {
	b.mu.Lock()
	b.status = s
	b.mu.Unlock()
}

func (b *CallerBox) settings() (port string, baud int) {
	b.app.DB.QueryRow(`SELECT value FROM settings WHERE key = 'callerid_port'`).Scan(&port)
	var bs string
	b.app.DB.QueryRow(`SELECT value FROM settings WHERE key = 'callerid_baud'`).Scan(&bs)
	baud, _ = strconv.Atoi(bs)
	if baud == 0 {
		baud = 9600
	}
	return
}

// restart 停掉目前的讀取，依設定重新開始。
func (b *CallerBox) restart() {
	b.ctl.Lock()
	defer b.ctl.Unlock()
	b.stopRun()
	port, baud := b.settings()
	if port == "" {
		b.setStatus("還沒設定盒子")
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &boxRun{cancel: cancel}
	b.mu.Lock()
	b.run = r
	b.mu.Unlock()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		b.listen(ctx, port, baud, false)
	}()
}

// listen 一直讀某個 COM 埠；斷了就每 5 秒重試。detect = true 時讀到期限就結束、不跳來電。
func (b *CallerBox) listen(ctx context.Context, port string, baud int, detect bool) {
	for ctx.Err() == nil {
		p, err := serialOpen(port, baud)
		if err != nil {
			if detect {
				b.detectErr(port, err)
				return
			}
			b.setStatus(fmt.Sprintf("連不上 %s：%v（5 秒後重試）", port, err))
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
			continue
		}
		if !detect {
			b.setStatus(fmt.Sprintf("已連上 %s（%d），等電話中", port, baud))
			log.Println("來電顯示盒已連上", port, baud)
		}
		b.readLoop(ctx, p, port, detect)
		p.Close()
	}
}

func (b *CallerBox) readLoop(ctx context.Context, p io.Reader, port string, detect bool) {
	done := make(chan struct{})
	defer close(done)
	go func() { // ctx 結束時關掉，讓 Read 回來
		select {
		case <-ctx.Done():
			if c, ok := p.(io.Closer); ok {
				c.Close()
			}
		case <-done:
		}
	}()
	buf := make([]byte, 512)
	var pending []byte
	var lastData time.Time
	for ctx.Err() == nil {
		n, err := p.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			lastData = time.Now()
			if detect {
				b.mu.Lock()
				for _, dp := range b.detectPorts {
					if dp.Port == port {
						dp.Bytes += n
					}
				}
				b.mu.Unlock()
			}
		}
		// 一段資料：遇到換行，或停了 300 毫秒，就當作一筆
		if len(pending) > 0 && (n == 0 || time.Since(lastData) > 300*time.Millisecond || len(pending) > 256) {
			b.handleChunk(port, pending, detect)
			pending = nil
		}
		if err != nil {
			if err == io.EOF {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			if ctx.Err() == nil && !detect {
				b.setStatus(fmt.Sprintf("%s 讀取中斷：%v", port, err))
			}
			return
		}
	}
}

func (b *CallerBox) handleChunk(port string, data []byte, detect bool) {
	c := boxChunk{At: time.Now().Format("15:04:05"), Port: port, Text: printable(data), Hex: spacedHex(data), Phone: extractPhone(data)}
	b.mu.Lock()
	b.recent = append([]boxChunk{c}, b.recent...)
	if len(b.recent) > 50 {
		b.recent = b.recent[:50]
	}
	dup := c.Phone != "" && time.Since(b.lastCall[c.Phone]) < 15*time.Second // 盒子常常同一通送兩次
	if c.Phone != "" && !detect {
		b.lastCall[c.Phone] = time.Now()
	}
	b.mu.Unlock()
	log.Printf("來電顯示盒 %s 收到：%s [%s]", port, c.Text, c.Hex)
	if detect || c.Phone == "" || dup {
		return
	}
	if _, err := b.app.IncomingCall(context.Background(), c.Phone, "box"); err != nil {
		log.Println("來電處理失敗:", err)
	}
}

func (b *CallerBox) detectErr(port string, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, dp := range b.detectPorts {
		if dp.Port == port {
			dp.Error = err.Error()
		}
	}
}

// startDetect 暫停平常的讀取，同時打開所有 COM 埠聽 90 秒。
func (b *CallerBox) startDetect(baud int) {
	b.ctl.Lock()
	defer b.ctl.Unlock()
	b.stopRun()

	ports := serialPorts()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	r := &boxRun{cancel: cancel}
	b.mu.Lock()
	b.run = r
	b.detecting = true
	b.detectBaud = baud
	b.detectUntil = time.Now().Add(90 * time.Second)
	b.detectPorts = nil
	b.recent = nil
	for _, p := range ports {
		b.detectPorts = append(b.detectPorts, &detectPort{Port: p})
	}
	b.mu.Unlock()
	go func() { // 裝置清單要幾秒，背景抓
		d := usbDevices()
		b.mu.Lock()
		b.devices = d
		b.mu.Unlock()
	}()
	for _, p := range ports {
		r.wg.Add(1)
		go func(p string) {
			defer r.wg.Done()
			b.listen(ctx, p, baud, true)
		}(p)
	}
	go func() { // 偵測時間到：回到平常的讀取
		<-ctx.Done()
		r.wg.Wait()
		b.mu.Lock()
		still := b.detecting && b.run == r
		b.detecting = false
		b.mu.Unlock()
		if still {
			b.restart()
		}
	}()
}

func (b *CallerBox) snapshot() map[string]any {
	b.mu.Lock()
	defer b.mu.Unlock()
	port, baud := b.settings()
	left := 0
	if b.detecting {
		left = int(time.Until(b.detectUntil).Seconds())
	}
	return map[string]any{
		"status": b.status, "port": port, "baud": baud, "recent": b.recent,
		"detecting": b.detecting, "detect_left": left, "detect_baud": b.detectBaud,
		"detect_ports": b.detectPorts, "devices": b.devices,
	}
}

// ---------------------------------------------------------------------
// 從原始資料抓號碼
// ---------------------------------------------------------------------

var digitRun = regexp.MustCompile(`[0-9][0-9\-]{5,}[0-9]`)

// extractPhone 找出像電話號碼的數字（7～12 碼）。前面黏著 8 碼日期時間（月日時分）的會去掉。
func extractPhone(data []byte) string {
	best := ""
	for _, m := range digitRun.FindAllString(string(data), -1) {
		d := strings.NewReplacer("-", "", " ", "").Replace(m)
		if len(d) > 12 && len(d)-8 >= 7 && d[8] == '0' {
			d = d[8:]
		}
		if len(d) < 7 || len(d) > 12 {
			continue
		}
		// 0 開頭的優先，其次越長越好
		if best == "" || (d[0] == '0' && best[0] != '0') || (d[0] == best[0] && len(d) > len(best)) {
			best = d
		}
	}
	return best
}

func printable(b []byte) string {
	var s strings.Builder
	for _, c := range b {
		if c >= 32 && c < 127 {
			s.WriteByte(c)
		} else {
			s.WriteString("·")
		}
	}
	return s.String()
}

func spacedHex(b []byte) string {
	h := hex.EncodeToString(b)
	var s strings.Builder
	for i := 0; i < len(h); i += 2 {
		if i > 0 {
			s.WriteByte(' ')
		}
		s.WriteString(h[i : i+2])
	}
	return s.String()
}

// ---------------------------------------------------------------------
// API
// ---------------------------------------------------------------------

func (a *App) callerBoxStatus(r *http.Request, u *User) (any, error) {
	s := a.box().snapshot()
	s["ports"] = serialPorts()
	return s, nil
}

func (a *App) callerBoxDetect(r *http.Request, u *User) (any, error) {
	if !isLocal(r) {
		return nil, bad("偵測只能在接盒子的店裡電腦上按")
	}
	var in struct {
		Baud int `json:"baud"`
	}
	readJSON(r, &in)
	if in.Baud == 0 {
		in.Baud = 9600
	}
	a.box().startDetect(in.Baud)
	return a.box().snapshot(), nil
}

func (a *App) callerBoxUse(r *http.Request, u *User) (any, error) {
	var in struct {
		Port string `json:"port"`
		Baud int    `json:"baud"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if in.Baud == 0 {
		in.Baud = 9600
	}
	for k, v := range map[string]string{"callerid_port": in.Port, "callerid_baud": strconv.Itoa(in.Baud)} {
		if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return nil, err
		}
	}
	b := a.box()
	b.mu.Lock()
	b.detecting = false
	b.mu.Unlock()
	go b.restart()
	return map[string]bool{"ok": true}, nil
}
