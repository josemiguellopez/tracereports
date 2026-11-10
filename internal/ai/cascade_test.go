package ai

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/atrest"
	"github.com/josemiguellopez/tracereports/internal/db"
)

const minute = int64(60_000)

// cascadeTest creates a test of file `file` (pytest nodeid) that ran from start to end (ms).
func cascadeTest(t *testing.T, store *db.Store, runID int64, file, name, status, msg, trace string, start, end int64) int64 {
	t.Helper()
	id, err := store.CreateTestWithMeta(runID, name, "", "", db.TestMeta{Key: file + "::TestFlujo::" + name})
	if err != nil {
		t.Fatal(err)
	}
	if status == "FAIL" {
		store.AddLog(id, "FAIL", msg, end, "")
	}
	store.FinishTest(id, status, msg, trace)
	if err := store.SetTestTimes(id, start, end); err != nil {
		t.Fatal(err)
	}
	return id
}

func trace(file string, line int) string {
	return "Traceback (most recent call last):\n  File \"C:\\work\\" + file + "\", line " + strconv.Itoa(line) + ", in test\n    raise Exception(msg)\nException: x"
}


func incidentWith(got []db.Incident, id int64) *db.Incident {
	for i := range got {
		for _, x := range got[i].TestIDs {
			if x == id {
				return &got[i]
			}
		}
	}
	return nil
}

// La forma del caso real (sintética): el primero del archivo se cuelga una hora sobre una página en
// blanco; los 4 siguientes dependen del pedido que no creó y fallan rápido, cada uno en otra línea.
func TestCascadeFromAHungFirstTest(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	const file = "tests/farmacia/test_pedidos.py"
	t0 := int64(1_700_000_000_000)
	root := cascadeTest(t, store, runID, file, "test_001_crear_pedido", "FAIL",
		"During handling of the above exception, another exception occurred:\nErrores acumulados: ['Error al enviar texto: elemento no interactuable']", trace(file, 60), t0, t0+64*minute)
	store.SaveDOM(root, `{"url":"https://app/","viewport":{"w":1280,"h":800},"elements":[{"tag":"div","visible":false},{"tag":"script","visible":false}]}`)
	var followers []int64
	for i, line := range []int{135, 158, 181, 213} {
		start := t0 + 64*minute + int64(i)*3000
		followers = append(followers, cascadeTest(t, store, runID, file, "test_00"+strconv.Itoa(2+i)+"_paso", "FAIL",
			"Exception: Error número de pedido no puede ser vacio", trace(file, line), start, start+2500))
	}
	got := incidents(t, store, runID)
	if len(got) != 1 {
		t.Fatalf("want 1 incident (1 cause, 5 failures), got %d: %+v", len(got), got)
	}
	in := got[0]
	if in.Kind != "cascade" || len(in.TestIDs) != 5 || in.TestIDs[0] != root {
		t.Fatalf("cascade incident with the first test first: %+v", in)
	}
	if in.Title != "Errores acumulados: ['Error al enviar texto: elemento no interactuable']" {
		t.Fatalf("the title is the error of the first test, not the chained-exception line: %q", in.Title)
	}
	kinds := map[string]string{}
	for _, e := range in.Evidence {
		kinds[e.Kind] = e.Text
	}
	if !strings.Contains(kinds["cascade"], "4 test(s) siguientes") || !strings.Contains(kinds["hung"], "1 h 04 min") ||
		!strings.Contains(kinds["blank_page"], "no tiene elementos visibles") {
		t.Fatalf("evidence: %+v", in.Evidence)
	}
	if h := defaultHeadline(mustRun(t, store, runID), got); !strings.Contains(h, "en cascada") {
		t.Fatalf("headline: %q", h)
	}
}

func mustRun(t *testing.T, store *db.Store, runID int64) *db.RunDetail {
	t.Helper()
	run, err := store.GetRunDetail(runID)
	if err != nil {
		t.Fatal(err)
	}
	return run
}

// Los siguientes dicen explícitamente que se omitieron porque falló el primero (sin colgarse).
func TestCascadeWhenTheMessageNamesTheFirstTest(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	const file = "tests/farmacia/test_receta.py"
	root := cascadeTest(t, store, runID, file, "test_001_login_nodo", "FAIL", "Errores acumulados: ['No se pudo iniciar sesión']", "", 0, 47_000)
	a := cascadeTest(t, store, runID, file, "test_002_validar", "FAIL",
		"Failed: Test 'test_002_validar' saltado: falló 'test_001_login_nodo'", "", 47_000, 48_000)
	got := incidents(t, store, runID)
	if len(got) != 1 || got[0].Kind != "cascade" || incidentWith(got, a) != incidentWith(got, root) {
		t.Fatalf("named first test: %+v", got)
	}
	for _, e := range got[0].Evidence {
		if e.Kind == "hung" {
			t.Fatalf("47 s is not hung: %+v", e)
		}
	}
}

// Lo que no apunta al primero no se le atribuye: un PASS corta la cadena, otro archivo es otra
// historia y un error propio (una aserción) sigue siendo su propio incidente.
func TestCascadeNeedsEvidence(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	const file, other = "tests/test_carro.py", "tests/test_otro.py"
	root := cascadeTest(t, store, runID, file, "test_001_crear", "FAIL", "TimeoutError: page load", "", 0, 10_000)
	own := cascadeTest(t, store, runID, file, "test_002_total", "FAIL", "AssertionError: assert 10 == 12", "", 10_000, 11_000)
	cascadeTest(t, store, runID, file, "test_003_pasa", "PASS", "", "", 11_000, 12_000)
	afterPass := cascadeTest(t, store, runID, file, "test_004_enviar", "FAIL", "Exception: pedido vacio", "", 12_000, 13_000)
	otherFile := cascadeTest(t, store, runID, other, "test_001_x", "FAIL", "Exception: pedido vacio", "", 1_000, 2_000)
	got := incidents(t, store, runID)
	for _, id := range []int64{own, afterPass, otherFile} {
		if in := incidentWith(got, id); in == nil || in.Kind == "cascade" || in == incidentWith(got, root) {
			t.Fatalf("test %d must not join the first test's incident: %+v", id, got)
		}
	}
	for _, in := range got {
		if in.Kind == "cascade" {
			t.Fatalf("no cascade without evidence: %+v", in)
		}
	}
}

// Punto 1: el mismo error del mismo archivo en líneas distintas es un incidente; las líneas quedan
// en la ubicación.
func TestSameErrorOnDifferentLinesOfAFileGroups(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	for i, line := range []int{135, 158, 181} {
		failAt(t, store, runID, "T"+string(rune('a'+i)), "Exception: Error número de pedido no puede ser vacio",
			trace("tests/pedidos.py", line), 0)
	}
	failAt(t, store, runID, "Otro archivo", "Exception: Error número de pedido no puede ser vacio", trace("tests/otro.py", 135), 0)
	got := incidents(t, store, runID)
	if len(got) != 2 || len(got[0].TestIDs) != 3 || got[0].Location != "tests/pedidos.py:135, 158, 181" {
		t.Fatalf("same error, same file: %+v", got)
	}
}

// Punto 3: página en blanco por la captura de un solo color (sin DOM); un elemento visible en el
// DOM dice que no lo estaba, diga lo que diga la captura.
func TestBlankPageEvidence(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	id := failAt(t, store, runID, "Sin DOM", "TimeoutError: x", "", 0)
	dir := t.TempDir()
	img := image.NewRGBA(image.Rect(0, 0, 200, 120))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)
	f, err := os.Create(filepath.Join(dir, "blank.png"))
	if err != nil {
		t.Fatal(err)
	}
	png.Encode(f, img)
	f.Close()
	a := New(store)
	if got := a.blankPage(id, "/screenshots/blank.png"); got != "" {
		t.Fatalf("without the screenshots folder there is nothing to tell: %q", got)
	}
	a.Shots = atrest.Dir{Path: dir}
	if got := a.blankPage(id, "/screenshots/blank.png"); !strings.Contains(got, "un solo color") {
		t.Fatalf("flat screenshot: %q", got)
	}
	store.SaveDOM(id, `{"elements":[{"tag":"button","visible":true}]}`)
	if got := a.blankPage(id, "/screenshots/blank.png"); got != "" {
		t.Fatalf("a visible element is not a blank page: %q", got)
	}
}

// Con el motor local, una cascada no es "el ambiente estaba caído": sus fallos no son independientes.
func TestCascadeIsNotADownEnvironment(t *testing.T) {
	clearEnv(t)
	t.Setenv("AI_PROVIDER", "builtin")
	store, runID := groupingStore(t)
	const file = "tests/farmacia/test_pedidos.py"
	cascadeTest(t, store, runID, file, "test_001_crear_pedido", "FAIL", "TimeoutError: la página no cargó", "", 0, 10_000)
	for i := 0; i < 4; i++ {
		start := int64(10_000 + i*3000)
		cascadeTest(t, store, runID, file, "test_00"+strconv.Itoa(2+i)+"_paso", "FAIL", "Exception: Error número de pedido no puede ser vacio", "", start, start+2000)
	}
	cascadeTest(t, store, runID, "tests/test_otro.py", "test_ok", "PASS", "", "", 0, 1000)
	store.FinishRun(runID)
	a := New(store)
	rt, err := a.analyzeRun(t.Context(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Incidents) != 1 || rt.Incidents[0].Kind != "cascade" {
		t.Fatalf("incidents: %+v", rt.Incidents)
	}
	if low := strings.ToLower(rt.Headline + " " + rt.Summary + " " + rt.Incidents[0].Cause); strings.Contains(low, "environment was probably down") || strings.Contains(low, "ambiente probablemente") {
		t.Fatalf("a cascade is not a down environment: %q / %q / %q", rt.Headline, rt.Summary, rt.Incidents[0].Cause)
	}
}

// Dos errores distintos que empiezan con la línea de excepción encadenada no son el mismo incidente.
func TestChainedExceptionLineDoesNotGroupUnrelatedErrors(t *testing.T) {
	clearEnv(t)
	store, runID := groupingStore(t)
	const chained = "During handling of the above exception, another exception occurred:\n"
	failAt(t, store, runID, "A", chained+"ValueError: fecha inválida", "", 0)
	failAt(t, store, runID, "B", chained+"KeyError: 'paciente'", "", 0)
	got := incidents(t, store, runID)
	if len(got) != 2 || got[0].Title == got[1].Title {
		t.Fatalf("two different errors: %+v", got)
	}
}
