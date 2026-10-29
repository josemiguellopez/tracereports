package ai

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai/builtin"
	"github.com/josemiguellopez/tracereports/internal/db"
)

// noNetwork falla el test si alguien usa la red (el cliente del analizador o el transporte por defecto).
type noNetwork struct{ t *testing.T }

func (n noNetwork) RoundTrip(r *http.Request) (*http.Response, error) {
	n.t.Errorf("el motor local no debe usar la red: %s %s", r.Method, r.URL)
	return nil, io.EOF
}

// builtinRun arma una ejecución con un fallo de backend, uno de locator y uno de aserción.
func builtinRun(t *testing.T, store *db.Store, runID int64) (backend, loc, assert int64) {
	t.Helper()
	const t0 = 1_700_000_000_000
	backend = failAt(t, store, runID, "pays an order", `AssertionError: Locator expected to be visible
Error: element(s) not found`, "", t0+1_000,
		db.NetConn{Method: "POST", URL: "https://shop.test/api/orders/12345?token=abc", Status: 500, StartedAt: t0 + 600})
	loc = failAt(t, store, runID, "add to cart", `TimeoutError: Page.click: Timeout 3000ms exceeded. Call log: - waiting for locator("#add-to-cart")`, "", t0+2_000)
	assert = failAt(t, store, runID, "cart total", "AssertionError: assert 3 == 4", `File "tests/test_cart.py", line 12, in test_total`, t0+3_000)
	if _, err := store.FinishRun(runID); err != nil {
		t.Fatal(err)
	}
	return
}

// Con cero configuración diagnostica el motor local, sin una sola petición de red.
func TestBuiltinDiagnosesWithoutNetwork(t *testing.T) {
	clearEnv(t)
	prev := http.DefaultTransport
	http.DefaultTransport = noNetwork{t}
	t.Cleanup(func() { http.DefaultTransport = prev })
	store, runID := groupingStore(t)
	backend, loc, assert := builtinRun(t, store, runID)

	a := New(store)
	a.client = &http.Client{Transport: noNetwork{t}}
	a.SetLanguage("es")
	if a.Provider() != ProviderBuiltin {
		t.Fatalf("provider por defecto: %q", a.Provider())
	}
	for _, id := range []int64{backend, loc, assert} {
		if !a.AnalyzeAsync(id) {
			t.Fatalf("test %d no se encoló", id)
		}
	}
	waitAll(t, a)
	want := map[int64][2]string{
		backend: {"BACKEND_TIMEOUT", "backend_5xx"},
		loc:     {"LOCATOR_CHANGED", "locator_not_found"},
		assert:  {"LOGIC_BUG", "assertion"},
	}
	for id, w := range want {
		got, _ := store.GetTest(id)
		tr := got.Triage
		if tr == nil || tr.State != "DONE" || tr.Category != w[0] || tr.Rule != w[1] || tr.Provider != ProviderBuiltin {
			t.Fatalf("test %d: %+v", id, tr)
		}
	}
	if got, _ := store.GetTest(backend); !strings.Contains(got.Triage.Summary, "POST shop.test/api/orders/:id → HTTP 500, 0,4 s antes del fallo") ||
		strings.Contains(got.Triage.Summary, "token=abc") {
		t.Fatalf("la conclusión cita la llamada, sin query: %s", got.Triage.Summary)
	}
	if got, _ := store.GetTest(assert); !strings.Contains(got.Triage.Summary, "se esperaba 4 y se obtuvo 3") || !strings.Contains(got.Triage.Suggestion, "tests/test_cart.py:12") {
		t.Fatalf("la aserción cita valores y línea: %+v", got.Triage)
	}

	// diagnóstico de la ejecución: causa y acción por incidente, sin IA y sin red
	a.AnalyzeRunAsync(runID, nil)
	waitAll(t, a)
	rt, _ := store.GetRunTriage(runID)
	if rt == nil || rt.State != "DONE" || rt.AI || rt.Provider != ProviderBuiltin || len(rt.Incidents) != 3 {
		t.Fatalf("resumen: %+v", rt)
	}
	for _, in := range rt.Incidents {
		if in.Cause == "" || in.Action == "" || in.Rule == "" || in.Insufficient {
			t.Fatalf("incidente sin conclusión del motor: %+v", in)
		}
	}
	if !strings.Contains(rt.Summary, "3 incidentes según el motor local") {
		t.Fatalf("resumen: %q", rt.Summary)
	}
	// el escalamiento usa la plantilla (el motor local no redacta) y el estado no consulta nada
	if e := a.Escalate(context.Background(), &Facts{}, runID, 0, "qa", "es"); e.Source == "ai" || e.AIError != "" {
		t.Fatalf("escalamiento: %+v", e)
	}
	if s := a.ProviderStatus(context.Background(), true); s.Indicator != "none" {
		t.Fatalf("estado: %+v", s)
	}
	if took, err := a.Test(context.Background(), Config{Provider: ProviderBuiltin}); err != nil || took != 0 {
		t.Fatalf("probar conexión: %v %v", took, err)
	}
	// nada se cuenta como uso de un proveedor de IA
	if u, err := store.AIUsage(time.Now()); err != nil || u.Today.Calls != 0 {
		t.Fatalf("el motor local no gasta llamadas: %v %+v", err, u)
	}
}

// El límite de diagnósticos por ejecución cuida el costo de un LLM: el motor local no lo usa.
func TestBuiltinIgnoresPerRunBudget(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	a := New(store)
	a.MaxPerRun = 1
	ids := []int64{
		failAt(t, store, runID, "A", "AssertionError: a", "", 0),
		failAt(t, store, runID, "B", "AssertionError: b", "", 0),
	}
	for _, id := range ids {
		a.AnalyzeAsync(id)
	}
	waitAll(t, a)
	for _, id := range ids {
		if got, _ := store.GetTest(id); got.Triage == nil || got.Triage.State != "DONE" {
			t.Fatalf("test %d: %+v", id, got.Triage)
		}
	}
}

// Con un LLM configurado, el motor local corre primero y el modelo recibe su conclusión, redactada.
func TestLLMReceivesBuiltinConclusionRedacted(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	const t0 = 1_700_000_000_000
	id := failAt(t, store, runID, "pays an order", "Error: element(s) not found (Authorization: Bearer sk-live-SECRET123)", "", t0+1_000,
		db.NetConn{Method: "POST", URL: "https://shop.test/api/orders", Status: 503, StartedAt: t0 + 500})
	store.FinishRun(runID)

	var mu sync.Mutex
	var prompts []string
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct{ Content string } `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		for _, m := range body.Messages {
			prompts = append(prompts, m.Content)
		}
		mu.Unlock()
		w.Write([]byte(`{"message":{"content":"{\"category\":\"BACKEND_TIMEOUT\",\"summary\":\"s\",\"suggestion\":\"x\",\"headline\":\"h\",\"incidents\":[]}"}}`))
	}))
	defer llm.Close()
	a := New(store)
	if err := a.SetConfig(Config{Provider: "ollama", BaseURL: llm.URL}); err != nil {
		t.Fatal(err)
	}
	a.AnalyzeAsync(id)
	waitAll(t, a)
	a.AnalyzeRunAsync(runID, nil)
	waitAll(t, a)

	mu.Lock()
	defer mu.Unlock()
	if len(prompts) < 2 {
		t.Fatalf("el LLM debía recibir el test y la ejecución: %d prompts", len(prompts))
	}
	for i, p := range prompts {
		if !strings.Contains(p, "backend_5xx") || !strings.Contains(p, "local rules engine") {
			t.Errorf("prompt %d sin la conclusión del motor local: %.300s", i, p)
		}
		if strings.Contains(p, "SECRET123") {
			t.Errorf("prompt %d con un secreto sin redactar", i)
		}
	}
	got, _ := store.GetTest(id)
	if got.Triage.Provider != "ollama" || got.Triage.Rule != "" {
		t.Fatalf("lo escribió el LLM: %+v", got.Triage)
	}

	// TRACEREPORTS_AI_BUILTIN_HINT=0: el prompt va sin la conclusión del motor
	t.Setenv("TRACEREPORTS_AI_BUILTIN_HINT", "0")
	prompts = nil
	mu.Unlock()
	a.ReanalyzeAsync(id)
	waitAll(t, a)
	mu.Lock()
	if len(prompts) == 0 || strings.Contains(prompts[0], "local rules engine") {
		t.Fatalf("sin pista: %v", prompts)
	}
}

// La firma de error es estable y nunca entra en pánico, con cualquier mensaje o traza.
func FuzzErrorSignature(f *testing.F) {
	for _, s := range []string{"", "AssertionError: assert 3 == 4", "E   TimeoutError: waiting 3000ms\n", `File "a.py", line 12`,
		"x_test.go:12: got 500", "\x00\xff", strings.Repeat("9", 300)} {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, msg, trace string) {
		s1, e1, l1 := errorSignature(msg, trace)
		s2, e2, l2 := errorSignature(msg, trace)
		if s1 != s2 || e1 != e2 || l1 != l2 {
			t.Fatal("firma inestable")
		}
		if !strings.HasPrefix(s1, e1+"|") || !strings.HasSuffix(s1, "|"+l1) {
			t.Fatalf("formato: %q", s1)
		}
		_ = builtin.Diagnose(builtin.Input{Error: msg, Trace: trace, Location: l1})
	})
}
