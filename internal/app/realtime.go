package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"
)

// ---------------------------------------------------------------------
// 即時更新：畫面開著時，資料一變就通知（Server-Sent Events）
// ---------------------------------------------------------------------

type Event struct {
	Type    string         `json:"type"`              // "orders" 資料有變、"dispatch" 有新派單、"call" 來電、"unseen" 師傅還沒看派單
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
		Platform string `json:"platform"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(in.Endpoint, "https://") || in.Keys.P256dh == "" || in.Keys.Auth == "" {
		return nil, bad("通知訂閱資料不完整")
	}
	switch in.Platform {
	case "iphone", "samsung", "android":
	default:
		in.Platform = "other"
	}
	// 同一支手機重新訂閱：換了金鑰就清掉舊的失敗紀錄
	_, err := a.DB.ExecContext(r.Context(), `INSERT INTO push_subscriptions (user_id, endpoint, p256dh, auth, platform) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (endpoint) DO UPDATE SET user_id = excluded.user_id, p256dh = excluded.p256dh, auth = excluded.auth, platform = excluded.platform,
		  last_error = CASE WHEN push_subscriptions.p256dh = excluded.p256dh THEN push_subscriptions.last_error END`,
		u.ID, in.Endpoint, in.Keys.P256dh, in.Keys.Auth, in.Platform)
	return nil, err
}

// pushAck：手機收到通知時自己回報（不用登入，知道那支手機的通知網址才能回報）
func (a *App) pushAck(r *http.Request, u *User) (any, error) {
	var in struct {
		Endpoint string `json:"endpoint"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	res, err := a.DB.ExecContext(r.Context(), `UPDATE push_subscriptions SET last_ack_at = datetime('now','localtime'), last_error = NULL WHERE endpoint = ?`, in.Endpoint)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		a.Events.Publish(0, Event{Type: "orders"}) // 電腦上的通知燈號跟著變
	}
	return map[string]bool{"ok": true}, nil
}

// pushTest：師傅自己測，或店裡對某位師傅送一次測試通知
func (a *App) pushTest(r *http.Request, u *User) (any, error) {
	var in struct {
		DriverID int64 `json:"driver_id"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	to := u.ID
	if !u.IsDriver() && in.DriverID > 0 {
		to = in.DriverID
	}
	n := a.SendPush(to, "🔔 測試通知", "收到這則就代表派單通知會響，不用回覆。", "/#/my")
	if n == 0 {
		if to == u.ID {
			return nil, bad("這支手機還沒開通知，請先按「開啟通知」")
		}
		return nil, bad("這位師傅還沒開通知，請他在手機上按「開啟通知」")
	}
	return map[string]int{"sent": n}, nil
}

// pushStatus：每位師傅的通知狀態燈號
//
//	ok   綠燈：最近 7 天手機有回報收到，而且最後一次送出的有收到
//	stale 黃燈：有開通知，但很久沒確認收到
//	bad  紅燈：沒開通知、送出失敗，或送出 2 分鐘了手機還沒回報收到
func (a *App) pushStatus(r *http.Request, u *User) (any, error) {
	drivers, err := rows(r.Context(), a.DB, `
		SELECT u.id, u.name,
		  (SELECT COUNT(*) FROM push_subscriptions s WHERE s.user_id = u.id) AS phones,
		  (SELECT MAX(last_ack_at) FROM push_subscriptions s WHERE s.user_id = u.id) AS last_ack_at,
		  (SELECT MAX(last_sent_at) FROM push_subscriptions s WHERE s.user_id = u.id) AS last_sent_at,
		  (SELECT group_concat(DISTINCT platform) FROM push_subscriptions s WHERE s.user_id = u.id) AS platforms,
		  (SELECT last_error FROM push_subscriptions s WHERE s.user_id = u.id AND last_error IS NOT NULL ORDER BY last_sent_at DESC LIMIT 1) AS last_error,
		  CASE
		    WHEN NOT EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.user_id = u.id) THEN 'bad'
		    -- 任何一支手機：最後一次送出的有收到、而且 7 天內 → 綠燈
		    WHEN EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.user_id = u.id AND last_ack_at IS NOT NULL
		         AND last_ack_at >= COALESCE(last_sent_at, '') AND last_ack_at >= datetime('now','localtime','-7 days')) THEN 'ok'
		    -- 送出 2 分鐘了，沒有一支手機回報收到 → 紅燈
		    WHEN NOT EXISTS (SELECT 1 FROM push_subscriptions s WHERE s.user_id = u.id
		         AND (last_sent_at IS NULL OR (last_sent_at > datetime('now','localtime','-2 minutes') AND last_error IS NULL) OR last_ack_at >= last_sent_at)) THEN 'bad'
		    ELSE 'stale'
		  END AS status
		FROM users u WHERE u.role = 'driver' AND u.active = 1 ORDER BY u.name`)
	if err != nil {
		return nil, err
	}
	return map[string]any{"drivers": drivers, "remind_minutes": a.remindMinutes()}, nil
}

// SendPush 發推播給某位使用者的所有手機，回傳送出幾支。
func (a *App) SendPush(userID int64, title, body, url string) int {
	pub, priv, err := a.vapidKeys()
	if err != nil {
		log.Println("推播金鑰錯誤:", err)
		return 0
	}
	list, err := rows(nilCtx(), a.DB, `SELECT id, endpoint, p256dh, auth FROM push_subscriptions WHERE user_id = ?`, userID)
	if err != nil {
		return 0
	}
	payload, _ := json.Marshal(map[string]string{"title": title, "body": body, "url": url})
	sent := 0
	for _, s := range list {
		id := s["id"]
		sub := &webpush.Subscription{Endpoint: s["endpoint"].(string), Keys: webpush.Keys{P256dh: s["p256dh"].(string), Auth: s["auth"].(string)}}
		a.DB.Exec(`UPDATE push_subscriptions SET last_sent_at = datetime('now','localtime') WHERE id = ?`, id)
		sent++
		resp, err := sendWebPush(payload, sub, &webpush.Options{
			Subscriber: "mailto:gas-shop@example.invalid", VAPIDPublicKey: pub, VAPIDPrivateKey: priv, TTL: 3600, Urgency: webpush.UrgencyHigh,
		})
		if err != nil {
			log.Println("推播失敗:", err)
			a.DB.Exec(`UPDATE push_subscriptions SET last_error = ? WHERE id = ?`, "網路送不出去："+err.Error(), id)
			continue
		}
		resp.Body.Close()
		switch {
		case resp.StatusCode == 404 || resp.StatusCode == 410:
			// 手機那邊已經取消（換手機、清資料、關掉通知），這支不能用了
			a.DB.Exec(`DELETE FROM push_subscriptions WHERE id = ?`, id)
			sent--
		case resp.StatusCode >= 300:
			a.DB.Exec(`UPDATE push_subscriptions SET last_error = ? WHERE id = ?`, fmt.Sprintf("通知伺服器拒絕（%d）", resp.StatusCode), id)
		}
	}
	return sent
}

// sendWebPush 測試時可以換掉，不真的送出去。
var sendWebPush = webpush.SendNotification

// onDispatch：派單後即時通知師傅（畫面開著 → 即時；沒開 → 推播）
func (a *App) onDispatch(driverID int64, o map[string]any) {
	msg := fmt.Sprintf("%v｜%v", o["customer_name"], o["address"])
	a.Events.Publish(driverID, Event{Type: "dispatch", Message: msg, Order: o})
	go a.SendPush(driverID, "新派單 "+fmt.Sprint(o["order_no"]), msg, fmt.Sprintf("/#/my/%v", o["id"]))
}

// ---------------------------------------------------------------------
// 派單已讀與沒看到再提醒
// ---------------------------------------------------------------------

// markSeen：師傅打開我的單或點開通知，記下看到的時間
func (a *App) markSeen(r *http.Request, u *User) (any, error) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if err := readJSON(r, &in); err != nil {
		return nil, err
	}
	n := int64(0)
	for _, id := range in.IDs {
		res, err := a.DB.ExecContext(r.Context(), `UPDATE orders SET seen_at = datetime('now','localtime')
			WHERE id = ? AND driver_id = ? AND status = 'dispatched' AND seen_at IS NULL`, id, u.ID)
		if err != nil {
			return nil, err
		}
		k, _ := res.RowsAffected()
		n += k
	}
	return map[string]int64{"seen": n}, nil
}

// remindMinutes：派單幾分鐘師傅還沒看就提醒（設定 remind_minutes，預設 3，0 = 不提醒）
func (a *App) remindMinutes() int {
	var v string
	a.DB.QueryRow(`SELECT value FROM settings WHERE key = 'remind_minutes'`).Scan(&v)
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil || n < 0 {
		return 3
	}
	return n
}

// RemindUnseen 找出派了超過幾分鐘還沒看的單：師傅手機再響一次，電腦畫面重畫（變紅）。回傳提醒幾張。
func (a *App) RemindUnseen() int {
	m := a.remindMinutes()
	if m == 0 {
		return 0
	}
	list, err := rows(nilCtx(), a.DB, `SELECT o.id, o.order_no, o.driver_id, o.address, c.name AS customer_name, u.name AS driver_name
		FROM orders o JOIN customers c ON c.id = o.customer_id LEFT JOIN users u ON u.id = o.driver_id
		WHERE o.status = 'dispatched' AND o.seen_at IS NULL AND o.reminded_at IS NULL AND o.driver_id IS NOT NULL
		  AND o.dispatched_at <= datetime('now','localtime', ?)`, fmt.Sprintf("-%d minutes", m))
	if err != nil {
		return 0
	}
	for _, o := range list {
		a.DB.Exec(`UPDATE orders SET reminded_at = datetime('now','localtime') WHERE id = ?`, o["id"])
		d, _ := o["driver_id"].(int64)
		msg := fmt.Sprintf("%v｜%v", o["customer_name"], o["address"])
		a.Events.Publish(d, Event{Type: "dispatch", Message: "（還沒看）" + msg, Order: o})
		go a.SendPush(d, "⚠ 還沒看的派單 "+fmt.Sprint(o["order_no"]), msg, fmt.Sprintf("/#/my/%v", o["id"]))
		a.Events.Publish(0, Event{Type: "unseen", Message: fmt.Sprintf("%v 派單 %d 分鐘了還沒看：%s（已再響一次，可按「傳 LINE 補送」）", o["driver_name"], m, msg), Order: o})
	}
	if len(list) > 0 {
		a.Events.Publish(0, Event{Type: "orders"})
	}
	return len(list)
}

// RunReminders 每 20 秒檢查一次有沒有師傅還沒看的派單。
func (a *App) RunReminders(ctx context.Context) {
	t := time.NewTicker(20 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			a.RemindUnseen()
		}
	}
}
