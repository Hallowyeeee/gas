package app

import (
	"io/fs"
	"mime"
	"net/http"
	"strings"
)

func init() { mime.AddExtensionType(".webmanifest", "application/manifest+json") }

// Handler 回傳整個網站（API + 網頁）。
func (a *App) Handler() http.Handler {
	m := http.NewServeMux()
	if a.Events == nil {
		a.Events = &Hub{}
	}
	if a.OnDispatch == nil {
		a.OnDispatch = a.onDispatch
	}
	const (
		admin  = "admin"
		staff  = "staff"
		driver = "driver"
	)
	office := []string{admin, staff}

	m.HandleFunc("GET /api/setup", a.public(a.handleSetupStatus))
	m.HandleFunc("POST /api/setup", a.public(a.handleSetup))
	m.HandleFunc("POST /api/login", a.public(a.handleLogin))
	m.HandleFunc("POST /api/logout", a.route(a.handleLogout))
	m.HandleFunc("GET /api/me", a.route(a.handleMe))

	m.HandleFunc("GET /api/users", a.route(a.listUsers, admin))
	m.HandleFunc("POST /api/users", a.route(a.createUser, admin))
	m.HandleFunc("PUT /api/users/{id}", a.route(a.updateUser, admin))
	m.HandleFunc("GET /api/drivers", a.route(a.listDrivers, office...))

	m.HandleFunc("GET /api/customers", a.route(a.listCustomers, office...))
	m.HandleFunc("POST /api/customers", a.route(a.createCustomer, office...))
	m.HandleFunc("GET /api/customers/{id}", a.route(a.getCustomer, office...))
	m.HandleFunc("PUT /api/customers/{id}", a.route(a.updateCustomer, office...))
	m.HandleFunc("PUT /api/customers/{id}/prices", a.route(a.setCustomerPrice, office...))

	m.HandleFunc("GET /api/products", a.route(a.listProducts)) // 師傅回報押瓶時要選鋼瓶
	m.HandleFunc("POST /api/products", a.route(a.createProduct, office...))
	m.HandleFunc("PUT /api/products/{id}", a.route(a.updateProduct, office...))

	m.HandleFunc("GET /api/orders", a.route(a.listOrders))
	m.HandleFunc("POST /api/orders", a.route(a.createOrder, office...))
	m.HandleFunc("GET /api/orders/{id}", a.route(a.getOrder))
	m.HandleFunc("PUT /api/orders/{id}", a.route(a.updateOrder, office...))
	m.HandleFunc("POST /api/orders/{id}/dispatch", a.route(a.dispatchOrder, office...))
	m.HandleFunc("POST /api/orders/{id}/deliver", a.route(a.deliverOrder))
	m.HandleFunc("POST /api/orders/{id}/cancel", a.route(a.cancelOrder, office...))
	m.HandleFunc("POST /api/orders/{id}/undeliver", a.route(a.undeliverOrder, admin))

	m.HandleFunc("GET /api/payments", a.route(a.listPayments, office...))
	m.HandleFunc("POST /api/payments", a.route(a.createPayment, office...))
	m.HandleFunc("DELETE /api/payments/{id}", a.route(a.deletePayment, admin))
	m.HandleFunc("GET /api/handovers", a.route(a.listHandovers, office...))
	m.HandleFunc("POST /api/handovers", a.route(a.createHandover, office...))
	m.HandleFunc("DELETE /api/handovers/{id}", a.route(a.deleteHandover, admin))
	m.HandleFunc("GET /api/deposits", a.route(a.listDeposits, office...))
	m.HandleFunc("POST /api/deposits", a.route(a.createDeposit))

	m.HandleFunc("GET /api/suppliers", a.route(a.listSuppliers, office...))
	m.HandleFunc("POST /api/suppliers", a.route(a.createSupplier, office...))
	m.HandleFunc("PUT /api/suppliers/{id}", a.route(a.updateSupplier, office...))
	m.HandleFunc("GET /api/purchases", a.route(a.listPurchases, office...))
	m.HandleFunc("POST /api/purchases", a.route(a.createPurchase, office...))
	m.HandleFunc("GET /api/purchases/{id}", a.route(a.getPurchase, office...))
	m.HandleFunc("DELETE /api/purchases/{id}", a.route(a.deletePurchase, admin))
	m.HandleFunc("GET /api/supplier-payments", a.route(a.listSupplierPayments, office...))
	m.HandleFunc("POST /api/supplier-payments", a.route(a.createSupplierPayment, office...))

	m.HandleFunc("GET /api/stock", a.route(a.listStock, office...))
	m.HandleFunc("GET /api/stock/movements", a.route(a.listStockMovements, office...))
	m.HandleFunc("POST /api/stock/adjust", a.route(a.adjustStock, office...))
	m.HandleFunc("GET /api/refill-due", a.route(a.listRefillDue, office...))
	m.HandleFunc("POST /api/refill-logs", a.route(a.createRefillLog, office...))

	m.HandleFunc("GET /api/reports/{name}", a.route(a.report, office...))

	m.HandleFunc("GET /api/calls", a.route(a.listCalls, office...))
	m.HandleFunc("GET /api/calls/lookup", a.route(a.lookupCaller, office...))
	m.HandleFunc("POST /api/calls/test", a.route(a.testCall, office...))
	m.HandleFunc("GET /api/callerid", a.route(a.callerBoxStatus, office...))
	m.HandleFunc("POST /api/callerid/detect", a.route(a.callerBoxDetect, admin))
	m.HandleFunc("POST /api/callerid/use", a.route(a.callerBoxUse, admin))
	m.HandleFunc("GET /api/local/call", a.localCall)
	m.HandleFunc("POST /api/local/call", a.localCall)

	m.HandleFunc("GET /api/legacy/captures", a.route(a.listCaptures, office...))
	m.HandleFunc("GET /api/legacy/captures/{id}", a.route(a.getCapture, office...))
	m.HandleFunc("POST /api/legacy/captures/{id}/save", a.route(a.saveCapture, office...))
	m.HandleFunc("POST /api/legacy/captures/{id}/cancel", a.route(a.cancelCapture, office...))
	m.HandleFunc("POST /api/legacy/upload", a.route(a.uploadCapture, office...))
	m.HandleFunc("POST /api/local/capture", a.localCapture)
	m.HandleFunc("OPTIONS /api/local/capture", a.localCapture)

	m.HandleFunc("GET /api/settings", a.route(a.getSettings, office...))
	m.HandleFunc("PUT /api/settings", a.route(a.putSettings, admin))
	m.HandleFunc("GET /api/system", a.route(a.systemStatus, office...))
	m.HandleFunc("POST /api/system/backup", a.route(a.backupNow, admin))
	m.HandleFunc("POST /api/update/check", a.route(a.handleUpdateCheck, admin))
	m.HandleFunc("POST /api/update/apply", a.route(a.handleUpdateApply, admin))

	m.HandleFunc("POST /api/local/show", a.public(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if !isLocal(r) {
			return nil, forbidden()
		}
		if a.ShowWindow != nil {
			go a.ShowWindow()
		}
		return map[string]bool{"ok": true}, nil
	}))
	m.HandleFunc("POST /api/local/open-line", a.public(func(w http.ResponseWriter, r *http.Request) (any, error) {
		if !isLocal(r) {
			return nil, forbidden()
		}
		if a.OpenURL == nil {
			return map[string]bool{"ok": false}, nil
		}
		a.OpenURL("line://")
		return map[string]bool{"ok": true}, nil
	}))
	m.HandleFunc("GET /api/events", a.handleEvents)
	m.HandleFunc("GET /api/push/key", a.route(a.pushKey))
	m.HandleFunc("POST /api/push/subscribe", a.route(a.pushSubscribe))

	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "沒有這個功能"})
	})
	if a.Static != nil {
		files := http.FileServer(http.FS(a.Static))
		m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			// 單頁網頁：找不到的路徑都回 index.html
			p := strings.TrimPrefix(r.URL.Path, "/")
			if p == "" {
				p = "index.html"
			}
			if _, err := fs.Stat(a.Static, p); err != nil {
				r.URL.Path = "/"
			}
			w.Header().Set("Cache-Control", "no-cache")
			files.ServeHTTP(w, r)
		})
	}
	return securityHeaders(a.notifyChanges(m))
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		h.ServeHTTP(w, r)
	})
}
