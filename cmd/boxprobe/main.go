// 來電盒偵測：找出來電顯示盒接在哪個 COM 埠、送出什麼資料，並找原廠軟體的設定。
// 結果會存成文字檔，傳給 Claude 就能寫出正確的抓號碼方式。
package main

import (
	"bufio"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hallowyeeee/gas/internal/serialport"
)

var (
	report  *os.File
	outMu   sync.Mutex
	vendorR = regexp.MustCompile(`(?i)easy|ez.?88|caller|callid|來電|捷|瓦斯|gas.?station|lazy_tel`)
	comR    = regexp.MustCompile(`(?i)\bcom\s*([0-9]{1,2})\b`)
	baudR   = regexp.MustCompile(`\b(1200|2400|4800|9600|19200|38400|57600|115200)\b`)
	digitR  = regexp.MustCompile(`[0-9][0-9\-]{5,}[0-9]`)
)

func say(format string, a ...any) {
	outMu.Lock()
	defer outMu.Unlock()
	s := fmt.Sprintf(format, a...)
	fmt.Println(s)
	if report != nil {
		report.WriteString(s + "\r\n")
	}
}

func title(s string) { say("\n==================== %s ====================", s) }

func main() {
	initConsole()
	exe, _ := os.Executable()
	name := filepath.Join(filepath.Dir(exe), "來電盒偵測結果_"+time.Now().Format("0102_1504")+".txt")
	if f, err := os.Create(name); err == nil {
		f.WriteString("\uFEFF")
		report = f
		defer f.Close()
	}
	host, _ := os.Hostname()
	say("來電盒偵測工具　%s　電腦：%s", time.Now().Format("2006-01-02 15:04:05"), host)
	say("結果會同時存到：%s", name)

	hints := findVendor()
	ports := serialport.Ports()
	title("COM 埠")
	if len(ports) == 0 {
		say("這台電腦沒有 COM 埠。盒子可能是 USB 裝置那一種，請看下面的裝置清單。")
	} else {
		say("找到：%s", strings.Join(ports, "、"))
	}
	title("USB / COM / 人性化介面裝置")
	for _, d := range serialport.Devices() {
		say("  %s", d)
	}

	baud := 9600
	if len(hints.bauds) > 0 {
		baud = hints.bauds[0]
	}
	if len(ports) > 0 {
		listen(ports, baud)
	}

	title("結束")
	say("請把這個檔案傳給 Claude：\r\n%s", name)
	fmt.Println("\n按 Enter 關閉視窗")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// ---------------------------------------------------------------------
// 找原廠軟體
// ---------------------------------------------------------------------

type vendorHints struct {
	bauds []int
	coms  []string
}

func findVendor() vendorHints {
	var h vendorHints
	title("正在執行的程式（不含 Windows 系統的）")
	if runtime.GOOS == "windows" {
		out := run("powershell", "-NoProfile", "-Command", `[Console]::OutputEncoding=[Text.Encoding]::UTF8; Get-Process | Where-Object { $_.Path -and $_.Path -notlike 'C:\Windows\*' } | Sort-Object Path -Unique | ForEach-Object { $_.ProcessName + ' | ' + $_.Path }`)
		for _, l := range lines(out) {
			mark := ""
			if vendorR.MatchString(l) {
				mark = "  ← 可能是原廠軟體"
			}
			say("  %s%s", l, mark)
		}
	}

	title("桌面與開機啟動的捷徑")
	var roots []string
	home, _ := os.UserHomeDir()
	for _, d := range []string{
		filepath.Join(home, "Desktop"), `C:\Users\Public\Desktop`,
		filepath.Join(os.Getenv("APPDATA"), `Microsoft\Windows\Start Menu\Programs\Startup`),
		`C:\ProgramData\Microsoft\Windows\Start Menu\Programs\StartUp`,
	} {
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if strings.HasSuffix(strings.ToLower(e.Name()), ".lnk") || strings.HasSuffix(strings.ToLower(e.Name()), ".url") {
				say("  %s", filepath.Join(d, e.Name()))
			}
		}
	}

	title("可能是原廠軟體的資料夾")
	for _, d := range []string{`C:\`, `C:\Program Files`, `C:\Program Files (x86)`, `C:\ProgramData`, os.Getenv("APPDATA"), os.Getenv("LOCALAPPDATA"), filepath.Join(home, "Desktop"), `D:\`} {
		if d == "" {
			continue
		}
		ents, _ := os.ReadDir(d)
		for _, e := range ents {
			if e.IsDir() && vendorR.MatchString(e.Name()) {
				roots = append(roots, filepath.Join(d, e.Name()))
			}
		}
	}
	if len(roots) == 0 {
		say("  沒找到名字像原廠軟體的資料夾")
	}
	var texts []string
	for _, r := range roots {
		say("\n  [資料夾] %s", r)
		count := 0
		filepath.WalkDir(r, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			if d.IsDir() {
				if strings.Count(strings.TrimPrefix(p, r), string(os.PathSeparator)) > 3 {
					return filepath.SkipDir
				}
				return nil
			}
			count++
			if count > 300 {
				return filepath.SkipAll
			}
			info, _ := d.Info()
			size := int64(0)
			if info != nil {
				size = info.Size()
			}
			say("    %s（%d）", strings.TrimPrefix(p, r), size)
			ext := strings.ToLower(filepath.Ext(p))
			if size > 0 && size < 64*1024 && strings.Contains(".ini.cfg.conf.config.json.xml.txt.inf.set.dat.log.bat", ext) {
				if t := readText(p); t != "" {
					texts = append(texts, t)
					say("    ------ 內容 ------\r\n%s\r\n    ------------------", indent(t, "      "))
				}
			}
			return nil
		})
	}

	if runtime.GOOS == "windows" {
		title("登錄檔裡的軟體名稱")
		for _, k := range []string{`HKCU\Software`, `HKLM\SOFTWARE`, `HKLM\SOFTWARE\WOW6432Node`} {
			for _, l := range lines(run("reg", "query", k)) {
				if vendorR.MatchString(filepath.Base(l)) {
					say("  %s", l)
					for _, v := range lines(run("reg", "query", l, "/s")) {
						say("    %s", v)
						texts = append(texts, v)
					}
				}
			}
		}
	}

	// 從設定檔裡找 COM 埠和鮑率
	seen := map[string]bool{}
	for _, t := range texts {
		for _, m := range comR.FindAllStringSubmatch(t, -1) {
			c := "COM" + m[1]
			if !seen[c] {
				seen[c] = true
				h.coms = append(h.coms, c)
			}
		}
		for _, m := range baudR.FindAllString(t, -1) {
			b, _ := strconv.Atoi(m)
			if !seen[m] {
				seen[m] = true
				h.bauds = append(h.bauds, b)
			}
		}
	}
	title("從原廠設定找到的線索")
	if len(h.coms)+len(h.bauds) == 0 {
		say("  沒找到 COM 埠或鮑率的設定")
	} else {
		say("  COM 埠：%v　鮑率：%v", h.coms, h.bauds)
	}
	return h
}

func run(name string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, name, args...).Output()
	return string(out)
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func readText(p string) string {
	b, err := os.ReadFile(p)
	if err != nil || len(b) == 0 {
		return ""
	}
	if len(b) > 4096 {
		b = b[:4096]
	}
	nul := 0
	for _, c := range b {
		if c == 0 {
			nul++
		}
	}
	if nul > len(b)/10 { // 二進位檔
		return ""
	}
	return strings.TrimSpace(string(b))
}

func indent(s, pre string) string {
	return pre + strings.ReplaceAll(strings.ReplaceAll(s, "\r", ""), "\n", "\r\n"+pre)
}

// ---------------------------------------------------------------------
// 聽 COM 埠
// ---------------------------------------------------------------------

func listen(ports []string, baud int) {
	cmds := make(chan string)
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			cmds <- strings.TrimSpace(sc.Text())
		}
		close(cmds)
	}()
	for {
		title(fmt.Sprintf("開始聽（鮑率 %d）", baud))
		say("現在用手機打一通到店裡電話，響兩三聲就可以掛掉。收到的資料會顯示在下面。")
		say("・換鮑率：輸入數字後按 Enter（例如 1200、2400、4800、9600、19200）")
		say("・重新打開 COM 埠：輸入 r 按 Enter（例如關掉原廠軟體之後）")
		say("・結束：直接按 Enter")
		ctx, cancel := context.WithCancel(context.Background())
		var wg sync.WaitGroup
		for _, p := range ports {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				listenOne(ctx, p, baud)
			}(p)
		}
		c, ok := <-cmds
		cancel()
		wg.Wait()
		if !ok || c == "" {
			return
		}
		if c == "r" || c == "R" {
			continue
		}
		if n, err := strconv.Atoi(c); err == nil && n >= 300 && n <= 921600 {
			baud = n
		} else {
			say("看不懂「%s」，維持鮑率 %d", c, baud)
		}
	}
}

func listenOne(ctx context.Context, port string, baud int) {
	p, err := serialport.Open(port, baud)
	if err != nil {
		hint := ""
		if strings.Contains(strings.ToLower(err.Error()), "denied") || strings.Contains(err.Error(), "拒") {
			hint = "　→ 有別的程式（多半是原廠軟體）正在用這個埠。先把它關掉，再輸入 r 按 Enter"
		}
		say("  %s 打不開：%v%s", port, err, hint)
		return
	}
	say("  %s 已打開，等資料中…", port)
	go func() { <-ctx.Done(); p.Close() }()
	buf := make([]byte, 512)
	var pending []byte
	last := time.Now()
	for ctx.Err() == nil {
		n, err := p.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			last = time.Now()
		}
		if len(pending) > 0 && (n == 0 || time.Since(last) > 300*time.Millisecond || len(pending) > 256) {
			show(port, pending)
			pending = nil
		}
		if err != nil {
			if err == io.EOF {
				time.Sleep(300 * time.Millisecond)
				continue
			}
			if ctx.Err() == nil {
				say("  %s 讀取中斷：%v", port, err)
			}
			return
		}
	}
}

func show(port string, b []byte) {
	var txt strings.Builder
	for _, c := range b {
		if c >= 32 && c < 127 {
			txt.WriteByte(c)
		} else {
			txt.WriteString("·")
		}
	}
	h := hex.EncodeToString(b)
	var hx []string
	for i := 0; i < len(h); i += 2 {
		hx = append(hx, h[i:i+2])
	}
	say("\n  [%s] %s 收到 %d 個位元組", time.Now().Format("15:04:05"), port, len(b))
	say("    文字：%s", txt.String())
	say("    原始：%s", strings.Join(hx, " "))
	var nums []string
	for _, m := range digitR.FindAllString(string(b), -1) {
		nums = append(nums, strings.ReplaceAll(m, "-", ""))
	}
	sort.Strings(nums)
	if len(nums) > 0 {
		say("    ★ 看起來像號碼：%s", strings.Join(nums, "、"))
	}
}
