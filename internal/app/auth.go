package app

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookie = "gas_session"

// User 是目前登入的人。
type User struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Role  string `json:"role"`
	Login string `json:"login_name"`
}

func (u *User) IsDriver() bool { return u.Role == "driver" }

func hashPassword(pw string) (string, error) {
	if len([]rune(pw)) < 6 {
		return "", bad("密碼至少 6 個字")
	}
	b, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(b), err
}

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (a *App) userFromRequest(r *http.Request) (*User, error) {
	token := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		token = c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		token = strings.TrimPrefix(h, "Bearer ")
	}
	if token == "" {
		return nil, nil
	}
	u := &User{}
	err := a.DB.QueryRowContext(r.Context(), `
		SELECT u.id, u.name, u.role, u.login_name FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token = ? AND s.expires_at > datetime('now','localtime') AND u.active = 1`, token).
		Scan(&u.ID, &u.Name, &u.Role, &u.Login)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// route 包裝一個 API：檢查登入、角色、防止跨站請求。
// roles 空白 = 任何登入的人都可以。
func (a *App) route(h handlerFunc, roles ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeErr(w, bad("請用 JSON 送資料"))
			return
		}
		u, err := a.userFromRequest(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		if u == nil {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "請先登入"})
			return
		}
		if len(roles) > 0 {
			ok := false
			for _, role := range roles {
				if role == u.Role {
					ok = true
				}
			}
			if !ok {
				writeErr(w, forbidden())
				return
			}
		}
		res, err := h(r, u)
		if err != nil {
			writeErr(w, err)
			return
		}
		if res == nil {
			res = map[string]bool{"ok": true}
		}
		writeJSON(w, http.StatusOK, res)
	}
}

// 不用登入的 API
func (a *App) public(h func(w http.ResponseWriter, r *http.Request) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			writeErr(w, bad("請用 JSON 送資料"))
			return
		}
		res, err := h(w, r)
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, res)
	}
}

func (a *App) countUsers(ctx context.Context) (int, error) {
	var n int
	err := a.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

// isLocal：請求是不是從店裡這台電腦本機來的（不是經過 Cloudflare 通道）。
func isLocal(r *http.Request) bool {
	if r.Header.Get("Cf-Connecting-Ip") != "" || r.Header.Get("X-Forwarded-For") != "" {
		return false
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *App) handleSetupStatus(w http.ResponseWriter, r *http.Request) (any, error) {
	n, err := a.countUsers(r.Context())
	if err != nil {
		return nil, err
	}
	return map[string]any{"needed": n == 0, "local": isLocal(r)}, nil
}

// 第一次使用：在店裡電腦上建立老闆帳號
func (a *App) handleSetup(w http.ResponseWriter, r *http.Request) (any, error) {
	if !isLocal(r) {
		return nil, &apiError{http.StatusForbidden, "第一次設定只能在店裡的電腦上操作"}
	}
	var in struct {
		Name      string `json:"name"`
		LoginName string `json:"login_name"`
		Password  string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.LoginName) == "" {
		return nil, bad("請填名字和登入帳號")
	}
	hash, err := hashPassword(in.Password)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if n, err := a.countUsers(r.Context()); err != nil || n > 0 {
		return nil, bad("已經設定過了")
	}
	res, err := a.DB.ExecContext(r.Context(), `INSERT INTO users (name, role, login_name, password_hash) VALUES (?, 'admin', ?, ?)`,
		strings.TrimSpace(in.Name), strings.TrimSpace(in.LoginName), hash)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return a.startSession(w, r, &User{ID: id, Name: strings.TrimSpace(in.Name), Role: "admin", Login: strings.TrimSpace(in.LoginName)})
}

func (a *App) handleLogin(w http.ResponseWriter, r *http.Request) (any, error) {
	var in struct {
		LoginName string `json:"login_name"`
		Password  string `json:"password"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	u := &User{}
	var hash string
	err := a.DB.QueryRowContext(r.Context(), `SELECT id, name, role, login_name, password_hash FROM users WHERE login_name = ? AND active = 1`,
		strings.TrimSpace(in.LoginName)).Scan(&u.ID, &u.Name, &u.Role, &u.Login, &hash)
	if err == nil {
		err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password))
	}
	if err != nil {
		time.Sleep(400 * time.Millisecond) // 減慢猜密碼
		return nil, &apiError{http.StatusUnauthorized, "帳號或密碼錯誤"}
	}
	return a.startSession(w, r, u)
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, u *User) (any, error) {
	token := newToken()
	expires := time.Now().AddDate(0, 0, 60)
	if _, err := a.DB.ExecContext(r.Context(), `INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		token, u.ID, expires.Format("2006-01-02 15:04:05")); err != nil {
		return nil, err
	}
	a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE expires_at < datetime('now','localtime')`)
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: expires,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil || r.Header.Get("Cf-Connecting-Ip") != "",
	})
	return map[string]any{"user": u, "token": token}, nil
}

func (a *App) handleLogout(r *http.Request, u *User) (any, error) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE token = ?`, c.Value)
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE token = ?`, strings.TrimPrefix(h, "Bearer "))
	}
	return nil, nil
}

func (a *App) handleMe(r *http.Request, u *User) (any, error) { return u, nil }

// ---------------------------------------------------------------------
// 使用者管理（老闆）
// ---------------------------------------------------------------------

func (a *App) listUsers(r *http.Request, u *User) (any, error) {
	return rows(r.Context(), a.DB, `SELECT id, name, role, phone, login_name, active, created_at FROM users ORDER BY active DESC, role, name`)
}

func (a *App) listDrivers(r *http.Request, u *User) (any, error) {
	return rows(r.Context(), a.DB, `SELECT id, name, phone FROM users WHERE role = 'driver' AND active = 1 ORDER BY name`)
}

func (a *App) createUser(r *http.Request, u *User) (any, error) {
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, []string{"name", "role", "phone", "login_name", "active"})
	if err != nil {
		return nil, err
	}
	pw, _ := in["password"].(string)
	hash, err := hashPassword(pw)
	if err != nil {
		return nil, err
	}
	f["password_hash"] = hash
	id, err := insertRow(r.Context(), a.DB, "users", f)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": id}, nil
}

func (a *App) updateUser(r *http.Request, u *User) (any, error) {
	id, err := pathID(r)
	if err != nil {
		return nil, err
	}
	var in map[string]any
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	f, err := filterFields(in, []string{"name", "role", "phone", "login_name", "active"})
	if err != nil {
		return nil, err
	}
	if pw, ok := in["password"].(string); ok && pw != "" {
		hash, err := hashPassword(pw)
		if err != nil {
			return nil, err
		}
		f["password_hash"] = hash
	}
	if id == u.ID && (f["role"] != nil && f["role"] != "admin" || f["active"] == int64(0)) {
		return nil, bad("不能把自己停用或拿掉老闆權限")
	}
	if err := updateRow(r.Context(), a.DB, "users", id, f, ""); err != nil {
		return nil, err
	}
	if f["active"] == int64(0) || f["password_hash"] != nil {
		a.DB.ExecContext(r.Context(), `DELETE FROM sessions WHERE user_id = ? AND user_id <> ?`, id, u.ID)
	}
	return nil, nil
}
