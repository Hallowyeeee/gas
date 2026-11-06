// 瓦斯行管理系統主程式：雙擊就會啟動後端、開 Cloudflare 通道、每日備份，並打開程式視窗。
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"github.com/hallowyeeee/gas/internal/app"
	"github.com/hallowyeeee/gas/web"
)

var version = "dev"

func main() {
	exe, _ := os.Executable()
	baseDir := filepath.Dir(exe)

	port := flag.Int("port", 8080, "網頁埠號")
	dbPath := flag.String("db", filepath.Join(baseDir, "gas.db"), "資料庫檔案")
	noTunnel := flag.Bool("no-tunnel", false, "不要開 Cloudflare 通道")
	noBrowser := flag.Bool("no-browser", false, "不要開視窗（只跑後端）")
	useBrowser := flag.Bool("browser", false, "用瀏覽器開，不用程式視窗")
	flag.Parse()

	var out io.Writer = os.Stdout
	if logFile, err := os.OpenFile(filepath.Join(baseDir, "gas.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644); err == nil {
		out = io.MultiWriter(logFile, os.Stdout) // 視窗版沒有黑色視窗，stdout 會失敗，所以檔案放前面
	}
	log.SetOutput(out)
	app.Version = version
	local := fmt.Sprintf("http://localhost:%d", *port)

	// 已經開著了：叫原本的視窗出來就好
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		if bringToFront(*port) {
			return
		}
		fatal(fmt.Sprintf("埠號 %d 被其他程式占用，無法啟動。", *port), err)
	}
	log.Println("瓦斯行管理系統 啟動，版本", version)

	db, err := app.OpenDB(*dbPath)
	if err != nil {
		fatal("開啟資料庫失敗：", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	ui := newUI(local, baseDir)
	restart := false
	a := &app.App{DB: db, Static: web.Static(), ExePath: exe}
	a.Restart = func() { restart = true; ui.Quit(); stop() }
	a.Backup = &app.Backup{DB: db, DefaultDir: filepath.Join(baseDir, "backups")}
	a.ShowWindow = ui.Show
	go a.Backup.Run(ctx)
	if !*noTunnel {
		a.Tunnel = &app.Tunnel{Port: *port, Dir: baseDir, Download: true}
		go a.Tunnel.Run(ctx)
	}
	go a.CheckUpdateOnStart(ctx, ui.Notify)
	a.StartCallerBox()

	srv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Println("網頁服務錯誤：", err)
		}
	}()
	log.Println("電腦請開：", local)

	switch {
	case *noBrowser:
		<-ctx.Done()
	case *useBrowser:
		openBrowser(local)
		<-ctx.Done()
	default:
		ui.Run(ctx) // 一直到按「結束」才回來
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	srv.Shutdown(shutdown)
	cancel()
	db.Close()
	if restart {
		log.Println("重新啟動新版")
		cmd := exec.Command(exe)
		cmd.Dir = baseDir
		if err := cmd.Start(); err != nil {
			fatal("新版啟動失敗，請手動雙擊 gas.exe：", err)
		}
		return
	}
	log.Println("程式結束")
}

// bringToFront 請已經在跑的那一個把視窗叫出來。
func bringToFront(port int) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Post(fmt.Sprintf("http://127.0.0.1:%d/api/local/show", port), "application/json", nil)
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == 200
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "windows":
		exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		exec.Command("open", url).Start()
	default:
		exec.Command("xdg-open", url).Start()
	}
}

func fatal(msg string, err error) {
	log.Println(msg, err)
	showError("瓦斯行管理系統", fmt.Sprintf("%s\n%v\n\n詳細紀錄在 gas.log", msg, err))
	os.Exit(1)
}
