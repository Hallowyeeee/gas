// 瓦斯行管理系統主程式：雙擊就會啟動後端、開 Cloudflare 通道、每日備份，並打開瀏覽器。
package main

import (
	"context"
	"flag"
	"fmt"
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
	noBrowser := flag.Bool("no-browser", false, "啟動後不要自動打開瀏覽器")
	flag.Parse()

	logFile, err := os.OpenFile(filepath.Join(baseDir, "gas.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err == nil {
		log.SetOutput(multiWriter{os.Stdout, logFile})
	}
	app.Version = version
	log.Println("瓦斯行管理系統 啟動，版本", version)

	db, err := app.OpenDB(*dbPath)
	if err != nil {
		fatal("開啟資料庫失敗：", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	a := &app.App{DB: db, Static: web.Static()}
	a.Backup = &app.Backup{DB: db, DefaultDir: filepath.Join(baseDir, "backups")}
	go a.Backup.Run(ctx)
	if !*noTunnel {
		a.Tunnel = &app.Tunnel{Port: *port, Dir: baseDir, Download: true}
		go a.Tunnel.Run(ctx)
	}

	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", *port))
	if err != nil {
		fatal(fmt.Sprintf("埠號 %d 被占用，可能程式已經開著了：", *port), err)
	}
	srv := &http.Server{Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		srv.Shutdown(context.Background())
	}()
	local := fmt.Sprintf("http://localhost:%d", *port)
	log.Println("電腦請開：", local)
	if !*noBrowser {
		go openBrowser(local)
	}
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		fatal("網頁服務錯誤：", err)
	}
	db.Close()
}

func openBrowser(url string) {
	time.Sleep(500 * time.Millisecond)
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
	fmt.Println("\n按 Enter 關閉…")
	fmt.Scanln()
	os.Exit(1)
}

type multiWriter []interface{ Write([]byte) (int, error) }

func (m multiWriter) Write(p []byte) (int, error) {
	for _, w := range m {
		w.Write(p)
	}
	return len(p), nil
}
