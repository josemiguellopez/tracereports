package api

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

const (
	maxArtifactBody = 100 << 20 // 100 MiB por trace o video
	// traceViewer is the official Playwright Trace Viewer: it fetches trace.zip cross-origin.
	traceViewer = "https://trace.playwright.dev"
)

// artifactKinds: only what the report knows how to show, validated by content (no HTML or
// scripts can be uploaded and then served from the report's origin).
var artifactKinds = map[string]func(head []byte) (ext string, ok bool){
	"trace": func(head []byte) (string, bool) { return ".zip", bytes.HasPrefix(head, []byte("PK\x03\x04")) },
	"video": func(head []byte) (string, bool) {
		switch http.DetectContentType(head) {
		case "video/webm":
			return ".webm", true
		case "video/mp4":
			return ".mp4", true
		}
		return "", false
	},
}

// uploadArtifact stores the Playwright trace (kind=trace, a .zip) or the video (kind=video, WebM
// or MP4) of a test: multipart field "file", plus "kind" and an optional "name".
func (s *Server) uploadArtifact(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxArtifactBody+(1<<20))
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		importBodyError(w, fmt.Errorf("invalid multipart form: %w", err), maxArtifactBody)
		return
	}
	defer r.MultipartForm.RemoveAll()
	kind := r.FormValue("kind")
	check, known := artifactKinds[kind]
	if !known {
		writeError(w, http.StatusBadRequest, `kind must be "trace" (Playwright trace.zip) or "video" (WebM or MP4)`)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, `multipart field "file" is required`)
		return
	}
	defer file.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(file, head)
	head = head[:n]
	ext, valid := check(head)
	if !valid {
		writeError(w, http.StatusUnsupportedMediaType, "the file is not a "+kind+" ("+map[string]string{"trace": "ZIP", "video": "WebM or MP4"}[kind]+")")
		return
	}
	if _, err := s.Store.GetTest(id); respondErr(w, err, "test") {
		return
	}
	name := fmt.Sprintf("a%d_%d%s", id, time.Now().UnixNano(), ext)
	path := filepath.Join(s.ScreenshotsDir, name)
	out, err := os.Create(path)
	if err != nil {
		serverError(w, err)
		return
	}
	size, err := io.Copy(out, io.MultiReader(bytes.NewReader(head), file))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
		serverError(w, err)
		return
	}
	label := clean(s.label(r.FormValue("name")), 200)
	if label == "" {
		label = kind + ext
	}
	a := &db.Artifact{TestID: id, Kind: kind, Name: label, URL: "/screenshots/" + name, Size: size}
	if err := s.Store.AddArtifact(a); err != nil {
		os.Remove(path)
		serverError(w, err)
		return
	}
	s.publishTest("artifact", id, a)
	writeJSON(w, http.StatusCreated, a)
}

// withTraceViewerCORS lets the official Trace Viewer download a trace.zip from the report
// (it fetches it from the browser, cross-origin). Nothing else gets CORS.
func withTraceViewerCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Origin") == traceViewer && strings.HasSuffix(r.URL.Path, ".zip") {
			w.Header().Set("Access-Control-Allow-Origin", traceViewer)
			w.Header().Set("Vary", "Origin")
		}
		next.ServeHTTP(w, r)
	})
}
