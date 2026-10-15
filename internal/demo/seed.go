package demo

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Summary is what Seed loaded, for the guide.
type Summary struct {
	Runs     int
	Nights   int // noches de historia
	Tests    int
	Failed   int
	Showcase int64 // la ejecución con más que ver (Playwright Python, checkout)
	APIRun   int64 // la suite de API (pytest + requests)
}

// seeder loads the sample data through the REST API, like a client would.
type seeder struct {
	ctx    context.Context
	base   string // TraceReports, p. ej. http://127.0.0.1:8080
	shop   string // la tienda, p. ej. http://127.0.0.1:8081
	client *http.Client
	sum    Summary
}

// Seed loads the demo data into the TraceReports server at base; the captured calls point to the
// shop at shopURL, so the Lab can resend them.
func Seed(ctx context.Context, base, shopURL string, now time.Time) (*Summary, error) {
	s := &seeder{ctx: ctx, base: strings.TrimRight(base, "/"), shop: strings.TrimRight(shopURL, "/"), client: &http.Client{Timeout: 30 * time.Second},
		sum: Summary{Nights: historyNights}}
	if err := s.all(now); err != nil {
		return nil, err
	}
	return &s.sum, nil
}

func (s *seeder) do(method, path string, at time.Time, body io.Reader, ctype string) (map[string]any, error) {
	req, err := http.NewRequestWithContext(s.ctx, method, s.base+path, body)
	if err != nil {
		return nil, err
	}
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	// la hora real del evento: la historia de la demo abarca varios días
	req.Header.Set("X-TraceReports-Timestamp", strconv.FormatInt(at.UnixMilli(), 10))
	res, err := s.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %s %s", method, path, res.Status, strings.TrimSpace(string(data)))
	}
	out := map[string]any{}
	_ = json.Unmarshal(data, &out)
	return out, nil
}

func (s *seeder) send(method, path string, at time.Time, v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return s.do(method, path, at, bytes.NewReader(b), "application/json")
}

func (s *seeder) screenshot(testID int64, at time.Time, message, status, kind string) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	f, _ := w.CreateFormFile("file", kind+".png")
	_, _ = f.Write(screen(kind))
	_ = w.WriteField("message", message)
	_ = w.WriteField("status", status)
	_ = w.WriteField("timestamp", strconv.FormatInt(at.UnixMilli(), 10))
	_ = w.Close()
	_, err := s.do(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/screenshot", testID), at, &buf, w.FormDataContentType())
	return err
}

// ---------- el modelo de los datos ----------

type step struct {
	Status, Message string
	Shot            string // tipo de captura (ver screen); "" = sin captura
}

type testSpec struct {
	Name, Category, Key, Suite, Params string
	Status                             string // PASS | FAIL | SKIP
	Error, Trace                       string
	Attempts                           int
	Steps                              []step
	// Network: cada llamada va en el paso de su índice (antes del fallo: así es la causa)
	Network map[int][]map[string]any
	Console []map[string]any
	DOM     map[string]any
}

type runSpec struct {
	Name, Environment, Project, Branch, Commit, Framework string
	At                                                    time.Time
	Tests                                                 []testSpec
	Showcase, API                                         bool
}

func (s *seeder) run(r runSpec) error {
	res, err := s.send(http.MethodPost, "/api/v1/runs", r.At, map[string]any{"name": r.Name, "environment": r.Environment,
		"project": r.Project, "branch": r.Branch, "commit": r.Commit, "framework": r.Framework})
	if err != nil {
		return err
	}
	runID := int64(res["run_id"].(float64))
	t := r.At.Add(2 * time.Second)
	for _, ts := range r.Tests {
		if t, err = s.test(runID, ts, t); err != nil {
			return err
		}
	}
	if _, err := s.send(http.MethodPatch, fmt.Sprintf("/api/v1/runs/%d/finish", runID), t.Add(time.Second), map[string]any{}); err != nil {
		return err
	}
	s.sum.Runs++
	if r.Showcase {
		s.sum.Showcase = runID
	}
	if r.API {
		s.sum.APIRun = runID
	}
	return nil
}

const stepGap = 700 * time.Millisecond

func (s *seeder) test(runID int64, ts testSpec, t0 time.Time) (time.Time, error) {
	res, err := s.send(http.MethodPost, fmt.Sprintf("/api/v1/runs/%d/tests", runID), t0, map[string]any{
		"name": ts.Name, "category": ts.Category, "key": ts.Key, "suite": ts.Suite, "params": ts.Params})
	if err != nil {
		return t0, err
	}
	id := int64(res["test_id"].(float64))
	t := t0
	for i, st := range ts.Steps {
		t = t0.Add(time.Duration(i+1) * stepGap)
		for j, c := range ts.Network[i] {
			c["started_at"] = t.Add(-stepGap + time.Duration(j+1)*90*time.Millisecond).UnixMilli()
		}
		if conns := ts.Network[i]; len(conns) > 0 {
			if _, err := s.send(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/network", id), t, map[string]any{"connections": conns}); err != nil {
				return t, err
			}
		}
		if st.Shot != "" {
			if err := s.screenshot(id, t, st.Message, st.Status, st.Shot); err != nil {
				return t, err
			}
		} else if _, err := s.send(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/logs", id), t, map[string]any{"status": st.Status, "message": st.Message, "timestamp": t.UnixMilli()}); err != nil {
			return t, err
		}
	}
	if len(ts.Console) > 0 {
		for i := range ts.Console {
			ts.Console[i]["timestamp"] = t.Add(-200 * time.Millisecond).UnixMilli()
		}
		if _, err := s.send(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/console", id), t, map[string]any{"entries": ts.Console}); err != nil {
			return t, err
		}
	}
	if ts.DOM != nil {
		if _, err := s.send(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/dom", id), t, ts.DOM); err != nil {
			return t, err
		}
	}
	end := t.Add(400 * time.Millisecond)
	attempts := max(ts.Attempts, 1)
	if _, err := s.send(http.MethodPatch, fmt.Sprintf("/api/v1/tests/%d/finish", id), end, map[string]any{
		"status": ts.Status, "error_message": ts.Error, "error_trace": ts.Trace, "attempts": attempts}); err != nil {
		return end, err
	}
	s.sum.Tests++
	if ts.Status == "FAIL" {
		s.sum.Failed++
	}
	return end.Add(300 * time.Millisecond), nil
}

// ---------- llamadas a la tienda ----------

func (s *seeder) hdr(extra ...string) map[string]string {
	h := map[string]string{"content-type": "application/json", "date": "Wed, 14 Oct 2026 12:00:00 GMT", "server": "demo-shop"}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = extra[i+1]
	}
	return h
}

const oldToken = "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYUBkZW1vLnNob3AifQ.recorded-session-0001"

func (s *seeder) page(path string) map[string]any {
	return map[string]any{"method": "GET", "url": s.shop + path, "status": 200, "resource_type": "document", "mime_type": "text/html",
		"duration_ms": 120, "response_headers": s.hdr("content-type", "text/html; charset=utf-8")}
}

func (s *seeder) asset(path, kind, mime string, size int) map[string]any {
	return map[string]any{"method": "GET", "url": s.shop + path, "status": 200, "resource_type": kind, "mime_type": mime,
		"duration_ms": 35, "body_size": size, "response_headers": s.hdr("content-type", mime)}
}

func (s *seeder) login(ok bool) map[string]any {
	c := map[string]any{"method": "POST", "url": s.shop + "/api/login", "resource_type": "fetch", "mime_type": "application/json", "duration_ms": 84,
		"request_headers": map[string]string{"content-type": "application/json", "accept": "application/json"},
		"post_data":       fmt.Sprintf(`{"email":%q,"password":%q}`, ShopUser, ShopPassword), "response_headers": s.hdr()}
	if ok {
		c["status"], c["response_body"] = 200, fmt.Sprintf(`{"accessToken":%q,"user":{"email":%q}}`, oldToken, ShopUser)
	} else {
		c["status"], c["response_body"] = 401, `{"error":"invalid credentials"}`
		c["expected"] = true
	}
	return c
}

func (s *seeder) products(ms int) map[string]any {
	return map[string]any{"method": "GET", "url": s.shop + "/api/products", "status": 200, "resource_type": "fetch", "mime_type": "application/json",
		"duration_ms": ms, "request_headers": map[string]string{"accept": "application/json"}, "response_headers": s.hdr(),
		"response_body": `{"products":[{"id":"P-1","name":"Trail running shoes","price":89.9},{"id":"P-2","name":"Waterproof jacket","price":129.0}]}`}
}

func (s *seeder) order(ok bool, requestID string) map[string]any {
	c := map[string]any{"method": "POST", "url": s.shop + "/api/orders", "resource_type": "fetch", "mime_type": "application/json",
		"request_headers": map[string]string{"content-type": "application/json", "authorization": "Bearer " + oldToken, "x-request-id": requestID},
		"post_data":       `{"items":[{"id":"P-1","qty":1}],"payment":{"method":"card","last4":"4242"}}`}
	if ok {
		c["status"], c["duration_ms"], c["response_headers"] = 201, 230, s.hdr("x-request-id", requestID)
		c["response_body"] = `{"id":"ORD-1001","status":"confirmed"}`
	} else {
		c["status"], c["status_text"], c["duration_ms"], c["response_headers"] = 500, "Internal Server Error", 1840, s.hdr("x-request-id", requestID)
		c["response_body"] = `{"error":"payments: connection pool exhausted","code":"DB_POOL_EXHAUSTED"}`
	}
	return c
}

func (s *seeder) refundForbidden() map[string]any {
	return map[string]any{"method": "POST", "url": s.shop + "/api/orders/ORD-1001/refund", "status": 403, "resource_type": "fetch",
		"mime_type": "application/json", "duration_ms": 41, "expected": true, "response_headers": s.hdr(),
		"request_headers": map[string]string{"authorization": "Bearer " + oldToken}, "response_body": `{"error":"admin role required"}`}
}

// ---------- el equipo de la tienda y sus suites ----------

// commit is a stable, real-looking Git commit for the build of each night.
func commit(day int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("demo-shop build %d", day)))
	return hex.EncodeToString(sum[:])
}

func (s *seeder) all(now time.Time) error {
	// todo relativo a ahora (nunca en el futuro): "hoy" son los últimos minutos, cada noche
	// anterior un día antes; min es la separación entre las suites de una misma noche
	day := func(d, min int) time.Time {
		return now.AddDate(0, 0, -d).Add(-time.Duration(60-min) * time.Minute)
	}
	// historia: 12 noches del checkout (Python y JS) para tendencias, flaky y "última vez que pasó"
	for d := historyNights; d >= 1; d-- {
		if err := s.run(s.pythonE2E(day(d, 10), d, false)); err != nil {
			return err
		}
		if err := s.run(s.playwrightJS(day(d, 30), d, false)); err != nil {
			return err
		}
	}
	// hoy: el backend de pedidos falla y cada suite lo ve a su manera
	today := []runSpec{
		s.junitImport(day(0, 0)),
		s.goAPI(day(0, 8)),
		s.seleniumJava(day(0, 16)),
		s.playwrightJS(day(0, 24), 0, true),
		s.pythonAPI(day(0, 32)),
		s.pythonE2E(day(0, 40), 0, true), // la última: se abre sola en el reporte
	}
	for _, r := range today {
		if r.Framework == "junit-import" {
			if err := s.importJUnit(r); err != nil {
				return err
			}
			continue
		}
		if err := s.run(r); err != nil {
			return err
		}
	}
	return nil
}

const project = "demo-shop"

// historyNights is how many earlier nights the demo has (Metrics, flaky, history).
const historyNights = 12

func (s *seeder) pythonE2E(at time.Time, d int, broken bool) runSpec {
	r := runSpec{Name: "Checkout · Playwright (Python)", Environment: "staging", Project: project, Branch: "main", Commit: commit(d),
		Framework: "pytest", At: at, Showcase: d == 0}
	login := testSpec{Name: "test_login_shows_dashboard", Category: "auth, smoke", Key: "tests/e2e/test_auth.py::test_login_shows_dashboard[chromium]",
		Suite: "tests/e2e/test_auth.py", Params: "chromium", Status: "PASS",
		Steps: []step{{"INFO", "Open the login page", "login"}, {"PASS", "Sign in as " + ShopUser, ""}, {"PASS", "The dashboard shows the user's name", "dashboard"}},
		Network: map[int][]map[string]any{0: {s.page("/login"), s.asset("/static/app.js", "script", "application/javascript", 182_311), s.asset("/static/app.css", "stylesheet", "text/css", 24_120)},
			1: {s.login(true)}}}
	search := testSpec{Name: "test_search_finds_products", Category: "catalog", Key: "tests/e2e/test_catalog.py::test_search_finds_products[chromium]",
		Suite: "tests/e2e/test_catalog.py", Params: "chromium", Status: "PASS",
		Steps:   []step{{"INFO", "Search for \"jacket\"", ""}, {"PASS", "2 products found", "catalog"}},
		Network: map[int][]map[string]any{0: {s.products(140)}}}
	checkout := testSpec{Name: "test_checkout_pays_order", Category: "checkout, smoke", Key: "tests/e2e/test_checkout.py::test_checkout_pays_order[chromium]",
		Suite: "tests/e2e/test_checkout.py", Params: "chromium", Status: "PASS",
		Steps:   []step{{"INFO", "Sign in and open the cart", "cart"}, {"PASS", "Pay with card ending 4242", ""}, {"PASS", "Order confirmed", "confirmed"}},
		Network: map[int][]map[string]any{0: {s.login(true), s.products(120)}, 1: {s.order(true, fmt.Sprintf("req-%d-ok", d))}}}
	cart := testSpec{Name: "test_add_to_cart", Category: "cart", Key: "tests/e2e/test_cart.py::test_add_to_cart[chromium]",
		Suite: "tests/e2e/test_cart.py", Params: "chromium", Status: "PASS",
		Steps: []step{{"INFO", "Open product P-1", ""}, {"PASS", "Add to cart", ""}, {"PASS", "The cart shows 1 item", "cart"}}}
	if d == 3 || d == 9 {
		// una noche vieja falló la búsqueda (ya arreglada): historia en Métricas
		search.Status, search.Error = "FAIL", "AssertionError: expected 2 products for \"jacket\", got 0"
		search.Steps[1] = step{"FAIL", "0 products found (the index was being rebuilt)", "catalog"}
	}
	if broken {
		checkout.Status = "FAIL"
		checkout.Error = "playwright._impl._errors.TimeoutError: Locator.wait_for: Timeout 5000ms exceeded.\nCall log:\n  - waiting for get_by_text(\"Order confirmed\") to be visible"
		checkout.Trace = "tests/e2e/test_checkout.py:41: in test_checkout_pays_order\n    page.get_by_text(\"Order confirmed\").wait_for(timeout=5000)\nplaywright._impl._errors.TimeoutError: Locator.wait_for: Timeout 5000ms exceeded."
		checkout.Steps = []step{{"INFO", "Sign in and open the cart", "cart"}, {"PASS", "Pay with card ending 4242", ""}, {"FAIL", "The confirmation never appeared: the page shows \"Something went wrong\"", "error"}}
		checkout.Network = map[int][]map[string]any{0: {s.login(true), s.products(120)}, 1: {s.order(false, "req-7f3a9c21")}}
		checkout.Console = []map[string]any{
			{"level": "error", "text": "Failed to load resource: the server responded with a status of 500 (Internal Server Error)", "location": s.shop + "/api/orders"},
			{"level": "pageerror", "text": "TypeError: Cannot read properties of undefined (reading 'id')\n    at renderConfirmation (checkout.js:42:18)"},
		}
		// un selector que cambió: el sugeridor propone el nuevo
		cart.Status = "FAIL"
		cart.Error = "playwright._impl._errors.TimeoutError: Locator.click: Timeout 5000ms exceeded.\nCall log:\n  - waiting for locator(\"#add-to-cart-btn\")"
		cart.Steps = []step{{"INFO", "Open product P-1", "product"}, {"FAIL", "The \"Add to cart\" button was not found", "product"}}
		cart.DOM = map[string]any{"url": s.shop + "/products/P-1", "title": "Trail running shoes · Demo Shop", "viewport": map[string]int{"w": 1280, "h": 720},
			"elements": []map[string]any{
				{"tag": "h1", "text": "Trail running shoes", "x": 80, "y": 120, "w": 520, "h": 40, "visible": true},
				{"tag": "button", "testid": "add-to-cart", "testid_attr": "data-testid", "text": "Add to cart", "type": "button", "x": 80, "y": 420, "w": 200, "h": 44, "visible": true},
				{"tag": "button", "id": "wishlist", "text": "Save for later", "type": "button", "x": 300, "y": 420, "w": 180, "h": 44, "visible": true},
				{"tag": "a", "text": "Back to products", "x": 80, "y": 80, "w": 140, "h": 20, "visible": true},
			}}
	}
	r.Tests = []testSpec{login, search, cart, checkout}
	return r
}

func (s *seeder) playwrightJS(at time.Time, d int, broken bool) runSpec {
	r := runSpec{Name: "Checkout · Playwright (JS)", Environment: "staging", Project: project, Branch: "main", Commit: commit(d), Framework: "playwright", At: at}
	pay := testSpec{Name: "pays with card", Category: "checkout", Key: "tests/checkout.spec.js > Checkout > pays with card [chromium]",
		Suite: "tests/checkout.spec.js", Params: "chromium", Status: "PASS",
		Steps:   []step{{"INFO", "goto /cart", "cart"}, {"PASS", "fill card 4242 4242 4242 4242", ""}, {"PASS", "expect(\"Order confirmed\").toBeVisible()", "confirmed"}},
		Network: map[int][]map[string]any{0: {s.login(true)}, 1: {s.order(true, fmt.Sprintf("req-js-%d", d))}}}
	discount := testSpec{Name: "applies a discount code", Category: "checkout", Key: "tests/checkout.spec.js > Checkout > applies a discount code [chromium]",
		Suite: "tests/checkout.spec.js", Params: "chromium", Status: "PASS",
		Steps: []step{{"INFO", "fill coupon DEMO10", ""}, {"PASS", "total shows -10%", ""}}}
	history := testSpec{Name: "shows the order history", Category: "account", Key: "tests/account.spec.js > Account > shows the order history [chromium]",
		Suite: "tests/account.spec.js", Params: "chromium", Status: "PASS", Steps: []step{{"PASS", "3 orders listed", ""}}}
	if d == 2 || d == 7 || d == 0 {
		// pasó en el reintento: flaky
		discount.Attempts = 2
		discount.Steps = append([]step{{"WARNING", "attempt 1: total did not update in time, retrying", ""}}, discount.Steps...)
	}
	if broken {
		pay.Status = "FAIL"
		pay.Error = "Error: expect(locator).toBeVisible() failed\n\nLocator: getByText('Order confirmed')\nExpected: visible\nTimeout: 5000ms"
		pay.Trace = "    at /repo/tests/checkout.spec.js:27:45"
		pay.Steps = []step{{"INFO", "goto /cart", "cart"}, {"PASS", "fill card 4242 4242 4242 4242", ""}, {"FAIL", "expect(\"Order confirmed\").toBeVisible() timed out", "error"}}
		pay.Network = map[int][]map[string]any{0: {s.login(true)}, 1: {s.order(false, "req-2b81d044")}}
	}
	r.Tests = []testSpec{pay, discount, history}
	return r
}

func (s *seeder) pythonAPI(at time.Time) runSpec {
	return runSpec{Name: "Orders API · pytest + requests", Environment: "staging", Project: project, Branch: "main", Commit: commit(0),
		Framework: "pytest", At: at, API: true, Tests: []testSpec{
			{Name: "test_login_returns_token", Category: "api, auth", Key: "tests/api/test_auth.py::test_login_returns_token", Suite: "tests/api/test_auth.py",
				Status: "PASS", Steps: []step{{"PASS", "POST /api/login → 200 with accessToken", ""}}, Network: map[int][]map[string]any{0: {s.login(true)}}},
			{Name: "test_wrong_password_is_rejected", Category: "api, auth", Key: "tests/api/test_auth.py::test_wrong_password_is_rejected", Suite: "tests/api/test_auth.py",
				Status: "PASS", Steps: []step{{"PASS", "POST /api/login with a wrong password → 401 (expected)", ""}}, Network: map[int][]map[string]any{0: {s.login(false)}}},
			{Name: "test_list_products", Category: "api, catalog", Key: "tests/api/test_catalog.py::test_list_products", Suite: "tests/api/test_catalog.py",
				Status: "PASS", Steps: []step{{"PASS", "GET /api/products → 2 products", ""}}, Network: map[int][]map[string]any{0: {s.products(95)}}},
			{Name: "test_create_order", Category: "api, orders", Key: "tests/api/test_orders.py::test_create_order", Suite: "tests/api/test_orders.py",
				Status: "FAIL", Error: "AssertionError: expected status 201, got 500\nassert 500 == 201",
				Trace:   "tests/api/test_orders.py:18: in test_create_order\n    assert res.status_code == 201, res.text\nAssertionError: expected status 201, got 500",
				Steps:   []step{{"PASS", "POST /api/login → token", ""}, {"FAIL", "POST /api/orders → 500 {\"error\":\"payments: connection pool exhausted\"}", ""}},
				Network: map[int][]map[string]any{0: {s.login(true)}, 1: {s.order(false, "req-api-91c2")}}},
			{Name: "test_refund_requires_admin", Category: "api, orders", Key: "tests/api/test_orders.py::test_refund_requires_admin", Suite: "tests/api/test_orders.py",
				Status: "PASS", Steps: []step{{"PASS", "POST /api/orders/ORD-1001/refund as QA → 403 (expected)", ""}}, Network: map[int][]map[string]any{0: {s.refundForbidden()}}},
		}}
}

func (s *seeder) seleniumJava(at time.Time) runSpec {
	return runSpec{Name: "Checkout · Selenium (Java, JUnit 5)", Environment: "staging", Project: project, Branch: "main", Commit: commit(0),
		Framework: "junit5", At: at, Tests: []testSpec{
			{Name: "loginShowsDashboard", Category: "auth", Key: "com.demoshop.AuthTest#loginShowsDashboard", Suite: "com.demoshop.AuthTest", Status: "PASS",
				Steps: []step{{"INFO", "driver.get(\"/login\")", "login"}, {"PASS", "Dashboard visible", "dashboard"}}},
			{Name: "paysWithCard", Category: "checkout", Key: "com.demoshop.CheckoutTest#paysWithCard", Suite: "com.demoshop.CheckoutTest", Status: "FAIL",
				Error:   "org.openqa.selenium.TimeoutException: Expected condition failed: waiting for visibility of element located by By.cssSelector: .order-confirmed (tried for 10 second(s))",
				Trace:   "\tat com.demoshop.CheckoutTest.paysWithCard(CheckoutTest.java:57)",
				Steps:   []step{{"INFO", "Open the cart", "cart"}, {"FAIL", "The order was not confirmed", "error"}},
				Console: []map[string]any{{"level": "error", "text": "POST " + s.shop + "/api/orders 500 (Internal Server Error)"}}},
			{Name: "invalidCouponShowsError", Category: "checkout", Key: "com.demoshop.CheckoutTest#invalidCouponShowsError", Suite: "com.demoshop.CheckoutTest", Status: "FAIL",
				Error: `org.openqa.selenium.NoSuchElementException: no such element: Unable to locate element: {"method":"css selector","selector":"#coupon-error"}`,
				Trace: "\tat com.demoshop.CheckoutTest.invalidCouponShowsError(CheckoutTest.java:83)",
				Steps: []step{{"INFO", "Apply coupon NOPE", "cart"}, {"FAIL", "The coupon error message was not found", "cart"}},
				DOM: map[string]any{"url": s.shop + "/cart", "title": "Cart · Demo Shop", "viewport": map[string]int{"w": 1280, "h": 720}, "elements": []map[string]any{
					{"tag": "input", "name": "coupon", "placeholder": "Discount code", "x": 80, "y": 300, "w": 240, "h": 36, "visible": true},
					{"tag": "div", "role": "alert", "testid": "coupon-message", "testid_attr": "data-testid", "text": "Coupon NOPE is not valid", "x": 80, "y": 344, "w": 320, "h": 24, "visible": true},
					{"tag": "button", "text": "Apply", "type": "submit", "x": 330, "y": 300, "w": 90, "h": 36, "visible": true},
				}}},
			{Name: "guestCanBrowseCatalog", Category: "catalog", Key: "com.demoshop.CatalogTest#guestCanBrowseCatalog", Suite: "com.demoshop.CatalogTest", Status: "PASS",
				Steps: []step{{"PASS", "3 products visible", "catalog"}}},
		}}
}

func (s *seeder) goAPI(at time.Time) runSpec {
	return runSpec{Name: "Orders API · Go", Environment: "staging", Project: project, Branch: "main", Commit: commit(0), Framework: "go", At: at, Tests: []testSpec{
		{Name: "TestHealth", Category: "api", Key: "demoshop/api/TestHealth", Suite: "demoshop/api", Status: "PASS", Steps: []step{{"PASS", "GET /api/health → ok", ""}}},
		{Name: "TestProducts/list", Category: "api", Key: "demoshop/api/TestProducts/list", Suite: "demoshop/api", Status: "PASS",
			Steps: []step{{"PASS", "GET /api/products → 200", ""}}, Network: map[int][]map[string]any{0: {s.products(88)}}},
		{Name: "TestOrders/create", Category: "api, orders", Key: "demoshop/api/TestOrders/create", Suite: "demoshop/api", Status: "FAIL",
			Error: "orders_test.go:34: POST /api/orders: got status 500, want 201", Trace: "orders_test.go:34: POST /api/orders: got status 500, want 201",
			Steps:   []step{{"PASS", "login → token", ""}, {"FAIL", "POST /api/orders → 500", ""}},
			Network: map[int][]map[string]any{0: {s.login(true)}, 1: {s.order(false, "req-go-55e0")}}},
		{Name: "TestOrders/list", Category: "api, orders", Key: "demoshop/api/TestOrders/list", Suite: "demoshop/api", Status: "SKIP",
			Steps: []step{{"SKIP", "skipped: depends on TestOrders/create", ""}}},
	}}
}

// junitImport is a run imported from a Maven Surefire JUnit XML file (no client, just the report).
func (s *seeder) junitImport(at time.Time) runSpec {
	return runSpec{Name: "Nightly · Maven (JUnit XML import)", Environment: "staging", Project: project, Branch: "main", Commit: commit(0),
		Framework: "junit-import", At: at}
}

const surefireXML = `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="com.demoshop.InventoryServiceTest" tests="4" failures="1" errors="0" skipped="1" time="1.84" timestamp="%s">
  <testcase classname="com.demoshop.InventoryServiceTest" name="reservesStock" time="0.21"/>
  <testcase classname="com.demoshop.InventoryServiceTest" name="releasesStockOnCancel" time="0.18"/>
  <testcase classname="com.demoshop.InventoryServiceTest" name="rejectsNegativeQuantity" time="0.05">
    <failure message="expected: &lt;400&gt; but was: &lt;500&gt;" type="org.opentest4j.AssertionFailedError">org.opentest4j.AssertionFailedError: expected: &lt;400&gt; but was: &lt;500&gt;
	at com.demoshop.InventoryServiceTest.rejectsNegativeQuantity(InventoryServiceTest.java:44)</failure>
  </testcase>
  <testcase classname="com.demoshop.InventoryServiceTest" name="syncsWithWarehouse" time="0">
    <skipped message="warehouse sandbox not available"/>
  </testcase>
</testsuite>`

func (s *seeder) importJUnit(r runSpec) error {
	q := url.Values{"name": {r.Name}, "project": {r.Project}, "environment": {r.Environment}, "branch": {r.Branch}, "commit": {r.Commit}, "framework": {"junit5"}}
	if _, err := s.do(http.MethodPost, "/api/v1/import/junit?"+q.Encode(), r.At, strings.NewReader(fmt.Sprintf(surefireXML, r.At.UTC().Format("2006-01-02T15:04:05"))), "application/xml"); err != nil {
		return err
	}
	s.sum.Runs++
	s.sum.Tests += 4
	s.sum.Failed++
	return nil
}
