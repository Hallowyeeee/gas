package app

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// ---------------------------------------------------------------------
// 即時更新：畫面開著時，資料一變就通知（Server-Sent Events）
// ---------------------------------------------------------------------

type Event struct {
	Type    string         `json:"type"`              // "orders" 資料有變、"dispatch" 有新派單、"call" 來電
	Message string         `json:"message,omitempty"` // 通知文字
	Order   map[string]any `json:"order,omitempty"`
	Call    map[string]any `json:"call,omitempty"`    // "call" 有電話打進來
	Capture map[string]any `json:"capture,omitempty"` // "capture" 從舊系統抓到一位客人，等確認
}

type sub struct {
	user *User
	ch   chan Event
}

type Hub struct {
	mu   sync.Mutex
	subs map[*sub]struct{}
}

func (h *Hub) add(u *User) *sub {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.subs == nil {
		h.subs = map[*sub]struct{}{}
	}
	s := &sub{user: u, ch: make(chan Event, 16)}
	h.subs[s] = struct{}{}
	return s
}

func (h *Hub) remove(s *sub) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.subs, s)
}

// Publish 發給某位使用者（userID > 0）或所有店裡的人和相關師傅（userID = 0）。
func (h *Hub) Publish(userID int64, e Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if userID != 0 && s.user.ID != userID && s.user.IsDriver() {
			continue
		}
		if userID == 0 && s.user.IsDriver() && e.Type != "orders" {
			continue
		}
		select {
		case s.ch <- e:
		default: // 對方太慢就略過，畫面會定時自己更新
		}
	}
}

func (a *App) handleEvents(w http.ResponseWriter, r *http.Request) {
	u, err := a.userFromRequest(r)
	if err != nil || u == nil {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "請先登入"})
		return
	}
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, bad("不支援即時更新"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	s := a.Events.add(u)
	defer a.Events.remove(s)
	fmt.Fprint(w, "retry: 3000\n\n")
	fl.Flush()
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case e := <-s.ch:
			b, _ := json.Marshal(e)
			fmt.Fprintf(w, "data: %s\n\n", b)
			fl.Flush()
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		}
	}
}

// 有改到訂單的請求成功後，通知所有開著的畫面重新整理
func (a *App) notifyChanges(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		next.ServeHTTP(rec, r)
		if r.Method != http.MethodGet && rec.status == 200 && strings.HasPrefix(r.URL.Path, "/api/") &&
			!strings.HasPrefix(r.URL.Path, "/api/login") && !strings.HasPrefix(r.URL.Path, "/api/push") && !strings.HasPrefix(r.URL.Path, "/api/local") {
			a.Events.Publish(0, Event{Type: "orders"})
		}
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// ---------------------------------------------------------------------
// 手機推播通知：師傅手機就算沒開畫面也會跳通知
// ---------------------------------------------------------------------

func (a *App) vapidKeys() (string, string, error) {
	var pub, priv string
	a.DB.QueryRow(`SELECT value FROM settings WHERE key = 'vapid_public'`).Scan(&pub)
	a.DB.QueryRow(`SELECT value FROM settings WHERE key = 'vapid_private'`).Scan(&priv)
	if pub != "" && priv != "" {
		return pub, priv, nil
	}
	priv, pub, err := webpush.GenerateVAPIDKeys()
	if err != nil {
		return "", "", err
	}
	_, err = a.DB.Exec(`INSERT INTO settings (key, value) VALUES ('vapid_public', ?), ('vapid_private', ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, pub, priv)
	return pub, priv, err
}

func (a *App) pushKey(r *http.Request, u *User) (any, error) {
	pub, _, err := a.vapidKeys()
	return map[string]string{"public_key": pub}, err
}

func (a *App) pushSubscribe(r *http.Request, u *User) (any, error) {
	var in struct {
		Endpoint string `json:"endpoint"`
		Keys     struct {
			P256dh string `json:"p256dh"`
			Auth   string `json:"auth"`
		} `json:"keys"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(in.Endpoint, "https://") || in.Keys.P256dh == "" || in.Keys.Auth == "" {
		return nil, bad("通知訂閱資料不完整")
	}
	_, err := a.DB.ExecContext(r.Context(), `INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth) VALUES (?, ?, ?, ?)
		ON CONFLICT (endpoint) DO UPDATE SET user_id = excluded.user_id, p256dh = excluded.p256dh, auth = excluded.auth`,
		u.ID, in.Endpoint, in.Keys.P256dh, in.Keys.Auth)
	return nil, err
}

// SendPush 發推播給某位使用者的所有手機。
func (a *App) SendPush(userID int64, title, body, url string) {
	pub, priv, err := a.vapidKeys()
	if err != nil {
		log.Println("推播金鑰錯誤:", err)
		return
	}
	list, err := rows(nilCtx(), a.DB, `SELECT id, endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = ?`, userID)
	if err != nil {
		return
	}
	payload, _ := json.Marshal(map[string]string{"title": title, "body": body, "url": url})
	for _, s := range list {
		sub := &webpush.Subscription{Endpoint: s["endpoint"].(string), Keys: webpush.Keys{P256dh: s["p256dh"].(string), Auth: s["auth"].(string)}}
		resp, err := webpush.SendNotification(payload, sub, &webpush.Options{
			Subscriber: "mailto:gas-shop@example.invalid", VAPIDPublicKey: pub, VAPIDPrivateKey: priv, TTL: 3600, Urgency: webpush.UrgencyHigh,
		})
		if err != nil {
			log.Println("推播失敗:", err)
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 404 || resp.StatusCode == 410 {
			a.DB.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, s["id"])
		}
	}
}

// onDispatch：派單後即時通知師傅（畫面開著 → 即時；沒開 → 推播）
func (a *App) onDispatch(driverID int64, o map[string]any) {
	msg := fmt.Sprintf("%v｜%v", o["customer_name"], o["address"])
	a.Events.Publish(driverID, Event{Type: "dispatch", Message: msg, Order: o})
	go a.SendPush(driverID, "新派單 "+fmt.Sprint(o["order_no"]), msg, fmt.Sprintf("/#/my/%v", o["id"]))
}
