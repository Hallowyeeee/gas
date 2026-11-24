package app

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Station 是「這台電腦」的設定，存在 gas.exe 旁邊的 station.json。
// 沒有這個檔 = 主電腦（跟以前一樣）。
type Station struct {
	Mode     string   `json:"mode"`                // "main" 主電腦；"client" 分機
	Server   string   `json:"server,omitempty"`    // 分機：外面連主電腦的網址
	LAN      []string `json:"lan,omitempty"`       // 分機：店裡直連主電腦的位址（自動找到的）
	ManualIP string   `json:"manual_ip,omitempty"` // 分機：手動填的主電腦位址（自動找不到時用）
	ServerID string   `json:"server_id,omitempty"` // 分機：主電腦的身分碼，確認連到的是同一台
	Popup    *bool    `json:"popup,omitempty"`     // 來電時跳出客人資料（每台自己設定，預設開）
}

func StationPath(dir string) string { return filepath.Join(dir, "station.json") }

// LoadStation 讀這台電腦的設定。沒有檔案時 Mode 是空的（還沒選），由程式決定：
// 已經有資料庫 = 舊的主電腦；全新安裝 = 第一次打開時問「主電腦還是分機」。
func LoadStation(path string) *Station {
	st := &Station{}
	b, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	json.Unmarshal(b, st)
	if st.Mode != "client" {
		st.Mode = "main"
	}
	return st
}

// ---------------------------------------------------------------------
// 連線碼：外面的分機第一次連時輸入，裡面是主電腦的外面網址和身分碼。
// ---------------------------------------------------------------------

const codePrefix = "GAS-"

type connCode struct {
	U  string `json:"u"`
	ID string `json:"id"`
}

func makeCode(publicURL, id string) string {
	if publicURL == "" {
		return ""
	}
	b, _ := json.Marshal(connCode{publicURL, id})
	return codePrefix + base64.RawURLEncoding.EncodeToString(b)
}

// parseCode 看懂連線碼；也接受直接打網址（例如 店名.hallowyeeee.com）。
func parseCode(s string) (url, id string, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", nil
	}
	if strings.HasPrefix(strings.ToUpper(s), codePrefix) {
		b, e := base64.RawURLEncoding.DecodeString(strings.TrimRight(s[len(codePrefix):], "="))
		var c connCode
		if e != nil || json.Unmarshal(b, &c) != nil || c.U == "" {
			return "", "", bad("連線碼不對，請從主電腦「設定 → 這台電腦」重新複製")
		}
		return c.U, c.ID, nil
	}
	if strings.ContainsAny(s, " \t") || !strings.Contains(s, ".") {
		return "", "", bad("請貼上主電腦的連線碼（GAS- 開頭）")
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	return strings.TrimRight(s, "/"), "", nil
}

func (s *Station) Save(path string) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	return os.WriteFile(path, b, 0o644)
}

func (s *Station) PopupOn() bool { return s == nil || s.Popup == nil || *s.Popup }

// stationView 給畫面看的「這台電腦」資料。
func (s *Station) view() map[string]any {
	return map[string]any{
		"mode": s.Mode, "server": s.Server, "manual_ip": s.ManualIP, "popup": s.PopupOn(),
		"server_id": s.ServerID, "version": Version,
	}
}

// stationInput 畫面送來要改的設定。
type stationInput struct {
	Mode     *string `json:"mode"`
	Server   *string `json:"server"` // 連線碼或網址
	ManualIP *string `json:"manual_ip"`
	Popup    *bool   `json:"popup"`
}

// apply 套用畫面的修改，回傳是不是換了主電腦/分機（要重開程式）。
func (s *Station) apply(in stationInput) (bool, error) {
	changed := false
	if in.Mode != nil && *in.Mode != s.Mode {
		if *in.Mode != "main" && *in.Mode != "client" {
			return false, bad("只能選主電腦或分機")
		}
		s.Mode = *in.Mode
		changed = true
	}
	if in.Server != nil && strings.TrimSpace(*in.Server) != "" {
		u, id, err := parseCode(*in.Server)
		if err != nil {
			return false, err
		}
		if u != s.Server || (id != "" && id != s.ServerID) {
			if id != s.ServerID {
				s.LAN = nil // 換一台主電腦了，店內位址重新找
			}
			s.Server, s.ServerID = u, id
		}
	}
	if in.ManualIP != nil {
		s.ManualIP = strings.TrimSpace(*in.ManualIP)
	}
	if in.Popup != nil {
		v := *in.Popup
		s.Popup = &v
	}
	return changed, nil
}

// ---------------------------------------------------------------------
// 主電腦這邊
// ---------------------------------------------------------------------

// serverID 主電腦的身分碼（第一次用到時產生，存在資料庫）。
func (a *App) serverID() string {
	var v string
	err := a.DB.QueryRow(`SELECT value FROM settings WHERE key = 'server_id'`).Scan(&v)
	if err == sql.ErrNoRows || v == "" {
		v = newToken()[:16]
		a.DB.Exec(`INSERT INTO settings (key, value) VALUES ('server_id', ?) ON CONFLICT (key) DO NOTHING`, v)
		a.DB.QueryRow(`SELECT value FROM settings WHERE key = 'server_id'`).Scan(&v)
	}
	return v
}

// handleStationInfo 分機用來找主電腦：回傳身分碼和店裡直連的位址。不用登入，不含任何客戶資料。
func (a *App) handleStationInfo(w http.ResponseWriter, r *http.Request) (any, error) {
	pub, _ := a.Tunnel.Info()
	return map[string]any{"id": a.serverID(), "lan": lanAddresses(a.Port), "public": pub, "version": Version}, nil
}

// handleLocalStation 主電腦自己的「這台電腦」設定（只有本機程式視窗能改）。
func (a *App) handleLocalStation(w http.ResponseWriter, r *http.Request) (any, error) {
	if !isLocal(r) || a.Station == nil {
		return nil, forbidden()
	}
	if r.Method == http.MethodGet {
		v := a.Station.view()
		v["lan"] = lanAddresses(a.Port)
		pub, _ := a.Tunnel.Info()
		v["public"], v["code"] = pub, makeCode(pub, a.serverID())
		return v, nil
	}
	var in stationInput
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	changed, err := a.Station.apply(in)
	if err != nil {
		return nil, err
	}
	if err := a.Station.Save(a.StationFile); err != nil {
		return nil, err
	}
	if changed && a.Restart != nil {
		go a.Restart()
	}
	return map[string]any{"ok": true, "restart": changed}, nil
}

// handleFirewall 主電腦：打開 Windows 防火牆，讓店裡其他電腦連進來（會跳出系統管理員確認）。
func (a *App) handleFirewall(w http.ResponseWriter, r *http.Request) (any, error) {
	if !isLocal(r) {
		return nil, forbidden()
	}
	if err := openFirewall(a.Port); err != nil {
		return nil, bad("打開防火牆失敗：" + err.Error())
	}
	return map[string]bool{"ok": true}, nil
}
