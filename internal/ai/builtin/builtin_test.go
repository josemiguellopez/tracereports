package builtin

import (
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func call500() *Call {
	return &Call{Method: "POST", Endpoint: "shop.local/api/orders", Host: "shop.local", Status: 500, BeforeMs: 400}
}

// Cada regla: un caso que la activa, uno que no y el borde de su condición.
func TestRules(t *testing.T) {
	cases := []struct {
		name     string
		in       Input
		rule     string
		category string
	}{
		// backend_5xx
		{"5xx causal", Input{Error: "AssertionError: Locator expected to be visible", Causal: call500()}, "backend_5xx", CategoryBackend},
		{"5xx borde 599", Input{Causal: &Call{Method: "GET", Endpoint: "x/a", Status: 599, BeforeMs: -1}}, "backend_5xx", CategoryBackend},
		{"499 no es 5xx", Input{Error: "boom", Causal: &Call{Method: "GET", Endpoint: "x/a", Status: 499, BeforeMs: -1}}, "backend_client_error", CategoryLogic},
		// backend_rate_limited
		{"429", Input{Causal: &Call{Method: "GET", Endpoint: "x/a", Status: 429, BeforeMs: -1}}, "backend_rate_limited", CategoryBackend},
		// network_unreachable / backend_no_response
		{"dns causal", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/a", Host: "api.x", Failed: true, Error: "net::ERR_NAME_NOT_RESOLVED", BeforeMs: -1}}, "network_unreachable", CategoryInfra},
		{"refused causal", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/a", Host: "api.x", Failed: true, Error: "net::ERR_CONNECTION_REFUSED", BeforeMs: -1}}, "network_unreachable", CategoryInfra},
		{"tls causal", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/a", Host: "api.x", Failed: true, Error: "net::ERR_CERT_DATE_INVALID", BeforeMs: -1}}, "network_unreachable", CategoryInfra},
		{"timeout causal", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/a", Failed: true, Error: "net::ERR_TIMED_OUT", BeforeMs: 1200}}, "backend_no_response", CategoryBackend},
		{"sin respuesta sin texto", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/a", Status: 0, BeforeMs: -1}}, "backend_no_response", CategoryBackend},
		{"408", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/a", Status: 408, BeforeMs: -1}}, "backend_no_response", CategoryBackend},
		// auth_rejected
		{"401 causal", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/me", Status: 401, BeforeMs: -1}}, "auth_rejected", CategoryInfra},
		{"403 causal", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/me", Status: 403, BeforeMs: -1}}, "auth_rejected", CategoryInfra},
		{"403 en el mensaje", Input{Error: "expected status 200, got 403"}, "auth_rejected", CategoryInfra},
		// backend_client_error
		{"404 sin recuperación", Input{Causal: &Call{Method: "GET", Endpoint: "api.x/items/#", Status: 404, BeforeMs: -1}}, "backend_client_error", CategoryLogic},
		// backend_status (sin red capturada: Go, REST, JUnit)
		{"go got/want", Input{Error: "shop_test.go: POST /api/orders: got status 500, want 201"}, "backend_status", CategoryBackend},
		{"rest expected/got", Input{Error: "expected status 201, got 500"}, "backend_status", CategoryBackend},
		{"junit expected but was", Input{Error: "expected: <400> but was: <500>"}, "backend_status", CategoryBackend},
		{"número que no es status", Input{Error: "AssertionError: expected 3 items, got 500 items in cart"}, "assertion", CategoryLogic},
		// network_error
		{"refused en el mensaje", Input{Error: "requests.exceptions.ConnectionError: HTTPConnectionPool(host='api.local', port=80): Max retries exceeded (Caused by NewConnectionError: Connection refused)"}, "network_error", CategoryInfra},
		{"ConnectionError de Python", Input{Error: "ConnectionError: db down"}, "network_error", CategoryInfra},
		{"dns en Go", Input{Error: `Get "http://api.local/x": dial tcp: lookup api.local: no such host`}, "network_error", CategoryInfra},
		// js_error
		{"pageerror + elemento ausente", Input{Error: "Error: expect(locator).toBeVisible() failed Locator: locator('.cart')", PageErrors: []string{"TypeError: cannot read properties of undefined"}}, "js_error", CategoryLogic},
		{"pageerror sin fallo de elemento", Input{Error: "AssertionError: 3 != 4", PageErrors: []string{"TypeError: x"}}, "assertion", CategoryLogic},
		// locator_not_found
		{"playwright waiting for locator", Input{Error: `TimeoutError: Page.click: Timeout 3000ms exceeded. Call log: - waiting for locator("#add-to-cart")`}, "locator_not_found", CategoryLocator},
		{"selenium no such element", Input{Error: `NoSuchElementException: no such element: Unable to locate element: {"method":"css selector","selector":"#add-to-cart"}`}, "locator_not_found", CategoryLocator},
		{"selector en el paso", Input{FailedStep: `no such element: Unable to locate element: {"selector":"#pay"}`}, "locator_not_found", CategoryLocator},
		// assertion
		{"pytest assert", Input{Error: "AssertionError: assert 3 == 4"}, "assertion", CategoryLogic},
		{"jest expected/received", Input{Error: "Error: expect(received).toBe(expected)\n\nExpected: 4\nReceived: 3"}, "assertion", CategoryLogic},
		// environment_down
		{"80 % fallido", Input{Error: "something odd", RunFailed: 8, RunTotal: 10}, "environment_down", CategoryInfra},
		{"79 % no", Input{Error: "something odd", RunFailed: 79, RunTotal: 100}, DefaultRule, CategoryLogic},
		{"pocos tests no", Input{Error: "something odd", RunFailed: 4, RunTotal: 4}, DefaultRule, CategoryLogic},
		{"ambiente caído no tapa un locator", Input{Error: `waiting for locator("#x")`, RunFailed: 9, RunTotal: 10}, "locator_not_found", CategoryLocator},
		// timeout
		{"timeout sin red", Input{Error: "context deadline exceeded"}, "timeout", CategoryBackend},
		// flaky
		{"flaky sin evidencia", Input{Error: "odd", Stability: "flaky", Flips: 4}, "flaky", CategoryInfra},
		{"persistente no es flaky", Input{Error: "odd", Stability: "persistent"}, DefaultRule, CategoryLogic},
		// default
		{"sin evidencia", Input{}, DefaultRule, CategoryLogic},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, lang := range []string{"es", "en"} {
				c.in.Lang = lang
				d := Diagnose(c.in)
				if d.Rule != c.rule || d.Category != c.category {
					t.Fatalf("%s: regla %s/%s, quería %s/%s (%s)", lang, d.Rule, d.Category, c.rule, c.category, d.Summary)
				}
				if d.Summary == "" || d.Suggestion == "" || strings.Contains(d.Summary+d.Suggestion, "{") {
					t.Fatalf("%s: texto incompleto: %q / %q", lang, d.Summary, d.Suggestion)
				}
				if d.Conclusive != (c.rule != DefaultRule) {
					t.Fatalf("conclusive = %v", d.Conclusive)
				}
			}
		})
	}
}

// El backend gana aunque el error parezca de locator: el elemento no apareció porque el servidor falló.
func TestConflictBackendBeatsLocator(t *testing.T) {
	in := Input{Lang: "es", Error: `waiting for locator(".order-confirmed")`, Causal: call500(), PageErrors: []string{"TypeError"}}
	d := Diagnose(in)
	if d.Rule != "backend_5xx" || d.Confidence != High {
		t.Fatalf("%+v", d)
	}
	if !strings.Contains(d.Summary, "POST shop.local/api/orders → HTTP 500, 0,4 s antes del fallo") {
		t.Fatalf("no cita la llamada: %s", d.Summary)
	}
	if !strings.Contains(Diagnose(Input{Lang: "en", Causal: call500()}).Summary, "0.4 s before the failure") {
		t.Fatal("en: la evidencia con decimales en inglés")
	}
}

// Misma evidencia, mismo resultado; y entre reglas de igual prioridad decide la confianza y luego el id.
func TestDeterministic(t *testing.T) {
	in := Input{Lang: "en", Error: "AssertionError: expected: <400> but was: <500>", Causal: call500(), Stability: "flaky", Flips: 3, RunFailed: 9, RunTotal: 10}
	first := Diagnose(in)
	for i := 0; i < 50; i++ {
		if d := Diagnose(in); !reflect.DeepEqual(d, first) {
			t.Fatalf("resultado distinto en la vuelta %d", i)
		}
	}
	if !strings.Contains(first.Summary, "flaky") {
		t.Fatalf("la nota de inestabilidad acompaña a la regla ganadora: %s", first.Summary)
	}
	// prioridades únicas salvo los empates documentados (rate limit y red inalcanzable no se pisan:
	// una exige respuesta 429 y la otra que no haya respuesta)
	seen := map[int][]string{}
	for _, r := range Catalog {
		seen[r.Priority] = append(seen[r.Priority], r.ID)
	}
	for p, ids := range seen {
		if len(ids) > 1 && !(p == 95 && reflect.DeepEqual(ids, []string{"backend_rate_limited", "network_unreachable"})) {
			t.Errorf("prioridad %d repetida: %v", p, ids)
		}
	}
}

// Cada clave existe en los dos idiomas, con las mismas variables y sin textos vacíos.
func TestTemplatesBothLanguages(t *testing.T) {
	vars := regexp.MustCompile(`\{\w+\}`)
	set := func(s string) []string {
		v := vars.FindAllString(s, -1)
		sort.Strings(v)
		return v
	}
	for key, es := range texts["es"] {
		en, ok := texts["en"][key]
		if !ok {
			t.Errorf("falta en inglés: %s", key)
			continue
		}
		if !reflect.DeepEqual(set(es.Summary+es.Suggestion), set(en.Summary+en.Suggestion)) {
			t.Errorf("%s: variables distintas es=%v en=%v", key, set(es.Summary+es.Suggestion), set(en.Summary+en.Suggestion))
		}
		if es.Summary == "" || en.Summary == "" {
			t.Errorf("%s: texto vacío", key)
		}
	}
	for key := range texts["en"] {
		if _, ok := texts["es"][key]; !ok {
			t.Errorf("falta en español: %s", key)
		}
	}
	for _, r := range append(Catalog, Rule{ID: DefaultRule}) {
		for _, lang := range []string{"es", "en"} {
			if tx := texts[lang][r.ID]; tx.Summary == "" || tx.Suggestion == "" {
				t.Errorf("la regla %s no tiene textos en %s", r.ID, lang)
			}
		}
	}
}

func TestDefaultCitesBestEvidence(t *testing.T) {
	d := Diagnose(Input{Lang: "es", Error: "\n  Something unexpected happened\nmore"})
	if d.Conclusive || !strings.Contains(d.Summary, "Something unexpected happened") {
		t.Fatalf("%+v", d)
	}
	d = Diagnose(Input{Lang: "en", FailedStep: "click Pay"})
	if !strings.Contains(d.Summary, "click Pay") {
		t.Fatalf("sin error, cita el paso que falló: %s", d.Summary)
	}
	d = Diagnose(Input{Lang: "es"})
	if !strings.Contains(d.Summary, "no informó") {
		t.Fatalf("sin nada: %s", d.Summary)
	}
}

func TestAssertionExtractsValues(t *testing.T) {
	for in, want := range map[string]string{
		"expected: <4> but was: <3>":                                         "se esperaba 4 y se obtuvo 3",
		"Error: expect(received).toBe(expected)\n\nExpected: 4\nReceived: 3": "se esperaba 4 y se obtuvo 3",
		"total: got 3, want 4":                                               "se esperaba 4 y se obtuvo 3",
		"AssertionError: assert 3 == 4":                                      "se esperaba 4 y se obtuvo 3",
		"AssertionError: expected 4 but got 3":                               "se esperaba 4 y se obtuvo 3",
	} {
		d := Diagnose(Input{Lang: "es", Error: in, Location: "tests/test_cart.py:12"})
		if d.Rule != "assertion" || !strings.Contains(d.Summary, want) || !strings.Contains(d.Suggestion, "tests/test_cart.py:12") {
			t.Errorf("%q → %s / %s", in, d.Summary, d.Suggestion)
		}
	}
}

func TestSummarize(t *testing.T) {
	inc := []Incident{
		{Tests: 3, Diagnosis: Diagnose(Input{Lang: "es", Causal: call500()})},
		{Tests: 1, Diagnosis: Diagnose(Input{Lang: "es", Error: `waiting for locator("#x")`})},
		{Tests: 1, Diagnosis: Diagnose(Input{Lang: "es", Error: "odd"})},
	}
	head, sum := Summarize("es", 10, 5, inc)
	if head != "" {
		t.Fatalf("50 %% fallido no es ambiente caído: %q", head)
	}
	for _, want := range []string{"3 incidentes", "1 de backend", "1 de locator", "1 sin regla concluyente", "Revisa los logs"} {
		if !strings.Contains(sum, want) {
			t.Errorf("falta %q en %q", want, sum)
		}
	}
	head, sum = Summarize("en", 10, 9, inc[:1])
	if !strings.Contains(head, "90 % of the tests failed") || !strings.Contains(sum, "1 incident according") {
		t.Fatalf("%q / %q", head, sum)
	}
	if h, s := Summarize("es", 3, 0, nil); h != "" || s != "" {
		t.Fatal("sin incidentes no hay resumen")
	}
}

func TestDetectLang(t *testing.T) {
	for in, want := range map[string]string{
		"crear una orden con tarjeta":      "es",
		"pago rechazado":                   "en", // ambiguo: inglés, como el LLM
		"añadir al carrito":                "es",
		"test_checkout_pays_the_order":     "en",
		"Shop › pays an order with a card": "en",
		"el usuario inicia sesión":         "es",
	} {
		if got := DetectLang(in); got != want {
			t.Errorf("%q: %s", in, got)
		}
	}
}

// La firma de un mensaje cualquiera no hace entrar en pánico al motor, y su resultado es estable.
func FuzzDiagnose(f *testing.F) {
	for _, s := range []string{"", "expected: <400> but was: <500>", `waiting for locator("#x")`, "got status 999, want 201",
		"assert ( == )", "expected:\nReceived:", "Expected: \x00", "http://[::1", "lookup : no such host"} {
		f.Add(s, s, 3, 5)
	}
	f.Fuzz(func(t *testing.T, msg, trace string, failed, total int) {
		in := Input{Lang: "es", Error: msg, Trace: trace, FailedStep: msg, RunFailed: failed, RunTotal: total, PageErrors: []string{trace}}
		a, b := Diagnose(in), Diagnose(in)
		if !reflect.DeepEqual(a, b) {
			t.Fatal("no determinístico")
		}
		if a.Rule == "" || a.Category == "" || a.Summary == "" {
			t.Fatalf("diagnóstico vacío: %+v", a)
		}
	})
}
