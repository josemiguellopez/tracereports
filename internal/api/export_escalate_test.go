package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

// El reporte exportado trae Escalar y Release ya calculados, para verlos sin servidor: el resumen
// de la ejecución y de cada test fallido, en los tres públicos y los dos idiomas, con la captura
// del fallo dentro del ZIP, y la decisión de salida.
func TestExportCarriesEscalationsAndRelease(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	run, test := failingRun(t, srv)
	if err := os.WriteFile(filepath.Join(srv.ScreenshotsDir, "pago.png"), []byte("\x89PNG fake"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := call(t, srv, "GET", "/api/v1/runs/"+itoa(run)+"/export", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		r, _ := f.Open()
		raw, _ := io.ReadAll(r)
		r.Close()
		files[f.Name[strings.Index(f.Name, "/")+1:]] = raw
	}
	js := strings.TrimSuffix(strings.TrimPrefix(string(files["data.js"]), "window.TRACEREPORTS_STATIC = "), ";\n")
	var data struct {
		Escalations map[string]*ai.Escalation `json:"escalations"`
		Release     *struct {
			Decision string `json:"decision"`
		} `json:"release"`
	}
	if err := json.Unmarshal([]byte(js), &data); err != nil {
		t.Fatal(err)
	}
	for _, target := range []int64{0, test} {
		for _, aud := range ai.Audiences {
			for _, lang := range []string{"es", "en"} {
				key := itoa(target) + ":" + aud + ":" + lang + ":tpl"
				e := data.Escalations[key]
				if e == nil || e.Source != "template" || e.Headline == "" {
					t.Fatalf("missing %s: %+v", key, e)
				}
			}
		}
	}
	e := data.Escalations[itoa(test)+":dev:en:tpl"]
	if e.Facts.Screenshot != "screenshots/pago.png" || files["screenshots/pago.png"] == nil {
		t.Fatalf("the failure screenshot travels inside the ZIP: %q", e.Facts.Screenshot)
	}
	if len(e.Facts.Dev) == 0 {
		t.Fatal("the developer detail goes too")
	}
	if data.Release == nil || data.Release.Decision == "" {
		t.Fatalf("release decision: %+v", data.Release)
	}
}
