//go:build windows

package main

import (
	"context"
	_ "embed"
	"log"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"fyne.io/systray"
	webview2 "github.com/jchv/go-webview2"
)

//go:embed icon.ico
var iconICO []byte

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procShowWindow       = user32.NewProc("ShowWindow")
	procSetForeground    = user32.NewProc("SetForegroundWindow")
	procIsIconic         = user32.NewProc("IsIconic")
	procSetWindowLongPtr = user32.NewProc("SetWindowLongPtrW")
	procCallWindowProc   = user32.NewProc("CallWindowProcW")
	procMessageBox       = user32.NewProc("MessageBoxW")
)

const (
	wmClose     = 0x0010
	swHide      = 0
	swShow      = 5
	swRestore   = 9
	gwlpWndProc = ^uintptr(3) // -4
)

// ui 是電腦上的「瓦斯行」程式視窗 + 右下角小圖示。
type ui struct {
	url, dir string
	wv       webview2.WebView
	hwnd     uintptr
	oldProc  uintptr
	quit     chan struct{}
	once     sync.Once
	ready    chan struct{}
}

func newUI(url, dir string) *ui {
	return &ui{url: url, dir: dir, quit: make(chan struct{}), ready: make(chan struct{})}
}

func (u *ui) Run(ctx context.Context) {
	go u.tray()
	runtime.LockOSThread()
	wv := webview2.NewWithOptions(webview2.WebViewOptions{
		DataPath:  filepath.Join(u.dir, "webview-data"),
		AutoFocus: true,
		WindowOptions: webview2.WindowOptions{
			Title: "瓦斯行管理系統", Width: 1280, Height: 860, IconId: 1, Center: true,
		},
	})
	if wv == nil {
		// 電腦沒有 WebView2（很舊的 Windows）：改用瀏覽器
		log.Println("找不到 WebView2，改用瀏覽器開啟")
		openBrowser(u.url)
		close(u.ready)
		select {
		case <-ctx.Done():
		case <-u.quit:
		}
		systray.Quit()
		return
	}
	u.wv = wv
	u.hwnd = uintptr(wv.Window())
	// 按 X 不關閉，只是藏起來（程式繼續在背景跑，師傅才收得到單）
	cb := syscall.NewCallback(func(hwnd, msg, wp, lp uintptr) uintptr {
		if msg == wmClose {
			procShowWindow.Call(hwnd, swHide)
			return 0
		}
		r, _, _ := procCallWindowProc.Call(u.oldProc, hwnd, msg, wp, lp)
		return r
	})
	u.oldProc, _, _ = procSetWindowLongPtr.Call(u.hwnd, gwlpWndProc, cb)
	wv.Navigate(u.url)
	close(u.ready)
	go func() {
		select {
		case <-ctx.Done():
		case <-u.quit:
		}
		wv.Dispatch(wv.Terminate)
	}()
	wv.Run()
	systray.Quit()
}

// Show 把視窗叫出來放到最前面。
func (u *ui) Show() {
	<-u.ready
	if u.wv == nil {
		openBrowser(u.url)
		return
	}
	u.wv.Dispatch(func() {
		if r, _, _ := procIsIconic.Call(u.hwnd); r != 0 {
			procShowWindow.Call(u.hwnd, swRestore)
		} else {
			procShowWindow.Call(u.hwnd, swShow)
		}
		procSetForeground.Call(u.hwnd)
	})
}

// Notify 在右下角小圖示顯示提醒文字。
func (u *ui) Notify(msg string) {
	systray.SetTooltip("瓦斯行管理系統｜" + msg)
	log.Println(msg)
}

// Quit 結束整個程式。
func (u *ui) Quit() { u.once.Do(func() { close(u.quit) }) }

func (u *ui) tray() {
	runtime.LockOSThread()
	systray.Run(func() {
		systray.SetIcon(iconICO)
		systray.SetTooltip("瓦斯行管理系統（執行中）")
		systray.SetOnTapped(u.Show)
		open := systray.AddMenuItem("打開瓦斯行", "")
		browser := systray.AddMenuItem("用瀏覽器打開", "")
		systray.AddSeparator()
		quit := systray.AddMenuItem("結束程式（師傅會收不到單）", "")
		go func() {
			for {
				select {
				case <-open.ClickedCh:
					u.Show()
				case <-browser.ClickedCh:
					openBrowser(u.url)
				case <-quit.ClickedCh:
					u.Quit()
					return
				}
			}
		}()
	}, nil)
}

func showError(title, msg string) {
	t, _ := syscall.UTF16PtrFromString(title)
	m, _ := syscall.UTF16PtrFromString(msg)
	procMessageBox.Call(0, uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), 0x10)
}
