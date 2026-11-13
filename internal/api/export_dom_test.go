package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// exportFiles exports run 1 and returns the ZIP's files by path (without the root folder).
func exportFiles(t *testing.T, srv *Server, runID int64) map[string]string {
	t.Helper()
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/v1/runs/%d/export", runID), nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("export: %d %s", rec.Code, rec.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name[strings.Index(f.Name, "/")+1:]] = string(b)
	}
	return files
}

type domIndex map[string]struct {
	Fail *struct {
		Included bool `json:"included"`
	} `json:"fail"`
	Steps []struct {
		Seq      int    `json:"seq"`
		Kind     string `json:"kind"`
		Label    string `json:"label"`
		Included bool   `json:"included"`
	} `json:"steps"`
	Omitted int `json:"omitted"`
}

func readDOMIndex(t *testing.T, js string) domIndex {
	t.Helper()
	var idx domIndex
	body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(js), "window.TRACEREPORTS_DOM_INDEX = "), ";")
	if err := json.Unmarshal([]byte(body), &idx); err != nil {
		t.Fatalf("dom/index.js: %v\n%s", err, js)
	}
	return idx
}

// El ZIP exportado trae las páginas del Inspector (antes ninguna: el reporte sin servidor no lo mostraba):
// cada una en dom/test_<id>_<seq|fail>.js, como la red, para abrirlo desde file://, y su índice.
func TestExportIncludesTheInspectorPages(t *testing.T) {
	srv, testID := newTestServer(t)
	run, _ := srv.Store.TestRunID(testID)
	postDOM(t, srv, testID, structSnap("step", "Abrir la ficha", ""))
	postDOM(t, srv, testID, structSnap("warning", "La grilla tardó 12 s", ""))
	postDOM(t, srv, testID, structSnap("fail", "", ""))
	files := exportFiles(t, srv, run)
	for _, want := range []string{fmt.Sprintf("dom/test_%d_fail.js", testID), fmt.Sprintf("dom/test_%d_1.js", testID), fmt.Sprintf("dom/test_%d_2.js", testID), "dom/index.js"} {
		if !strings.HasPrefix(files[want], "(window.TRACEREPORTS_DOM") && want != "dom/index.js" {
			t.Errorf("falta %s", want)
		}
	}
	page := files[fmt.Sprintf("dom/test_%d_1.js", testID)]
	if !strings.Contains(page, fmt.Sprintf(`["%d/1"] = {`, testID)) || !strings.Contains(page, `"nodes":[`) {
		t.Fatalf("la página del paso: %.200s", page)
	}
	idx := readDOMIndex(t, files["dom/index.js"])[fmt.Sprint(testID)]
	if idx.Fail == nil || !idx.Fail.Included || len(idx.Steps) != 2 || !idx.Steps[0].Included || idx.Steps[1].Kind != "warning" || idx.Steps[0].Label != "Abrir la ficha" || idx.Omitted != 0 {
		t.Fatalf("índice: %+v", idx)
	}
}

// Con el tope: primero la página del fallo, después la del WARNING, después los pasos; lo que no entra
// queda contado en el índice para avisarlo en el reporte.
func TestExportDOMKeepsFailAndWarningFirst(t *testing.T) {
	srv, testID := newTestServer(t)
	run, _ := srv.Store.TestRunID(testID)
	postDOM(t, srv, testID, structSnap("step", "paso 1", ""))
	postDOM(t, srv, testID, structSnap("step", "paso 2", ""))
	postDOM(t, srv, testID, structSnap("warning", "lento", ""))
	postDOM(t, srv, testID, structSnap("fail", "", ""))
	fail, _ := srv.Store.GetDOM(testID)
	warning, _ := srv.Store.GetDOMStep(testID, 3)
	old := maxExportDOM
	maxExportDOM = len(fail) + len(warning) + 5 // entran justo la del fallo y la del WARNING
	t.Cleanup(func() { maxExportDOM = old })
	files := exportFiles(t, srv, run)
	idx := readDOMIndex(t, files["dom/index.js"])[fmt.Sprint(testID)]
	got := map[string]bool{"fail": idx.Fail.Included}
	for _, s := range idx.Steps {
		got[s.Label] = s.Included
	}
	if !got["fail"] || !got["lento"] || got["paso 1"] || got["paso 2"] || idx.Omitted != 2 {
		t.Fatalf("prioridad: %+v (omitidas %d)", got, idx.Omitted)
	}
	if _, ok := files[fmt.Sprintf("dom/test_%d_1.js", testID)]; ok {
		t.Error("una página que no entró no va en el ZIP")
	}
}

// Un run sin páginas no lleva dom/.
func TestExportWithoutDOM(t *testing.T) {
	srv, testID := newTestServer(t)
	run, _ := srv.Store.TestRunID(testID)
	for name := range exportFiles(t, srv, run) {
		if strings.HasPrefix(name, "dom/") {
			t.Fatalf("sin páginas no hay dom/: %s", name)
		}
	}
}
