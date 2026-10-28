package api

import (
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/atrest"
)

// shots is the screenshots folder through the Vault (TRACEREPORTS_ENCRYPT_AT_REST): every read
// and write of evidence files goes through it.
func (s *Server) shots() atrest.Dir {
	return atrest.Dir{Path: s.ScreenshotsDir, Vault: s.Vault}
}

// serveEvidence serves /screenshots/<file>, decrypting it when it is encrypted: Range requests still
// work, so a video can be scrubbed without decrypting it whole. Old plain files are served too.
func (s *Server) serveEvidence() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Path
		if strings.ContainsAny(name, `/\`) {
			http.NotFound(w, r)
			return
		}
		f, err := s.shots().Open(name)
		switch {
		case errors.Is(err, os.ErrNotExist):
			http.NotFound(w, r)
			return
		case err != nil:
			// sin la clave que la abre, o alterada: se registra sin contenido y no se sirve
			slog.Error("evidence: cannot read", "file", name, "err", err)
			writeError(w, http.StatusInternalServerError, "the evidence cannot be read with the configured key")
			return
		}
		defer f.Close()
		http.ServeContent(w, r, name, f.ModTime(), f)
	})
}
