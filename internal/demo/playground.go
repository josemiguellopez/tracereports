package demo

import (
	"context"
	_ "embed"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"sync"
	"time"
)

// El playground: la página de la tienda de la demo donde cualquiera ejecuta un escenario (lo que
// envía de verdad cada cliente) y ve las peticiones, lo que comprobó y el link a la ejecución en
// TraceReports. "Ejecutar todos" es el mismo chequeo que `tracereports selftest`.

//go:embed playground.html
var playgroundHTML []byte

// scenarioES are the Spanish title and description of each scenario.
var scenarioES = map[string][2]string{
	"rest-ok":                {"API REST · una ejecución que pasa", "Cualquier lenguaje puede reportar con la API REST: una ejecución con dos tests que pasan y sus pasos."},
	"rest-nok-backend":       {"API REST · el backend responde 500", "Un test reportado por REST con su red: la llamada que falla se vuelve un incidente de backend que puedes reenviar en el Lab."},
	"pytest-ok":              {"pytest + Playwright · iniciar sesión", "Un test real de pytest-playwright: pasos, una captura y cada llamada del navegador, con la contraseña enmascarada."},
	"pytest-nok-backend":     {"pytest + Playwright · el pago falla (500)", "El pedido nunca se confirma porque POST /api/orders responde 500: incidente de backend, captura, consola y el Lab."},
	"pytest-nok-selector":    {"pytest + Playwright · cambió un selector", "El test sigue usando #add-to-cart-btn; la página ahora tiene data-testid=\"add-to-cart\": TraceReports sugiere el nuevo."},
	"playwright-ok":          {"Playwright Test (JS) · iniciar sesión", "Una ejecución real de Playwright Test con el reporter de TraceReports: pasos, adjuntos y red."},
	"playwright-nok-backend": {"Playwright Test (JS) · el pago falla (500)", "El mismo fallo de backend visto desde JavaScript: se agrupa en un incidente de backend y el Lab lo reproduce."},
	"playwright-flaky":       {"Playwright Test (JS) · pasa al reintentar", "Las recomendaciones fallan en la primera llamada (503) y funcionan en la segunda: el test queda como pasado tras reintento (inestable)."},
	"selenium-ok":            {"Selenium + JUnit 5 (Java) · iniciar sesión", "Un test real de Selenium WebDriver con la extensión de JUnit 5: pasos y una captura."},
	"selenium-nok-selector":  {"Selenium + JUnit 5 (Java) · cambió un selector", "NoSuchElementException para #add-to-cart-btn: captura, snapshot de la página y el selector sugerido."},
	"go-ok":                  {"Go (go test) · la API pasa", "Un test de API en Go con el cliente de Go: la llamada que hizo aparece en la pestaña Red."},
	"go-nok-backend":         {"Go (go test) · la API responde 500", "El test de API que crea un pedido falla con 500: incidente de backend y el Lab, desde Go."},
	"junit-import":           {"Importación de JUnit XML (Maven Surefire)", "Sin cliente: un reporte XML de Surefire importado con POST /api/v1/import/junit (tests que pasan, fallan y se omiten)."},
}

// Playground serves the playground page and its API, and the shop for everything else.
type Playground struct {
	Shop *Shop
	// Base is where the playground sends the scenarios (http://127.0.0.1:8080); Public is the
	// TraceReports address for the links of the page (http://localhost:8080).
	Base, Public string
	// ShopURL is this shop's address, the one the recorded calls point to.
	ShopURL string
	mu      sync.Mutex // un escenario a la vez: cada uno pone el estado de la tienda que necesita
}

type scenarioView struct {
	Scenario
	TitleES string `json:"title_es"`
	WhatES  string `json:"what_es"`
}

type playResult struct {
	RunID  int64   `json:"run_id,omitempty"`
	Report string  `json:"report,omitempty"`
	OK     bool    `json:"ok"`
	Checks []Check `json:"checks"`
	Sent   []Sent  `json:"sent"`
	Error  string  `json:"error,omitempty"`
	Ms     int64   `json:"ms"`
}

func (p *Playground) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/" && r.Method == http.MethodGet:
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(playgroundHTML)
	case r.URL.Path == "/__scenarios" && r.Method == http.MethodGet:
		out := make([]scenarioView, 0, len(Scenarios))
		for _, sc := range Scenarios {
			es := scenarioES[sc.ID]
			out = append(out, scenarioView{Scenario: sc, TitleES: es[0], WhatES: es[1]})
		}
		writeJSON(w, http.StatusOK, map[string]any{"scenarios": out, "tracereports": p.Public, "user": ShopUser, "password": ShopPassword})
	case r.URL.Path == "/__state" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, p.state())
	case r.URL.Path == "/__state" && r.Method == http.MethodPost:
		if !jsonBody(w, r) {
			return
		}
		var in struct {
			Bug   string `json:"bug"`
			On    bool   `json:"on"`
			Reset bool   `json:"reset"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		if in.Reset {
			p.Shop.Reset()
		} else if knownBug(in.Bug) {
			p.Shop.SetBug(in.Bug, in.On)
		}
		writeJSON(w, http.StatusOK, p.state())
	case r.URL.Path == "/__play" && r.Method == http.MethodPost:
		if !jsonBody(w, r) {
			return
		}
		var in struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&in)
		sc, ok := ScenarioByID(in.ID)
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown scenario"})
			return
		}
		writeJSON(w, http.StatusOK, p.play(r.Context(), sc))
	default:
		p.Shop.ServeHTTP(w, r)
	}
}

// jsonBody only accepts JSON writes: a page on another site cannot send them without a CORS
// preflight, which this server never approves.
func jsonBody(w http.ResponseWriter, r *http.Request) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "expected application/json"})
		return false
	}
	return true
}

func knownBug(key string) bool {
	for _, b := range Bugs {
		if b.Key == key {
			return true
		}
	}
	return false
}

// bugES are the Spanish title and effect of each bug of the shop.
var bugES = map[string][2]string{
	"orders": {"Pedidos: pool de conexiones agotado", "POST /api/orders responde 500"},
	"slow":   {"Productos: base de datos lenta", "GET /api/products tarda 3 s"},
	"flaky":  {"Recomendaciones: caché inestable", "GET /api/recommendations falla una llamada sí y otra no (503)"},
}

func (p *Playground) state() map[string]any {
	var bugs []map[string]any
	for _, b := range Bugs {
		es := bugES[b.Key]
		bugs = append(bugs, map[string]any{"key": b.Key, "title": b.Title, "effect": b.Effect, "title_es": es[0], "effect_es": es[1], "on": p.Shop.Bug(b.Key)})
	}
	return map[string]any{"bugs": bugs}
}

// play sends a scenario with the shop as it needs it (its bugs), checks what TraceReports shows
// and leaves the shop as the person had it.
func (p *Playground) play(ctx context.Context, sc Scenario) playResult {
	p.mu.Lock()
	defer p.mu.Unlock()
	saved := map[string]bool{}
	for _, b := range Bugs {
		saved[b.Key] = p.Shop.Bug(b.Key)
	}
	defer func() {
		for k, v := range saved {
			p.Shop.SetBug(k, v)
		}
	}()
	p.Shop.Reset()
	for k, v := range sc.Bugs {
		p.Shop.SetBug(k, v)
	}
	start := time.Now()
	res := playResult{Checks: []Check{}, Sent: []Sent{}}
	var mu sync.Mutex
	runID, err := PlayLogged(ctx, p.Base, p.ShopURL, sc, time.Now(), func(s Sent) {
		mu.Lock()
		res.Sent = append(res.Sent, s)
		mu.Unlock()
	})
	if err != nil {
		res.Error = err.Error()
		res.Ms = time.Since(start).Milliseconds()
		return res
	}
	res.RunID = runID
	res.Report = strings.TrimRight(p.Public, "/") + "/#run=" + itoa(runID) + "&view=tests"
	res.Checks = Verify(ctx, p.Base, sc, runID)
	res.OK = true
	for _, c := range res.Checks {
		res.OK = res.OK && c.OK
	}
	res.Ms = time.Since(start).Milliseconds()
	return res
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
