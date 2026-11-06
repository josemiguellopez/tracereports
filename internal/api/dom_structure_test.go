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
