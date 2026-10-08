package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/unicode/norm"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

// bodyFileMinChars: bodies above it get their own file in the ZIP, so the UI can point to it
// when its 20 KB preview (MAX_PREVIEW in app.js) is cut. It is lower than 20 KB because the
// preview is cut on pretty-printed JSON, which is larger than the raw body.
const bodyFileMinChars = 4000

// staticDataMarker is replaced in index.html by the <script> that loads the exported data.
const staticDataMarker = "<!-- tracereports:static-data -->"

const exportReadme = `TraceReports - exported report / reporte exportado
=====================================================

EN
1. Unzip the whole file (do not open the HTML from inside the ZIP).
2. Open index.html in any browser (Chrome, Edge, Firefox).

The report works without a server and without internet: charts and fonts are included.
Switch the language under Settings.

ES
1. Descomprime el ZIP completo (no abras el HTML desde dentro del ZIP).
2. Abre index.html con cualquier navegador (Chrome, Edge, Firefox).

El reporte funciona sin servidor y sin internet: los graficos y las fuentes van
incluidos. El idioma se cambia en Ajustes.
`

// maxExportArtifacts caps the traces and videos copied into an exported ZIP (the rest stay on the
// server).
const maxExportArtifacts = 100 << 20

// exportRun streams a self-contained ZIP (HTML + data + screenshots) of a run,
// viewable offline by opening index.html — meant to be shared by email.
func (s *Server) exportRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	detail, err := s.Store.GetRunDetail(id)
	if respondErr(w, err, "run") {
		return
	}
	if err := s.decorate(id, detail.Tests); err != nil {
		serverError(w, err)
		return
	}

	tests := make(map[int64]*db.Test, len(detail.Tests))
	// la red de cada test se lee y se escribe en el ZIP de a un test (ver más abajo), no toda junta
	var withNetwork []int64
	var shots []string
	var artifactBytes int64
	for _, summary := range detail.Tests {
		t, err := s.Store.GetTest(summary.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		t.Owner, t.Verdict, t.PreviousVerdict = summary.Owner, summary.Verdict, summary.PreviousVerdict
		for i := range t.Logs {
			if name := screenshotFile(t.Logs[i].Screenshot); name != "" {
				t.Logs[i].Screenshot = "screenshots/" + name // relative: works from file://
				shots = append(shots, name)
			}
		}
		// traces y videos, mientras quepan: un ZIP para adjuntar a un correo no puede pesar 1 GB
		kept := t.Artifacts[:0]
		for _, a := range t.Artifacts {
			if name := screenshotFile(a.URL); name != "" && artifactBytes+a.Size <= maxExportArtifacts {
				artifactBytes += a.Size
				a.URL = "screenshots/" + name
				shots = append(shots, name)
				kept = append(kept, a)
			}
		}
		t.Artifacts = kept
		tests[t.ID] = t
		if t.NetworkTotal > 0 {
			withNetwork = append(withNetwork, t.ID)
		}
	}

	// Historial, comparación y endpoints: el reporte exportado los muestra sin servidor.
	history := make(map[int64][]db.HistoryEntry, len(tests))
	for id, t := range tests {
		if history[id], err = s.Store.TestHistory(t.Key, t.RunID, historyLimit); err != nil {
			serverError(w, err)
			return
		}
	}
	comparison, err := s.Store.CompareRuns(id, 0)
	if err != nil {
		serverError(w, err)
		return
	}
	endpoints, err := s.Store.RunEndpoints(id, 30)
	if err != nil {
		serverError(w, err)
		return
	}
	locators := map[int64]*LocatorReport{}
	drifts := map[int64][]db.EndpointDrift{}
	for _, summary := range detail.Tests {
		if summary.Status == "FAIL" {
			// los pasos de tests[] ya apuntan a "screenshots/<archivo>" (relativo al ZIP)
			if rep, err := s.locatorReport(tests[summary.ID]); err == nil && rep != nil {
				locators[summary.ID] = rep
			}
		}
		if summary.NetDrift != nil {
			if eps, err := s.Store.TestDrift(summary.ID); err == nil {
				drifts[summary.ID] = eps
			}
		}
	}

	data, err := json.Marshal(map[string]any{
		"config":      map[string]any{"ai_enabled": s.AI.Enabled(), "ai_model": s.AI.Model(), "ai_provider": s.AI.Provider(), "static": true},
		"run":         detail,
		"tests":       tests,
		"history":     history,
		"compare":     comparison,
		"endpoints":   endpoints,
		"locators":    locators,
		"drifts":      drifts,
		"exported_at": db.NowMs(),
	})
	if err != nil {
		serverError(w, err)
		return
	}
	if data, err = redactJSON(s.redactor(), data); err != nil {
		serverError(w, err)
		return
	}
	index, err := fs.ReadFile(s.Web, "index.html")
	if err != nil {
		serverError(w, err)
		return
	}
	index = bytes.Replace(index, []byte(staticDataMarker), []byte(`<script src="data.js"></script>`), 1)

	folder := fmt.Sprintf("tracereports_%s_%s_run%d",
		slug(detail.Name), time.UnixMilli(detail.StartedAt).Format("20060102_1504"), detail.ID)

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.zip"`, folder))

	// From here on the response is streaming: errors can only be logged.
	zw := zip.NewWriter(w)
	add := func(name string, content []byte, method uint16) {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: folder + "/" + name, Method: method, Modified: time.Now()})
		if err == nil {
			_, err = fw.Write(content)
		}
		if err != nil {
			slog.Error("export: write zip entry", "run_id", id, "entry", name, "err", err)
		}
	}

	add("README.txt", []byte(exportReadme), zip.Deflate)
	add("index.html", index, zip.Deflate)
	for _, asset := range []string{"style.css", "themes.css", "features.css", "i18n.js", "i18n.en.js", "tracereports_features.js", "app.js"} {
		content, err := fs.ReadFile(s.Web, asset)
		if err != nil {
			slog.Error("export: read asset", "asset", asset, "err", err)
			continue
		}
		add(asset, content, zip.Deflate)
	}
	// dependencias locales (Chart.js, fuentes y sus licencias): el ZIP funciona sin internet
	_ = fs.WalkDir(s.Web, "vendor", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		content, err := fs.ReadFile(s.Web, path)
		if err != nil {
			slog.Error("export: read vendor asset", "asset", path, "err", err)
			return nil
		}
		method := uint16(zip.Deflate)
		if strings.HasSuffix(path, ".woff2") {
			method = zip.Store // ya comprimido
		}
		add(path, content, method)
		return nil
	})
	add("data.js", append(append([]byte("window.TRACEREPORTS_STATIC = "), data...), ";\n"...), zip.Deflate)
	data = nil
	// la red, de a un test: en memoria solo la del test que se está escribiendo
	for _, testID := range withNetwork {
		js, bodies, err := s.exportNetwork(testID)
		if err != nil {
			slog.Error("export: network", "run_id", id, "test_id", testID, "err", err)
			continue
		}
		add(fmt.Sprintf("network/test_%d.js", testID), js, zip.Deflate)
		for _, b := range bodies {
			add(b.name, b.content, zip.Deflate)
		}
	}
	// capturas, traces y videos: se copian del disco al ZIP sin cargarlos enteros en memoria
	for _, name := range shots {
		f, err := os.Open(filepath.Join(s.ScreenshotsDir, name))
		if err != nil {
			slog.Warn("export: screenshot missing", "file", name, "err", err)
			continue
		}
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: folder + "/screenshots/" + name, Method: zip.Store, Modified: time.Now()}) // ya comprimidas
		if err == nil {
			_, err = io.Copy(fw, f)
		}
		f.Close()
		if err != nil {
			slog.Error("export: write zip entry", "run_id", id, "entry", name, "err", err)
		}
	}
	if err := zw.Close(); err != nil {
		slog.Error("export: close zip", "run_id", id, "err", err)
	}
}

type exportBody struct {
	name    string
	content []byte
}

// exportNetwork builds network/test_<id>.js of one test and the files of its large bodies
// (network/bodies/<id>.<ext>), with the secrets masked again (data stored before the central
// redaction does not leave in the ZIP either).
func (s *Server) exportNetwork(testID int64) ([]byte, []exportBody, error) {
	conns, err := s.Store.ListNetwork(testID)
	if err != nil {
		return nil, nil, err
	}
	s.linkCalls(conns)
	var bodies []exportBody
	for i := range conns {
		if len(conns[i].ResponseBody) > bodyFileMinChars {
			ext, _ := bodyFileType(&conns[i])
			conns[i].BodyFile = fmt.Sprintf("network/bodies/%d%s", conns[i].ID, ext)
			bodies = append(bodies, exportBody{conns[i].BodyFile, []byte(s.redactor().Text(conns[i].ResponseBody))})
		}
	}
	js, err := json.Marshal(conns)
	if err != nil {
		return nil, nil, err
	}
	if js, err = redactJSON(s.redactor(), js); err != nil {
		return nil, nil, err
	}
	return []byte(fmt.Sprintf("(window.TRACEREPORTS_NET = window.TRACEREPORTS_NET || {})[%d] = %s;\n", testID, js)), bodies, nil
}

// redactJSON masks the secrets of an exported JSON document string by string, before it is
// written: evidence stored before the central redaction (or a body that is itself JSON) sits in
// it as an escaped string, which a pass over the serialized text does not look into. Every
// string value goes through the policy, and the value of a sensitive key or header is masked.
// Numbers are kept exact (UseNumber) and the structure and types do not change.
func redactJSON(p *redact.Policy, data []byte) ([]byte, error) {
	if !p.Enabled() {
		return data, nil
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.Marshal(redactValue(p, v))
}

func redactValue(p *redact.Policy, v any) any {
	switch x := v.(type) {
	case string:
		return p.Text(x)
	case []any:
		for i := range x {
			x[i] = redactValue(p, x[i])
		}
	case map[string]any:
		for k, val := range x {
			if str, ok := val.(string); ok && str != "" && (p.SensitiveKey(k) || p.SensitiveHeader(k)) {
				x[k] = redact.Mask
				continue
			}
			x[k] = redactValue(p, val)
		}
	}
	return v
}

// screenshotFile extracts a safe file name from a "/screenshots/<file>" URL.
func screenshotFile(url string) string {
	name := strings.TrimPrefix(url, "/screenshots/")
	if name == "" || name == url || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return ""
	}
	return name
}

// slug turns "Regresión Web - Checkout" into "regresion_web_checkout".
func slug(s string) string {
	var b strings.Builder
	for _, r := range norm.NFD.String(strings.ToLower(s)) {
		switch {
		case unicode.Is(unicode.Mn, r): // drop accents
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)
		case b.Len() > 0 && !strings.HasSuffix(b.String(), "_"):
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 40 {
		out = strings.TrimRight(out[:40], "_")
	}
	if out == "" {
		return "reporte"
	}
	return out
}
