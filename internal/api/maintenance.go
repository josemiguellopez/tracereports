package api

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
)

// Mantenimiento: las ejecuciones con problemas de todos los proyectos, el cierre o borrado en lote
// y el umbral del cierre automático por inactividad.

// IdleWarn: an open run without activity for this long shows as "sin actividad" (the same as the
// interface: IDLE_WARN_MS in app.js).
const IdleWarn = 5 * time.Minute

// settingStaleHours stores the inactivity threshold chosen in the interface.
const settingStaleHours = "stale_run_hours"

// maxStaleHours bounds the inactivity threshold (30 days).
const maxStaleHours = 720

// StaleRunHours is how many hours without activity close an open run (0 = never) and where it
// comes from: "env" (TRACEREPORTS_STALE_RUN_HOURS, which wins), "ui" (Mantenimiento) or "default"
// (24). It is read on every pass of the closer, so a change in the interface applies without a
// restart.
func (s *Server) StaleRunHours() (float64, string) {
	if v := strings.TrimSpace(env.Get("STALE_RUN_HOURS")); v != "" {
		if h, err := strconv.ParseFloat(v, 64); err == nil && h >= 0 {
			return h, "env"
		}
	}
	if saved, err := s.Store.Settings(); err == nil {
		if h, err := strconv.ParseFloat(saved[settingStaleHours], 64); err == nil && h >= 0 {
			return h, "ui"
		}
	}
	return 24, "default"
}

// maintenance lists the runs with problems (GET /api/v1/maintenance) with the inactivity setting.
func (s *Server) maintenance(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := db.MaintenanceFilter{State: q.Get("state"), Project: q.Get("project"), Environment: q.Get("environment"),
		Branch: q.Get("branch"), IdleBefore: time.Now().Add(-IdleWarn).UnixMilli()}
	if v := q.Get("older_than_hours"); v != "" {
		h, err := strconv.ParseFloat(v, 64)
		if err != nil || h <= 0 {
			writeError(w, http.StatusBadRequest, "older_than_hours must be a positive number")
			return
		}
		f.StartedBefore = time.Now().Add(-time.Duration(h * float64(time.Hour))).UnixMilli()
	}
	runs, err := s.Store.MaintenanceRuns(f)
	if errors.Is(err, db.ErrBadMaintenanceState) {
		writeError(w, http.StatusBadRequest, "state must be one of idle, running, incomplete, manual, auto")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	hours, source := s.StaleRunHours()
	editable, _ := s.settingsAccess(r)
	writeJSON(w, http.StatusOK, map[string]any{"runs": runs, "idle_warn_ms": IdleWarn.Milliseconds(),
		"stale": map[string]any{"hours": hours, "source": source, "editable": editable && source != "env"}})
}

// setStaleHours changes the inactivity threshold from the interface (0 = never close).
func (s *Server) setStaleHours(w http.ResponseWriter, r *http.Request) {
	if !s.guardSettingsWrite(w, r) {
		return
	}
	var in struct {
		Hours *float64 `json:"hours"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Hours == nil || *in.Hours < 0 || *in.Hours > maxStaleHours {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("hours must be between 0 (never) and %d", maxStaleHours))
		return
	}
	if _, source := s.StaleRunHours(); source == "env" {
		writeError(w, http.StatusConflict, "set by TRACEREPORTS_STALE_RUN_HOURS: change it there")
		return
	}
	if err := s.Store.SaveSettings(map[string]string{settingStaleHours: strconv.FormatFloat(*in.Hours, 'f', -1, 64)}); err != nil {
		serverError(w, err)
		return
	}
	hours, source := s.StaleRunHours()
	writeJSON(w, http.StatusOK, map[string]any{"hours": hours, "source": source, "editable": true})
}

// bulkRunIDs reads {"run_ids": [...]} (1 to db.MaxMaintenanceRuns ids) and the optional reason.
func bulkRunIDs(w http.ResponseWriter, r *http.Request) ([]int64, string, bool) {
	var in struct {
		RunIDs []int64 `json:"run_ids"`
		Reason string  `json:"reason"`
	}
	if !decode(w, r, &in) {
		return nil, "", false
	}
	if len(in.RunIDs) == 0 || len(in.RunIDs) > db.MaxMaintenanceRuns {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("run_ids must have between 1 and %d runs", db.MaxMaintenanceRuns))
		return nil, "", false
	}
	return in.RunIDs, in.Reason, true
}

// closeRunsByHand closes several RUNNING runs at once, with one reason; the ones already closed or
// unknown are skipped and counted.
func (s *Server) closeRunsByHand(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	ids, reason, ok := bulkRunIDs(w, r)
	if !ok {
		return
	}
	by, reason := s.uiUser(r), s.redactor().Text(reason)
	closed, skipped := 0, 0
	for _, id := range ids {
		_, err := s.Store.CloseRunByHand(id, by, reason)
		switch {
		case errors.Is(err, db.ErrRunClosed) || errors.Is(err, db.ErrNotFound):
			skipped++
		case err != nil:
			serverError(w, err)
			return
		default:
			closed++
			s.runClosed(id, true)
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"closed": closed, "skipped": skipped})
}

// deleteRuns deletes several closed runs with all their evidence (a RUNNING one is skipped: close
// it first). Like retention, the screenshots and artifacts are deleted and the database compacted,
// so nothing of them is left on disk.
func (s *Server) deleteRuns(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	ids, _, ok := bulkRunIDs(w, r)
	if !ok {
		return
	}
	// si una tanda falla, las anteriores ya se borraron: sus archivos se borran igual y después se informa
	n, files, err := s.Store.DeleteRuns(ids)
	for _, url := range files {
		name := strings.TrimPrefix(url, "/screenshots/")
		if name == url || name != filepath.Base(name) || strings.HasPrefix(name, ".") || s.ScreenshotsDir == "" {
			continue
		}
		os.Remove(filepath.Join(s.ScreenshotsDir, name))
	}
	if n > 0 {
		slog.Info("maintenance: runs deleted by hand", "runs", n, "by", s.uiUser(r))
		s.publish("run", 0, 0, map[string]string{"action": "deleted"}) // las listas abiertas (Recientes) se ponen al día
		s.markCompactPending() // compactar reescribe toda la base: se hace aparte (compact.go)
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n, "skipped": len(ids) - n})
}
