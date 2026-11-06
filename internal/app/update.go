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
func (a *App) CheckUpdateOnStart(ctx context.Context, notify func(string)) {
	if a.ExePath != "" {
		go func() { time.Sleep(10 * time.Second); os.Remove(a.ExePath + ".old") }()
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
		if info, err := a.checkUpdate(ctx); err == nil && info.Available && notify != nil {
			notify("有新版本 " + info.Latest + "，請到「設定」按更新")
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(6 * time.Hour):
		}
	}
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
	if a.ExePath == "" || a.Restart == nil {
		return nil, bad("這個版本不支援自動更新")
	}
	a.upd.mu.Lock()
	if a.upd.applying {
		a.upd.mu.Unlock()
		return nil, bad("正在更新中")
	}
	a.upd.applying = true
	a.upd.mu.Unlock()
	defer func() { a.upd.mu.Lock(); a.upd.applying = false; a.upd.mu.Unlock() }()

	info, err := a.checkUpdate(r.Context())
	if err != nil {
		return nil, bad("檢查更新失敗：" + err.Error())
	}
	if !info.Available {
		return nil, bad("已經是最新版")
	}
	if a.Backup != nil {
		if _, err := a.Backup.Now(); err != nil {
			return nil, bad("更新前備份失敗，先不更新：" + err.Error())
		}
	}
	newPath := a.ExePath + ".new"
	os.Remove(newPath)
	if err := download(info.downloadURL, newPath); err != nil {
		return nil, bad("下載新版失敗：" + shortErr(err))
	}
	if err := checkExe(newPath); err != nil {
		os.Remove(newPath)
		return nil, bad(err.Error())
	}
	oldPath := a.ExePath + ".old"
	os.Remove(oldPath)
	if err := os.Rename(a.ExePath, oldPath); err != nil {
		os.Remove(newPath)
		return nil, bad("換新版失敗：" + err.Error())
	}
	if err := os.Rename(newPath, a.ExePath); err != nil {
		os.Rename(oldPath, a.ExePath) // 換回舊的
		return nil, bad("換新版失敗：" + err.Error())
	}
	log.Println("已下載新版", info.Latest, "，重新啟動")
	go func() { time.Sleep(800 * time.Millisecond); a.Restart() }()
	return map[string]any{"ok": true, "version": info.Latest}, nil
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
