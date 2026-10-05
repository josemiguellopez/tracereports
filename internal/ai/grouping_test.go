package ai

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func groupingStore(t *testing.T) (*db.Store, int64) {
	t.Helper()
	store, err := db.Open(filepath.Join(t.TempDir(), "g.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	runID, _ := store.CreateRun("Regresión", "qa")
	return store, runID
}

// failAt creates a failed test whose FAIL step happens at `at` (ms) with the given connections.
func failAt(t *testing.T, store *db.Store, runID int64, name, msg, trace string, at int64, conns ...db.NetConn) int64 {
	t.Helper()
	id, _ := store.CreateTest(runID, name, "", "")
	if len(conns) > 0 {
		if err := store.AddNetwork(id, conns); err != nil {
			t.Fatal(err)
		}
	}
	store.AddLog(id, "FAIL", msg, at, "")
	store.FinishTest(id, "FAIL", msg, trace)
	return id
}

func incidents(t *testing.T, store *db.Store, runID int64) []db.Incident {
	t.Helper()
	store.FinishRun(runID)
	run, err := store.GetRunDetail(runID)
	if err != nil {
		t.Fatal(err)
	}
	a := New(store)
	out, err := a.groupFailures(run)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestExpectedOrRecoveredNegativeResponseDoesNotDominate(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	const t0 = 1_700_000_000_000
	// 401 declarado como esperado: el fallo posterior de UI se agrupa por su error
	failAt(t, store, runID, "Login inválido", "TimeoutError: locator('#welcome') not visible", "", t0+20_000,
		db.NetConn{Method: "POST", URL: "https://app.test/api/auth", Status: 401, StartedAt: t0, Expected: true})
	// 401 no declarado, pero la app siguió (después hubo respuestas OK del mismo host)
	failAt(t, store, runID, "Perfil", "TimeoutError: locator('#welcome') not visible", "", t0+20_000,
		db.NetConn{Method: "GET", URL: "https://app.test/api/me", Status: 401, StartedAt: t0},
		db.NetConn{Method: "POST", URL: "https://app.test/api/auth", Status: 200, StartedAt: t0 + 1000})
	// 5xx justo antes del fallo: esa sí es la causa probable
	failAt(t, store, runID, "Pago", "AssertionError: no llegó la confirmación", "", t0+5_000,
		db.NetConn{Method: "POST", URL: "https://app.test/api/pay", Status: 503, StartedAt: t0 + 4_000})
	// 5xx ocurrido después del fallo (teardown): no explica el fallo
	failAt(t, store, runID, "Logout", "AssertionError: menú no visible", "", t0+5_000,
		db.NetConn{Method: "POST", URL: "https://app.test/api/logout", Status: 500, StartedAt: t0 + 60_000})

	got := incidents(t, store, runID)
	byTitle := map[string]db.Incident{}
	for _, in := range got {
		byTitle[in.Title] = in
	}
	ui := byTitle["TimeoutError: locator('#welcome') not visible"]
	if len(got) != 3 || ui.Kind != "error" || len(ui.TestIDs) != 2 {
		t.Fatalf("the two UI failures must group by their error, not by the 401s: %+v", got)
	}
	pay := byTitle["Backend: POST app.test/api/pay → HTTP 503"]
	if pay.Kind != "backend" || len(pay.Evidence) == 0 || !strings.Contains(pay.Evidence[0].Text, "1.0 s antes del fallo") {
		t.Fatalf("the 503 right before the failure is the backend incident with its evidence: %+v", got)
	}
	if _, ok := byTitle["AssertionError: menú no visible"]; !ok {
		t.Fatalf("a 500 after the failure must not be its cause: %+v", got)
	}
}

func TestDifferentServicesAndBugsStaySeparate(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	// misma ruta en dos servicios distintos
	failAt(t, store, runID, "Catálogo", "x", "", 0, db.NetConn{Method: "GET", URL: "https://catalog.test/api/health", Status: 503})
	failAt(t, store, runID, "Pagos", "x", "", 0, db.NetConn{Method: "GET", URL: "https://payments.test/api/health", Status: 503})
	// dos bugs de lógica distintos (aunque la IA diga LOGIC_BUG en ambos)
	a := failAt(t, store, runID, "Total", "AssertionError: assert 10 == 12", "tests/test_cart.py:40: AssertionError", 0)
	b := failAt(t, store, runID, "Descuento", "AssertionError: discount not applied", "tests/test_promo.py:12: AssertionError", 0)
	for _, id := range []int64{a, b} {
		store.SetTriagePending(id)
		store.SaveTriage(id, "LOGIC_BUG", "s", "x", "", "")
	}
	// el mismo error con otros números: misma firma
	failAt(t, store, runID, "Total 2", "AssertionError: assert 11 == 13", "tests/test_cart.py:40: AssertionError", 0)

	got := incidents(t, store, runID)
	if len(got) != 4 {
		t.Fatalf("want 4 incidents (2 services, 2 bugs), got %d: %+v", len(got), got)
	}
	if got[0].Kind != "error" || len(got[0].TestIDs) != 2 || got[0].Location != "tests/test_cart.py:40" || got[0].Exception != "AssertionError" {
		t.Fatalf("same signature with different numbers must group: %+v", got[0])
	}
}

func TestNoIncidentIsDropped(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	for i := 0; i < 12; i++ {
		failAt(t, store, runID, fmt.Sprintf("T%d", i), fmt.Sprintf("ValueError: bad field %c", 'a'+i), "", 0)
	}
	got := incidents(t, store, runID)
	tests := 0
	for _, in := range got {
		tests += len(in.TestIDs)
	}
	if len(got) != 12 || tests != 12 {
		t.Fatalf("every failed test must stay in an incident: %d incidents, %d tests", len(got), tests)
	}
}

func TestErrorSignatureLocation(t *testing.T) {
	trace := `Traceback (most recent call last):
  File "C:\work\tests\test_login.py", line 22, in test_login
    page.login()
  File "C:\work\pages\login_page.py", line 31, in login
    self.page.click("#go")
  File "C:\Python\Lib\site-packages\playwright\sync_api\_generated.py", line 999, in click
playwright._impl._errors.TimeoutError: Timeout 30000ms exceeded.`
	_, exc, loc := errorSignature("playwright._impl._errors.TimeoutError: Timeout 30000ms exceeded.", trace)
	if exc != "playwright._impl._errors.TimeoutError" || loc != "pages/login_page.py:31" {
		t.Fatalf("exc=%q loc=%q (want the innermost frame of the project, not the library)", exc, loc)
	}
}

func clearEnv(t *testing.T) {
	for _, k := range []string{"AI_PROVIDER", "AI_API_KEY", "GEMINI_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OLLAMA_HOST"} {
		t.Setenv(k, "")
	}
}
