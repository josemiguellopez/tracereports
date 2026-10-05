package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
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

	tests := make(map[int64]*db.Test, len(detail.Tests))
	network := map[int64][]byte{} // test_id -> network/test_<id>.js (loaded on demand by the UI)
	bodies := map[string][]byte{} // network/bodies/<id>.<ext> for bodies larger than the UI preview
	var shots []string
	for _, summary := range detail.Tests {
		t, err := s.Store.GetTest(summary.ID)
		if err != nil {
			serverError(w, err)
			return
		}
		for i := range t.Logs {
			if name := screenshotFile(t.Logs[i].Screenshot); name != "" {
				t.Logs[i].Screenshot = "screenshots/" + name // relative: works from file://
				shots = append(shots, name)
			}
		}
		tests[t.ID] = t
		if t.NetworkTotal > 0 {
			conns, err := s.Store.ListNetwork(t.ID)
			if err != nil {
				serverError(w, err)
				return
			}
			for i := range conns {
				if len(conns[i].ResponseBody) > bodyFileMinChars {
					ext, _ := bodyFileType(&conns[i])
					conns[i].BodyFile = fmt.Sprintf("network/bodies/%d%s", conns[i].ID, ext)
					bodies[conns[i].BodyFile] = []byte(s.redactor().Text(conns[i].ResponseBody))
				}
			}
			js, err := json.Marshal(conns)
			if err != nil {
				serverError(w, err)
				return
			}
			// segunda capa: datos guardados antes de la redacción central tampoco salen en el ZIP
			network[t.ID] = []byte(fmt.Sprintf("(window.TRACEREPORTS_NET = window.TRACEREPORTS_NET || {})[%d] = %s;\n", t.ID, s.redactor().Text(string(js))))
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
	data = []byte(s.redactor().Text(string(data)))
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
	for testID, js := range network {
		add(fmt.Sprintf("network/test_%d.js", testID), js, zip.Deflate)
	}
	for name, body := range bodies {
		add(name, body, zip.Deflate)
	}
	for _, name := range shots {
		content, err := os.ReadFile(filepath.Join(s.ScreenshotsDir, name))
		if err != nil {
			slog.Warn("export: screenshot missing", "file", name, "err", err)
			continue
		}
		add("screenshots/"+name, content, zip.Store) // images are already compressed
	}
	if err := zw.Close(); err != nil {
		slog.Error("export: close zip", "run_id", id, "err", err)
	}
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
