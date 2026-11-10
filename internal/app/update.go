package app

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
)

// 線上更新：新版放在 GitHub Releases，程式自己去抓。
// 只認標籤是 V001、V002… 的正式版，其他（例如舊的 latest、測試版）不理。
var releaseAPI = "https://api.github.com/repos/Hallowyeeee/gas/releases?per_page=30"

type UpdateInfo struct {
	Current     string `json:"current"`
	Latest      string `json:"latest"`
	Available   bool   `json:"available"`
	Notes       string `json:"notes"`
	PublishedAt string `json:"published_at"`
	CheckedAt   string `json:"checked_at"`
	downloadURL string
}

type updater struct {
	mu       sync.Mutex
	info     *UpdateInfo
	applying bool
}

var releaseTag = regexp.MustCompile(`^[Vv](\d+)$`)

// versionNum：V001 → 1；不是正式版號（舊的日期版、dev）回傳 0。
func versionNum(v string) int {
	m := releaseTag.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return 0
	}
	n := 0
	fmt.Sscanf(m[1], "%d", &n)
	return n
}

// newerVersion：比 V 後面的數字。舊的日期版本號、dev 都算比 V001 舊。
func newerVersion(remote, local string) bool {
	r := versionNum(remote)
	return r > 0 && r > versionNum(local)
}

type ghRelease struct {
	TagName     string `json:"tag_name"`
	Body        string `json:"body"`
	PublishedAt string `json:"published_at"`
	Draft       bool   `json:"draft"`
	Prerelease  bool   `json:"prerelease"`
	Assets      []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

func (a *App) checkUpdate(ctx context.Context) (*UpdateInfo, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", releaseAPI, nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "gas-shop-updater")
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSHandshakeTimeout: 30 * time.Second}}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("連不上 GitHub：%s", shortErr(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub 回應 %d", resp.StatusCode)
	}
	var list []ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	// 挑版本號最大的正式版
	var rel *ghRelease
	for i := range list {
		r := &list[i]
		if r.Draft || r.Prerelease || versionNum(r.TagName) == 0 {
			continue
		}
		if rel == nil || versionNum(r.TagName) > versionNum(rel.TagName) {
			rel = r
		}
	}
	info := &UpdateInfo{Current: Version, CheckedAt: time.Now().Format("2006-01-02 15:04:05")}
	if rel != nil {
		info.Latest = strings.ToUpper(rel.TagName)
		info.PublishedAt = rel.PublishedAt
		if i := strings.Index(rel.Body, "更新內容"); i >= 0 {
			info.Notes = strings.TrimSpace(rel.Body[i:])
		}
		for _, as := range rel.Assets {
			if as.Name == "gas.exe" {
				info.downloadURL = as.URL
			}
		}
	}
	info.Available = info.downloadURL != "" && newerVersion(info.Latest, Version)
	a.upd.mu.Lock()
	a.upd.info = info
	a.upd.mu.Unlock()
	return info, nil
}

// CheckUpdateOnStart 開程式 15 秒後自動檢查一次，之後每 6 小時一次。
// 半夜 2～5 點如果有新版，而且設定沒有關掉「半夜自動更新」，就自動更新（會先備份）。
func (a *App) CheckUpdateOnStart(ctx context.Context, notify func(string)) {
	if a.ExePath != "" {
		go func() { time.Sleep(10 * time.Second); os.Remove(a.ExePath + ".old") }()
	}
	select {
	case <-ctx.Done():
		return
	case <-time.After(15 * time.Second):
	}
	var last time.Time
	for {
		now := time.Now()
		night := a.autoUpdateOn() && inNightWindow(now)
		if now.Sub(last) >= 6*time.Hour || (night && now.Sub(last) >= 25*time.Minute) {
			last = now
			info, err := a.checkUpdate(ctx)
			switch {
			case err != nil:
			case info.Available && night:
				log.Println("半夜自動更新到", info.Latest)
				if _, err := a.applyUpdate(ctx); err != nil {
					log.Println("自動更新失敗：", err)
				}
			case info.Available && notify != nil:
				notify("有新版本 " + info.Latest + "，半夜會自動更新；也可以到「設定」按更新")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Minute):
		}
	}
}

// inNightWindow 半夜 2:00～4:59 才自動更新，不會打擾營業。
func inNightWindow(t time.Time) bool { return t.Hour() >= 2 && t.Hour() < 5 }

// autoUpdateOn 設定 auto_update = "off" 才關掉；預設開。
func (a *App) autoUpdateOn() bool {
	if a.ExePath == "" || a.Restart == nil {
		return false
	}
	var v string
	a.DB.QueryRow(`SELECT value FROM settings WHERE key = 'auto_update'`).Scan(&v)
	return v != "off"
}

func (a *App) updateStatus() *UpdateInfo {
	a.upd.mu.Lock()
	defer a.upd.mu.Unlock()
	return a.upd.info
}

func (a *App) handleUpdateCheck(r *http.Request, u *User) (any, error) {
	info, err := a.checkUpdate(r.Context())
	if err != nil {
		return nil, bad("檢查更新失敗：" + err.Error())
	}
	return info, nil
}

// 一鍵更新：先備份 → 下載新版 → 換掉舊的 → 重新開啟
func (a *App) handleUpdateApply(r *http.Request, u *User) (any, error) {
	if !isLocal(r) {
		return nil, &apiError{http.StatusForbidden, "更新只能在店裡的電腦上按"}
	}
	v, err := a.applyUpdate(r.Context())
	if err != nil {
		return nil, bad(err.Error())
	}
	return map[string]any{"ok": true, "version": v}, nil
}

func (a *App) applyUpdate(ctx context.Context) (string, error) {
	if a.ExePath == "" || a.Restart == nil {
		return "", fmt.Errorf("這個版本不支援自動更新")
	}
	a.upd.mu.Lock()
	if a.upd.applying {
		a.upd.mu.Unlock()
		return "", fmt.Errorf("正在更新中")
	}
	a.upd.applying = true
	a.upd.mu.Unlock()
	defer func() { a.upd.mu.Lock(); a.upd.applying = false; a.upd.mu.Unlock() }()

	info, err := a.checkUpdate(ctx)
	if err != nil {
		return "", fmt.Errorf("檢查更新失敗：%w", err)
	}
	if !info.Available {
		return "", fmt.Errorf("已經是最新版")
	}
	if a.Backup != nil {
		if _, err := a.Backup.Now(); err != nil {
			return "", fmt.Errorf("更新前備份失敗，先不更新：%w", err)
		}
	}
	newPath := a.ExePath + ".new"
	os.Remove(newPath)
	if err := download(info.downloadURL, newPath); err != nil {
		return "", fmt.Errorf("下載新版失敗：%s", shortErr(err))
	}
	if err := checkExe(newPath); err != nil {
		os.Remove(newPath)
		return "", err
	}
	oldPath := a.ExePath + ".old"
	os.Remove(oldPath)
	if err := os.Rename(a.ExePath, oldPath); err != nil {
		os.Remove(newPath)
		return "", fmt.Errorf("換新版失敗：%w", err)
	}
	if err := os.Rename(newPath, a.ExePath); err != nil {
		os.Rename(oldPath, a.ExePath) // 換回舊的
		return "", fmt.Errorf("換新版失敗：%w", err)
	}
	log.Println("已下載新版", info.Latest, "，重新啟動")
	go func() { time.Sleep(800 * time.Millisecond); a.Restart() }()
	return info.Latest, nil
}

// checkExe 確認下載的是完整的 Windows 程式。
func checkExe(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if len(b) < 1<<20 || !bytes.HasPrefix(b, []byte("MZ")) {
		return fmt.Errorf("下載的檔案不完整，請稍後再試")
	}
	return nil
}
