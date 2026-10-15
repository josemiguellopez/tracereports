// Package demo powers `tracereports demo`: a small shop backend with a bug that can be switched
// on and off, and the sample data of a team that tests it from several languages (Python, JS,
// Java, Go, a REST API suite and a JUnit import). The data goes through the same REST API the
// clients use, so the demo shows the real product, FixLab included.
package demo

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Credentials of the shop (shown on its page and in the demo guide).
const (
	ShopUser     = "qa@demo.shop"
	ShopPassword = "demo"
)

// Bugs the shop can have; "orders" is on when the demo starts.
var Bugs = []struct{ Key, Title, Effect string }{
	{"orders", "Orders: connection pool exhausted", "POST /api/orders answers 500"},
	{"slow", "Products: slow database", "GET /api/products takes 3 s"},
	{"flaky", "Recommendations: flaky cache", "GET /api/recommendations fails every other call (503)"},
}

// Shop is the demo backend.
type Shop struct {
	mu     sync.Mutex
	bugs   map[string]bool
	tokens map[string]bool
	orders int
	recos  int // llamadas a /api/recommendations (el bug "flaky" falla las impares)
}

// NewShop returns the shop with the orders bug on.
func NewShop() *Shop {
	return &Shop{bugs: map[string]bool{"orders": true}, tokens: map[string]bool{}, orders: 1000}
}

// Reset puts the shop back as it starts: the orders bug on, the rest off, counters at zero.
func (s *Shop) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bugs = map[string]bool{"orders": true}
	s.recos = 0
}

// SetBug switches a bug on or off.
func (s *Shop) SetBug(key string, on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bugs[key] = on
}

// Bug reports whether a bug is on.
func (s *Shop) Bug(key string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.bugs[key]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

var products = []map[string]any{
	{"id": "P-1", "name": "Trail running shoes", "price": 89.9},
	{"id": "P-2", "name": "Waterproof jacket", "price": 129.0},
	{"id": "P-3", "name": "Insulated bottle", "price": 24.5},
}

func (s *Shop) authorized(r *http.Request) bool {
	tok := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.tokens[tok]
}

// ServeHTTP serves the shop API and its control page.
func (s *Shop) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Server", "demo-shop")
	if s.store(w, r) {
		return
	}
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		s.page(w)
	case r.URL.Path == "/__reset" && r.Method == http.MethodPost:
		s.Reset()
		writeJSON(w, http.StatusOK, map[string]bool{"reset": true})
	case r.URL.Path == "/__bug" && r.Method == http.MethodPost:
		_ = r.ParseForm()
		key := r.PostForm.Get("key")
		s.SetBug(key, r.PostForm.Get("on") == "1")
		http.Redirect(w, r, "/", http.StatusSeeOther)
	case r.URL.Path == "/api/login" && r.Method == http.MethodPost:
		var in struct{ Email, Password string }
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.Password != ShopPassword {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid credentials"})
			return
		}
		b := make([]byte, 16)
		_, _ = rand.Read(b)
		tok := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYUBkZW1vLnNob3AifQ." + hex.EncodeToString(b)
		s.mu.Lock()
		s.tokens[tok] = true
		s.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]any{"accessToken": tok, "user": map[string]string{"email": in.Email}})
	case r.URL.Path == "/api/products" && r.Method == http.MethodGet:
		if s.Bug("slow") {
			select {
			case <-time.After(3 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"products": products})
	case r.URL.Path == "/api/orders":
		if !s.authorized(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token expired"})
			return
		}
		if r.Method == http.MethodGet {
			writeJSON(w, http.StatusOK, map[string]any{"orders": []any{}})
			return
		}
		if s.Bug("orders") {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "payments: connection pool exhausted", "code": "DB_POOL_EXHAUSTED"})
			return
		}
		s.mu.Lock()
		s.orders++
		id := s.orders
		s.mu.Unlock()
		writeJSON(w, http.StatusCreated, map[string]any{"id": fmt.Sprintf("ORD-%d", id), "status": "confirmed"})
	case r.URL.Path == "/api/recommendations":
		s.mu.Lock()
		s.recos++
		fail := s.bugs["flaky"] && s.recos%2 == 1
		s.mu.Unlock()
		if fail {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "recommendations cache warming up"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": []string{"Insulated bottle", "Trail socks", "Running cap"}})
	case r.URL.Path == "/api/health":
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

// page is the control page: what the shop is and a switch for each bug.
func (s *Shop) page(w http.ResponseWriter) {
	var rows strings.Builder
	for _, b := range Bugs {
		on := s.Bug(b.Key)
		state, action, label := "fixed", "1", "Break it"
		if on {
			state, action, label = "BROKEN", "0", "Fix the backend"
		}
		fmt.Fprintf(&rows, `<tr><td><b>%s</b><br><small>%s</small></td><td class="%s">%s</td>
			<td><form method="post" action="/__bug"><input type="hidden" name="key" value="%s"><input type="hidden" name="on" value="%s">
			<button data-bug="%s">%s</button></form></td></tr>`,
			html.EscapeString(b.Title), html.EscapeString(b.Effect), strings.ToLower(state), state, b.Key, action, b.Key, label)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Demo Shop · TraceReports</title><style>
body{font-family:system-ui,sans-serif;max-width:760px;margin:40px auto;padding:0 16px;color:#1f2937}
h1{margin-bottom:4px}p{line-height:1.5}table{width:100%%;border-collapse:collapse;margin:20px 0}
td{padding:12px;border-bottom:1px solid #e5e7eb;vertical-align:middle}.broken{color:#dc2626;font-weight:700}.fixed{color:#16a34a;font-weight:700}
button{padding:8px 14px;border-radius:6px;border:1px solid #2563eb;background:#2563eb;color:#fff;font-weight:600;cursor:pointer}
code{background:#f3f4f6;padding:2px 6px;border-radius:4px}</style></head><body>
<h1>Demo Shop</h1><p>The backend the demo tests were run against. Break or fix it here, then go back to TraceReports
and use the <b>Lab</b> or <b>Verify</b> on the incident to see the failure reproduce or get fixed.</p>
<table>%s</table>
<p>Login of the demo: <code>%s</code> / password <code>%s</code></p></body></html>`, rows.String(), ShopUser, ShopPassword)
}
