package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/locator"
)

// structSnap: a structural snapshot (nodes) plus the elements of the locator, as the client sends it.
func structSnap(kind, label, extra string) string {
	return `{"url":"https://app/product","title":"Producto","viewport":{"w":1280,"h":860},"page":{"w":1280,"h":900},` +
		`"kind":"` + kind + `","label":"` + label + `",` +
		`"elements":[{"tag":"button","testid":"add-to-cart","testid_attr":"data-testid","text":"Add to cart","x":10,"y":20,"w":100,"h":40,"visible":true}],` +
		`"nodes":[{"i":0,"p":-1,"tag":"body","x":0,"y":0,"w":1280,"h":900,"visible":true,"depth":0,"style":{"backgroundColor":"rgb(246, 244, 239)"}},` +
		`{"i":1,"p":0,"tag":"div","attrs":{"class":"skeleton"},"text":"Cargando…","x":0,"y":0,"w":300,"h":40,"visible":true,"depth":1},` +
		`{"i":2,"p":0,"tag":"button","attrs":{"data-testid":"add-to-cart","onclick":"steal()","value":"x"},"text":"Add to cart","x":10,"y":20,"w":100,"h":40,"visible":true,"depth":1,"tx":12,"ty":8,` +
		`"style":{"color":"rgb(255, 255, 255)","backgroundImage":"url(https://evil.example/t.png)","fontFamily":"\"Segoe UI\", sans-serif"}}` + extra + `]}`
}

func postDOM(t *testing.T, srv *Server, testID int64, body string) map[string]any {
	t.Helper()
	rec := do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/dom", testID), body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST dom: %d %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return out
}

// El snapshot estructural se guarda limpio: solo atributos y estilos de la lista blanca, nada que
// ejecute o cargue algo, y los secretos enmascarados. Los elements del locator siguen igual.
func TestStructuralSnapshotIsStoredClean(t *testing.T) {
	srv, testID := newTestServer(t)
	secret := `,{"i":3,"p":0,"tag":"a","attrs":{"href":"javascript:alert(1)","title":"token=sk-live-abcdefghijklmnopqrstuvwx"},` +
		`"text":"Authorization: Bearer abcdefghijklmnopqrstuvwxyz0123456789","x":0,"y":0,"w":1,"h":1,"visible":true,"depth":1}`
	out := postDOM(t, srv, testID, structSnap("", "", secret))
	if out["kind"] != "fail" || out["nodes"].(float64) != 4 || out["elements"].(float64) != 1 {
		t.Fatalf("answer: %+v", out)
	}
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET dom: %d", rec.Code)
	}
	body := rec.Body.String()
	for _, bad := range []string{"steal()", "onclick", `"value"`, "evil.example", "javascript:", "abcdefghijklmnopqrstuvwxyz0123456789", "sk-live-abcdefghijklmnopqrstuvwx"} {
		if strings.Contains(body, bad) {
			t.Errorf("stored %q: %s", bad, body)
		}
	}
	var snap locator.Snapshot
	json.Unmarshal(rec.Body.Bytes(), &snap)
	b := snap.Nodes[2]
	if b.Attrs["data-testid"] != "add-to-cart" || b.Style["color"] != "rgb(255, 255, 255)" || b.Style["fontFamily"] == "" || b.TX == nil || *b.TX != 12 {
		t.Fatalf("kept what is needed to draw it and select it: %+v", b)
	}
	if snap.Nodes[1].Text != "Cargando…" || snap.Page == nil || snap.Page.H != 900 || len(snap.Elements) != 1 {
		t.Fatalf("structure and elements: %+v", snap)
	}
	// el recomendador de locators sigue usando los elements
	if rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/locator", testID), ""); rec.Code == http.StatusInternalServerError {
		t.Fatalf("locator: %d", rec.Code)
	}
}

// Un árbol que no se sostiene (un padre después de su hijo) se descarta entero; los elements quedan.
func TestBrokenTreeIsDropped(t *testing.T) {
	srv, testID := newTestServer(t)
	bad := `{"elements":[{"tag":"button","text":"Ok","x":1,"y":1,"w":1,"h":1,"visible":true}],` +
		`"nodes":[{"i":0,"p":-1,"tag":"body"},{"i":1,"p":5,"tag":"div"}]}`
	if out := postDOM(t, srv, testID, bad); out["nodes"].(float64) != 0 || out["elements"].(float64) != 1 {
		t.Fatalf("broken tree: %+v", out)
	}
	if out := postDOM(t, srv, testID, `{"nodes":[{"i":0,"p":-1,"tag":"<script>"}]}`); out["nodes"].(float64) != 0 {
		t.Fatalf("invalid tag: %+v", out)
	}
	if rec := do(t, srv, http.MethodGet, "/api/v1/tests/999/dom", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("no snapshot: %d", rec.Code)
	}
}

// Los snapshots por paso se guardan aparte, en orden, sin pisar el del fallo, y con un tope por test.
func TestStepSnapshots(t *testing.T) {
	srv, testID := newTestServer(t)
	postDOM(t, srv, testID, structSnap("fail", "", ""))
	if out := postDOM(t, srv, testID, structSnap("step", "Abrir login", "")); out["seq"].(float64) != 1 {
		t.Fatalf("first step: %+v", out)
	}
	postDOM(t, srv, testID, structSnap("step", "Iniciar sesión", ""))
	var steps []struct {
		Seq   int    `json:"seq"`
		Label string `json:"label"`
		At    int64  `json:"at"`
	}
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/steps", testID), "")
	json.Unmarshal(rec.Body.Bytes(), &steps)
	if len(steps) != 2 || steps[0].Label != "Abrir login" || steps[1].Seq != 2 || steps[0].At == 0 {
		t.Fatalf("steps: %s", rec.Body.String())
	}
	if rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/steps/2", testID), ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"label":"Iniciar sesión"`) {
		t.Fatalf("step 2: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/steps/9", testID), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("missing step: %d", rec.Code)
	}
	if rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), ""); !strings.Contains(rec.Body.String(), `"kind":"fail"`) {
		t.Fatalf("the failure snapshot stays: %s", rec.Body.String())
	}
	for i := 0; i < 45; i++ {
		postDOM(t, srv, testID, structSnap("step", fmt.Sprintf("paso %d", i), ""))
	}
	rec = do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/steps", testID), "")
	json.Unmarshal(rec.Body.Bytes(), &steps)
	if len(steps) != 40 {
		t.Fatalf("bounded: %d steps", len(steps))
	}
}

// Con el cifrado en reposo, los snapshots por paso tampoco quedan legibles en el disco y se leen igual.
func TestStepSnapshotsEncryptedAtRest(t *testing.T) {
	srv, testID := encryptedServer(t)
	postDOM(t, srv, testID, structSnap("step", atRestMarker, ""))
	if hits := grepDir(t, srv.ScreenshotsDir, atRestMarker); len(hits) > 0 {
		t.Fatalf("the step snapshot is readable on disk in %v", hits)
	}
	if rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/steps/1", testID), ""); !strings.Contains(rec.Body.String(), atRestMarker) {
		t.Fatalf("read back: %s", rec.Body.String())
	}
	if keys, _ := srv.Store.DOMKeys(); keys[srv.Vault.KeyID()] != 1 {
		t.Fatalf("counted for the key rotation: %+v", keys)
	}
}

// TRACEREPORTS_DOM_ON=warning: la página de un WARNING se guarda entre las del test, marcada, y no
// reemplaza la del fallo.
func TestWarningSnapshotsAreMarked(t *testing.T) {
	srv, testID := newTestServer(t)
	postDOM(t, srv, testID, structSnap("step", "Abrir la agenda", ""))
	if out := postDOM(t, srv, testID, structSnap("warning", "La grilla tardó 12 s", "")); out["kind"] != "warning" || out["seq"].(float64) != 2 {
		t.Fatalf("warning: %+v", out)
	}
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/steps", testID), "")
	if !strings.Contains(rec.Body.String(), `"label":"Abrir la agenda","at":`) || !strings.Contains(rec.Body.String(), `"kind":"step"`) ||
		!strings.Contains(rec.Body.String(), `"label":"La grilla tardó 12 s"`) || !strings.Contains(rec.Body.String(), `"kind":"warning"`) {
		t.Fatalf("steps: %s", rec.Body.String())
	}
	if rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("un WARNING no es la página del fallo: %d", rec.Code)
	}
}

// Comparar con el último PASS: el mismo test en su última ejecución exitosa del mismo contexto que guardó
// sus páginas, la del mismo paso si existe.
func TestDOMBaselineIsTheLastPassWithPages(t *testing.T) {
	srv, _ := newTestServer(t)
	run := func(status string, steps ...string) int64 {
		rec := do(t, srv, http.MethodPost, "/api/v1/runs", `{"name":"nightly","environment":"qa","project":"box"}`)
		var r struct {
			RunID int64 `json:"run_id"`
		}
		json.Unmarshal(rec.Body.Bytes(), &r)
		rec = do(t, srv, http.MethodPost, fmt.Sprintf("/api/v1/runs/%d/tests", r.RunID), `{"name":"test_guardar","key":"box::test_guardar"}`)
		var tt struct {
			TestID int64 `json:"test_id"`
		}
		json.Unmarshal(rec.Body.Bytes(), &tt)
		for _, s := range steps {
			postDOM(t, srv, tt.TestID, structSnap("step", s, ""))
		}
		do(t, srv, http.MethodPatch, fmt.Sprintf("/api/v1/tests/%d/finish", tt.TestID), `{"status":"`+status+`"}`)
		return tt.TestID
	}
	first := run("FAIL")
	if rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/baseline", first), ""); rec.Code != http.StatusNoContent {
		t.Fatalf("sin un PASS antes: %d", rec.Code)
	}
	pass := run("PASS", "Abrir la ficha", "Guardar")
	run("PASS") // un PASS más nuevo pero sin páginas: se salta
	failed := run("FAIL", "Guardar")
	var base struct {
		TestID   int64           `json:"test_id"`
		Seq      int             `json:"seq"`
		Label    string          `json:"label"`
		SameStep bool            `json:"same_step"`
		Snapshot json.RawMessage `json:"snapshot"`
	}
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/baseline?label=%s", failed, "Abrir%20la%20ficha"), "")
	if json.Unmarshal(rec.Body.Bytes(), &base); rec.Code != 200 || base.TestID != pass || base.Seq != 1 || !base.SameStep || !strings.Contains(string(base.Snapshot), `"nodes"`) {
		t.Fatalf("baseline: %d %s", rec.Code, rec.Body)
	}
	rec = do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom/baseline?label=otro", failed), "")
	if json.Unmarshal(rec.Body.Bytes(), &base); base.Seq != 2 || base.Label != "Guardar" || base.SameStep {
		t.Fatalf("sin el mismo paso, la última página: %s", rec.Body)
	}
}

// Las capas y el recorte del Inspector: zIndex y overflow pasan la lista blanca; un valor con url() no.
func TestLayerStylesAreKept(t *testing.T) {
	srv, testID := newTestServer(t)
	postDOM(t, srv, testID, structSnap("fail", "", `,{"i":3,"p":0,"tag":"div","attrs":{"class":"modal"},"x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1,`+
		`"style":{"position":"fixed","zIndex":"1050","overflowX":"hidden","overflowY":"auto","opacity":"0.5"}},`+
		`{"i":4,"p":0,"tag":"div","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1,"style":{"zIndex":"url(x)"}}`))
	body := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "").Body.String()
	for _, want := range []string{`"zIndex":"1050"`, `"overflowX":"hidden"`, `"overflowY":"auto"`, `"opacity":"0.5"`} {
		if !strings.Contains(body, want) {
			t.Errorf("falta %s", want)
		}
	}
	if strings.Contains(body, "url(x)") {
		t.Error("un valor con url() no pasa")
	}
}

// Una ficha de cliente muestra RUT, teléfonos y correos en texto plano: el snapshot se guarda sin ellos
// (texto, atributos de texto, elements del recomendador y URL). Los atributos de los locators quedan.
func TestDOMSnapshotMasksPersonalData(t *testing.T) {
	srv, testID := newTestServer(t)
	postDOM(t, srv, testID, `{"url":"https://app/ficha?mail=ana@correo.cl","viewport":{"w":800,"h":600},`+
		`"elements":[{"tag":"span","id":"rut-13.905.701-5","text":"RUN 13.905.701-5","x":0,"y":0,"w":1,"h":1,"visible":true}],`+
		`"nodes":[{"i":0,"p":-1,"tag":"body","x":0,"y":0,"w":800,"h":600,"visible":true,"depth":0},`+
		`{"i":1,"p":0,"tag":"span","attrs":{"id":"tel","title":"Llamar al +56 9 1234 5678"},"text":"Teléfono móvil +56 (9) 58720108","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1},`+
		`{"i":2,"p":0,"tag":"td","text":"06-11-2026 17:26 · RUN 13.905.701-5 · ana@correo.cl","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1}]}`)
	body := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "").Body.String()
	body = strings.NewReplacer("\\u003c", "<", "\\u003e", ">").Replace(body) // el JSON escribe < y > escapados
	for _, gone := range []string{"58720108", "1234 5678", "ana@correo.cl", "RUN 13.905.701-5"} {
		if strings.Contains(body, gone) {
			t.Errorf("quedó %q", gone)
		}
	}
	for _, kept := range []string{"06-11-2026 17:26", "<telefono>", "<rut>", "<correo>", `"id":"tel"`, `"id":"rut-13.905.701-5"`} {
		if !strings.Contains(body, kept) {
			t.Errorf("falta %q", kept)
		}
	}
}

// TRACEREPORTS_REDACT_PII=off deja los datos como vienen (los secretos se siguen enmascarando).
func TestDOMPersonalDataMaskCanBeTurnedOff(t *testing.T) {
	t.Setenv("TRACEREPORTS_REDACT_PII", "off")
	srv, testID := newTestServer(t)
	postDOM(t, srv, testID, structSnap("fail", "", `,{"i":3,"p":0,"tag":"td","text":"RUN 13.905.701-5","x":0,"y":0,"w":1,"h":1,"visible":true,"depth":1}`))
	if body := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "").Body.String(); !strings.Contains(body, "13.905.701-5") {
		t.Fatal("con off el RUT queda")
	}
}

// Pestañas verticales: writing-mode, y el giro de una caja (rotate) como matriz sin traslación con su
// tamaño de layout. Lo que no es una matriz de números se descarta, y la caja queda como estaba.
func TestTurnedBoxesAreKept(t *testing.T) {
	srv, testID := newTestServer(t)
	postDOM(t, srv, testID, structSnap("fail", "", `,{"i":3,"p":0,"tag":"div","text":"Historia","x":0,"y":0,"w":47,"h":105,"visible":true,"depth":1,"style":{"writingMode":"vertical-rl"}},`+
		`{"i":4,"p":0,"tag":"div","text":"Formularios","x":0,"y":0,"w":24,"h":90,"visible":true,"depth":1,"tf":"matrix(0, 1, -1, 0, 0, 0)","lw":90,"lh":24},`+
		`{"i":5,"p":0,"tag":"div","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1,"tf":"matrix(1, 0, 0, 1, 0, 0); background:url(x)","lw":10,"lh":10},`+
		`{"i":6,"p":0,"tag":"div","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1,"tf":"matrix(0, 1, -1, 0, 0, 0)","lw":0,"lh":10},`+
		`{"i":7,"p":0,"tag":"div","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1,"tf":"matrix(0, 1, -1, 0, 500, 500)","lw":10,"lh":10}`))
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "")
	var snap locator.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Nodes[3].Style["writingMode"] != "vertical-rl" {
		t.Errorf("writing-mode: %+v", snap.Nodes[3].Style)
	}
	if n := snap.Nodes[4]; n.TF != "matrix(0, 1, -1, 0, 0, 0)" || n.LW != 90 || n.LH != 24 {
		t.Errorf("turned box: %+v", n)
	}
	for _, i := range []int{5, 6, 7} {
		if n := snap.Nodes[i]; n.TF != "" || n.LW != 0 || n.LH != 0 {
			t.Errorf("node %d: not a plain matrix with a size, dropped: %+v", i, n)
		}
	}
}

// Bordes de cada lado, sombras, degradados y el corte de los textos largos viajan; una imagen de fondo
// (url()) no, ni algo que no sea un degradado.
func TestBoxStylesAreKept(t *testing.T) {
	srv, testID := newTestServer(t)
	shadow := "rgba(0, 0, 0, 0.2) 0px 1px 3px 0px, rgba(0, 0, 0, 0.14) 0px 1px 1px 0px, rgba(0, 0, 0, 0.12) 0px 2px 1px -1px"
	postDOM(t, srv, testID, structSnap("fail", "", `,{"i":3,"p":0,"tag":"div","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1,"style":{`+
		`"borderBottomWidth":"2px","borderBottomColor":"rgb(51, 117, 194)","borderLeftWidth":"0px","borderRightWidth":"0px",`+
		`"boxShadow":"`+shadow+`","backgroundImage":"linear-gradient(90deg, rgb(243, 243, 243) 0%, rgb(236, 235, 235) 100%)",`+
		`"textOverflow":"ellipsis","whiteSpace":"nowrap"}},`+
		`{"i":4,"p":0,"tag":"div","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1,"style":{"backgroundImage":"linear-gradient(red, blue), url(https://evil.example/x.png)"}},`+
		`{"i":5,"p":0,"tag":"div","x":0,"y":0,"w":10,"h":10,"visible":true,"depth":1,"style":{"backgroundImage":"image-set(x 1x)"}}`))
	rec := do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/tests/%d/dom", testID), "")
	var snap locator.Snapshot
	if err := json.Unmarshal(rec.Body.Bytes(), &snap); err != nil {
		t.Fatal(err)
	}
	st := snap.Nodes[3].Style
	for k, want := range map[string]string{"borderBottomWidth": "2px", "borderBottomColor": "rgb(51, 117, 194)", "borderLeftWidth": "0px",
		"boxShadow": shadow, "backgroundImage": "linear-gradient(90deg, rgb(243, 243, 243) 0%, rgb(236, 235, 235) 100%)",
		"textOverflow": "ellipsis", "whiteSpace": "nowrap"} {
		if st[k] != want {
			t.Errorf("%s = %q, want %q", k, st[k], want)
		}
	}
	for _, i := range []int{4, 5} {
		if v, ok := snap.Nodes[i].Style["backgroundImage"]; ok {
			t.Errorf("node %d: only a gradient without url() is kept, got %q", i, v)
		}
	}
}
