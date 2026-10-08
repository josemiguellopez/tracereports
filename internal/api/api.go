// Package api implements the TraceReports REST API and static file serving.
package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/live"
	"github.com/josemiguellopez/tracereports/internal/notify"
	"github.com/josemiguellopez/tracereports/internal/owners"
	"github.com/josemiguellopez/tracereports/internal/redact"
	"github.com/josemiguellopez/tracereports/internal/release"
	"github.com/josemiguellopez/tracereports/internal/repro"
	"github.com/josemiguellopez/tracereports/internal/secret"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

const (
	maxJSONBody       = 1 << 20  // 1 MiB
	maxScreenshotBody = 15 << 20 // 15 MiB
)

var (
	logStatuses  = set("INFO", "PASS", "FAIL", "WARNING", "SKIP")
	testStatuses = set("PASS", "FAIL", "WARNING", "SKIP")
	imageExt     = map[string]string{"image/png": ".png", "image/jpeg": ".jpg", "image/gif": ".gif", "image/webp": ".webp"}
)

// Server bundles the dependencies of the HTTP handlers.
type Server struct {
	Store          *db.Store
	AI             *ai.Analyzer
	Notify         *notify.Notifier
	Live           *live.Hub // eventos en vivo (SSE); nil = desactivado
	Auth           Auth
	ScreenshotsDir string
	Web            fs.FS // root containing index.html, style.css, app.js
	// SettingsLocked (TRACEREPORTS_SETTINGS_LOCKED) makes the Settings screen read-only: the
	// configuration comes only from the environment.
	SettingsLocked bool
	// Redact masks secrets before anything is stored (nil = redact.Default()).
	Redact *redact.Policy
	// Secrets encrypts the credentials saved from Settings (TRACEREPORTS_SECRET_KEY); nil = none
	// configured: credentials cannot be saved from the UI, only read from the environment.
	Secrets *secret.Box
	// Hosts accepted on requests without credentials (DNS rebinding); nil checks nothing.
	Hosts *HostPolicy
	// Trackers where a failure can be turned into a ticket (GitHub, Jira, Azure DevOps).
	Trackers []tracker.Provider
	// LogsURL and TraceURL are link templates that open the backend logs or the trace of a call
	// (TRACEREPORTS_LOGS_URL, TRACEREPORTS_TRACE_URL; see correlate.Link).
	LogsURL, TraceURL string
	// Owners assigns an owner to each test (TRACEREPORTS_OWNERS / _FILE); nil = none.
	Owners *owners.Rules
	// ReleaseGate are the "can we ship?" criteria (TRACEREPORTS_RELEASE_GATE); nil = defaults.
	ReleaseGate *release.Gate
}

// redactor returns the masking policy applied to every incoming text.
func (s *Server) redactor() *redact.Policy {
	if s.Redact == nil {
		return redact.Default()
	}
	return s.Redact
}

// Router builds the chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(peerAddr, middleware.RealIP, middleware.Recoverer, middleware.Compress(5, "application/json", "text/html", "text/css", "application/javascript"))
	r.Use(s.Auth.middleware, s.hostGuard)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(s.trackActivity, s.idempotent)
		r.Get("/config", s.getConfig)
		r.Get("/settings", s.getSettings)
		r.Get("/auth/check", s.checkToken)
		r.Put("/settings", s.putSettings)
		r.Post("/settings/ai/test", s.testAISettings)
		r.Delete("/settings/ai", s.resetAISettings)
		r.Get("/settings/ai/usage", s.aiUsage)
		r.Get("/settings/ai/status", s.aiStatus)
		r.Get("/metrics", s.getMetrics)
		r.Get("/runs/{run_id}/recurrence", s.runRecurrence)
		r.Get("/runs/{run_id}/escalation", s.getEscalation)
		r.Post("/ui/tests/{test_id}/analyze", s.reanalyzeTest)
		r.Post("/ui/runs/{run_id}/analyze", s.reanalyzeRun)
		r.Post("/ui/escalate", s.escalate)
		r.Post("/ui/escalate/send", s.sendEscalation)
		r.Post("/ui/tickets", s.createTicket)
		r.Post("/ui/summary/weekly", s.weeklySummary)
		r.Post("/ui/quarantine", s.setQuarantine)
		r.Delete("/ui/quarantine/{test_id}", s.removeQuarantine)
		r.Get("/quarantine", s.listQuarantine)
		r.Post("/ui/tests/{test_id}/verdict", s.setVerdict)
		r.Get("/stream", s.stream)

		r.Get("/runs", s.listRuns)
		r.Post("/runs", s.createRun)
		r.Get("/runs/{run_id}", s.getRun)
		r.Get("/runs/{run_id}/export", s.exportRun)
		r.Get("/runs/{run_id}/tickets", s.listTickets)
		r.Get("/runs/{run_id}/release", s.runRelease)
		r.Get("/runs/{run_id}/compare", s.compareRun)
		r.Get("/runs/{run_id}/baselines", s.runBaselines)
		r.Get("/runs/{run_id}/endpoints", s.runEndpoints)
		r.Patch("/runs/{run_id}/finish", s.finishRun)
		r.Post("/runs/{run_id}/tests", s.createTest)
		r.Post("/import/junit", s.importJUnit)
		r.Post("/import/allure", s.importAllure)

		r.Get("/tests/{test_id}", s.getTest)
		r.Post("/tests/{test_id}/logs", s.addLog)
		r.Post("/tests/{test_id}/screenshot", s.uploadScreenshot)
		r.Post("/tests/{test_id}/artifact", s.uploadArtifact)
		r.Post("/tests/{test_id}/console", s.addConsole)
		r.Patch("/tests/{test_id}/finish", s.finishTest)
		r.Get("/tests/{test_id}/history", s.testHistory)
		r.Get("/tests/{test_id}/locator", s.testLocator)
		r.Post("/tests/{test_id}/dom", s.addDOM)
		r.Get("/tests/{test_id}/drift", s.testDrift)
		r.Get("/tests/{test_id}/network", s.listNetwork)
		r.Post("/tests/{test_id}/network", s.addNetwork)
		r.Get("/network/{conn_id}/body", s.networkBody)
		r.Get("/network/{conn_id}/baseline", s.networkBaseline)
	})

	r.Handle("/screenshots/*", withTraceViewerCORS(http.StripPrefix("/screenshots/", noDirListing(http.FileServer(http.Dir(s.ScreenshotsDir))))))
	r.Handle("/*", http.FileServer(http.FS(s.Web)))
	return r
}

// ---------- Handlers: runs ----------

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	saved, err := s.Store.Settings()
	if err != nil {
		serverError(w, err)
		return
	}
	canAct, why := s.uiAccess(r)
	teams, slack := false, false
	publicURL := false
	if s.Notify != nil {
		teams, slack = s.Notify.Channels()
		publicURL = s.Notify.PublicURL() != ""
	}
	trackers := []map[string]string{} // solo cuáles hay: los tokens nunca salen del servidor
	for _, p := range s.Trackers {
		trackers = append(trackers, map[string]string{"id": p.ID(), "name": p.Name()})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ai_enabled": s.AI.Enabled(), "ai_model": s.AI.Model(), "trackers": trackers,
		"ai_provider": s.AI.Provider(), "language": saved[setLanguage],
		// acciones desde la UI (re-analizar, escalar, enviar) y canales disponibles
		"ui_actions": canAct, "ui_actions_reason": why, "teams": teams, "slack": slack, "public_url": publicURL})
}

func (s *Server) listRuns(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	runs, err := s.Store.ListRuns(limit)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

func (s *Server) createRun(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name        string `json:"name"`
		Environment string `json:"environment"`
		db.RunMeta
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	commit, warnings := runCommit(in.Commit)
	meta := db.RunMeta{Project: clean(s.label(in.Project), 200), Branch: clean(s.label(in.Branch), 200), Commit: commit, Framework: clean(in.Framework, 60)}
	name, environment := strings.TrimSpace(s.label(in.Name)), strings.TrimSpace(s.label(in.Environment))
	var id int64
	err := s.commit(w, r, func(tx *db.Store) (int, any, error) {
		var err error
		if id, err = tx.CreateRunWithMeta(name, environment, meta); err != nil {
			return 0, nil, err
		}
		if ts := eventTime(r); ts > 0 {
			if err := tx.SetRunStarted(id, ts); err != nil {
				return 0, nil, err
			}
		}
		res := map[string]any{"run_id": id}
		if len(warnings) > 0 {
			res["warnings"] = warnings
		}
		return http.StatusCreated, res, nil
	})
	if err != nil {
		serverError(w, err)
		return
	}
	s.publish("run", id, 0, map[string]string{"action": "created"})
}

// runCommit validates the commit of a run: it must be a Git commit id (4 to 64 hex characters,
// what the clients send from the CI variables or git rev-parse). Anything else is not stored,
// since it ends up in the "run it locally" commands, and the run is still created: the warning
// tells the client.
func runCommit(raw string) (string, []string) {
	raw = strings.TrimSpace(raw)
	c := repro.NormalizeCommit(raw)
	if raw != "" && c == "" {
		return "", []string{"commit ignored: it must be a Git commit id (4 to 64 hexadecimal characters)"}
	}
	return c, nil
}

func (s *Server) getRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	d, err := s.Store.GetRunDetail(id)
	if respondErr(w, err, "run") {
		return
	}
	if err := s.decorate(id, d.Tests); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) finishRun(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	var in struct {
		Interrupted bool `json:"interrupted"` // el cliente no llegó a terminar todos sus tests
	}
	if !decode(w, r, &in) {
		return
	}
	run, first, err := s.Store.CloseRun(id, in.Interrupted)
	if respondErr(w, err, "run") {
		return
	}
	if ts := eventTime(r); ts > 0 && first { // un cierre repetido conserva la hora del primero
		if err := s.Store.SetRunEnded(id, ts); err != nil {
			serverError(w, err)
			return
		}
		run.EndedAt = &ts
	}
	s.runClosed(id, first)
	writeJSON(w, http.StatusOK, run)
}

// runClosed starts what follows closing a run: the diagnosis of the whole run and then the
// Teams/Slack notice. Only on the first close: a repeated close (retry, spool) does not spend AI
// or notify again.
func (s *Server) runClosed(id int64, first bool) {
	if first {
		s.AI.AnalyzeRunAsync(id, func() {
			if s.Notify != nil {
				s.Notify.RunFinished(id)
			}
		})
	}
	s.publish("run", id, 0, map[string]string{"action": "finished"})
}

// ---------- Handlers: tests ----------

func (s *Server) createTest(w http.ResponseWriter, r *http.Request) {
	runID, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	var in struct {
		Name        string `json:"name"`
		Category    string `json:"category"`
		Description string `json:"description"`
		db.TestMeta
	}
	if !decode(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Name) == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	key, err := s.testIdentity(clean(in.Key, 1000), strings.TrimSpace(in.Name))
	if err != nil {
		serverError(w, err)
		return
	}
	meta := db.TestMeta{Key: key, Suite: clean(s.label(in.Suite), 500), Params: clean(s.redactor().Text(in.Params), 500), Worker: clean(s.label(in.Worker), 60)}
	name, category, description := strings.TrimSpace(s.label(in.Name)), s.label(in.Category), s.redactor().Text(in.Description)
	var id int64
	err = s.commit(w, r, func(tx *db.Store) (int, any, error) {
		var err error
		if id, err = tx.CreateTestWithMeta(runID, name, category, description, meta); err != nil {
			return 0, nil, err
		}
		if ts := eventTime(r); ts > 0 {
			if err := tx.SetTestStarted(id, ts); err != nil {
				return 0, nil, err
			}
		}
		return http.StatusCreated, map[string]any{"test_id": id}, nil
	})
	if respondErr(w, err, "run") {
		return
	}
	s.publish("test", runID, id, map[string]string{"action": "created"})
}

func (s *Server) getTest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	t, err := s.Store.GetTest(id)
	if respondErr(w, err, "test") {
		return
	}
	if err := s.decorateOne(t); err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) addLog(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	var in struct {
		Status    string          `json:"status"`
		Message   string          `json:"message"`
		Timestamp json.RawMessage `json:"timestamp"`
	}
	if !decode(w, r, &in) {
		return
	}
	status := strings.ToUpper(strings.TrimSpace(in.Status))
	if status == "" {
		status = "INFO"
	}
	if !logStatuses[status] {
		writeError(w, http.StatusBadRequest, "status must be one of INFO, PASS, FAIL, WARNING, SKIP")
		return
	}
	ts, err := parseTimestamp(in.Timestamp)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if ts == 0 {
		ts = eventTime(r)
	}
	msg := s.redactor().Text(in.Message)
	var l *db.Log
	err = s.commit(w, r, func(tx *db.Store) (int, any, error) {
		var err error
		l, err = tx.AddLog(id, status, msg, ts, "")
		return http.StatusCreated, l, err
	})
	if respondErr(w, err, "test") {
		return
	}
	s.publishTest("log", id, l)
}

// uploadScreenshot stores the "file" multipart field and records it as a step.
// Optional form fields: "message", "status" (default INFO).
func (s *Server) uploadScreenshot(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxScreenshotBody)
	if err := r.ParseMultipartForm(4 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form: "+err.Error())
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, `multipart field "file" is required`)
		return
	}
	defer file.Close()

	data, err := io.ReadAll(file)
	if err != nil {
		writeError(w, http.StatusBadRequest, "could not read file")
		return
	}
	if _, ok := imageExt[http.DetectContentType(data)]; !ok {
		writeError(w, http.StatusUnsupportedMediaType, "only PNG, JPEG, GIF or WEBP images are accepted")
		return
	}
	status := strings.ToUpper(strings.TrimSpace(r.FormValue("status")))
	if status == "" {
		status = "INFO"
	}
	if !logStatuses[status] {
		writeError(w, http.StatusBadRequest, "status must be one of INFO, PASS, FAIL, WARNING, SKIP")
		return
	}
	if _, err := s.Store.GetTest(id); respondErr(w, err, "test") {
		return
	}

	// el archivo primero; su paso y la respuesta idempotente, en una transacción
	url, err := s.saveScreenshotFile(id, data)
	if err != nil {
		serverError(w, err)
		return
	}
	msg := s.redactor().Text(r.FormValue("message"))
	var l *db.Log
	err = s.commit(w, r, func(tx *db.Store) (int, any, error) {
		var err error
		if l, err = tx.AddLog(id, status, msg, eventTime(r), url); err != nil {
			return 0, nil, err
		}
		return http.StatusCreated, map[string]any{"url": l.Screenshot, "log": l}, nil
	})
	if err != nil {
		s.removeScreenshotFile(url) // no quedó referenciada
	}
	if respondErr(w, err, "test") {
		return
	}
	s.publishTest("log", id, l)
}

// saveScreenshotFile writes an image to the screenshots folder and returns its URL.
func (s *Server) saveScreenshotFile(testID int64, data []byte) (string, error) {
	ext, ok := imageExt[http.DetectContentType(data)]
	if !ok {
		return "", errNotImage
	}
	name := fmt.Sprintf("t%d_%d%s", testID, time.Now().UnixNano(), ext)
	if err := os.WriteFile(filepath.Join(s.ScreenshotsDir, name), data, 0o644); err != nil {
		return "", err
	}
	return "/screenshots/" + name, nil
}

func (s *Server) removeScreenshotFile(url string) {
	if name := strings.TrimPrefix(url, "/screenshots/"); name != url && name == filepath.Base(name) {
		os.Remove(filepath.Join(s.ScreenshotsDir, name))
	}
}

// errNotImage: the bytes are not a PNG, JPEG, GIF or WEBP image.
var errNotImage = errors.New("only PNG, JPEG, GIF or WEBP images are accepted")

// storeScreenshot saves an image of a test and records it as a step (ts 0 = now).
func (s *Server) storeScreenshot(testID int64, data []byte, status, message string, ts int64) (*db.Log, error) {
	url, err := s.saveScreenshotFile(testID, data)
	if err != nil {
		return nil, err
	}
	l, err := s.Store.AddLog(testID, status, s.redactor().Text(message), ts, url)
	if err != nil {
		s.removeScreenshotFile(url)
	}
	return l, err
}

func (s *Server) finishTest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	var in struct {
		Status       string `json:"status"`
		ErrorMessage string `json:"error_message"`
		ErrorTrace   string `json:"error_trace"`
		Attempts     int    `json:"attempts"` // veces que el runner lo ejecutó (reintentos)
	}
	if !decode(w, r, &in) {
		return
	}
	status := strings.ToUpper(strings.TrimSpace(in.Status))
	if status != "" && !testStatuses[status] {
		writeError(w, http.StatusBadRequest, "status must be one of PASS, FAIL, WARNING, SKIP (or empty to derive from steps)")
		return
	}
	if in.Attempts > 1 {
		if err := s.Store.SetAttempts(id, min(in.Attempts, 100)); err != nil {
			serverError(w, err)
			return
		}
	}
	t, change, err := s.Store.FinishTestChange(id, status, s.redactor().Text(in.ErrorMessage), s.redactor().Text(in.ErrorTrace))
	if respondErr(w, err, "test") {
		return
	}
	if ts := eventTime(r); ts > 0 {
		if err := s.Store.SetTestEnded(id, ts); err != nil {
			serverError(w, err)
			return
		}
		t.EndedAt = &ts
	}
	if change.Changed {
		s.AI.Supersede(t.ID) // el análisis del resultado anterior ya no sirve
	}
	if t.Status == "FAIL" {
		s.AI.AnalyzeAsync(t.ID)
	}
	if change.Late {
		// resultado tardío que cambia un run ya cerrado (spool): su resumen describe la evidencia
		// anterior. Queda pendiente y se reconstruye (con o sin IA) tras el análisis del test; sin
		// volver a notificar. Un replay idéntico no cambia nada y no gasta IA.
		s.AI.AnalyzeRunAsync(t.RunID, nil)
	}
	s.publish("test", t.RunID, t.ID, map[string]string{"action": "finished", "status": t.Status})
	writeJSON(w, http.StatusOK, t)
}

// ---------- Helpers ----------

func set(vals ...string) map[string]bool {
	m := make(map[string]bool, len(vals))
	for _, v := range vals {
		m[v] = true
	}
	return m
}

// clean trims s and cuts it to n bytes (identity and context fields).
func clean(s string, n int) string { return truncate(strings.TrimSpace(s), n) }

// label masks secrets in a visible name (run, test, suite, category, environment...). Masking
// can swallow the bracket that closes a pytest parameter ("t[token=x]" -> "t[token=<masked>"):
// it is put back so the label still reads well.
func (s *Server) label(v string) string {
	out := s.redactor().Text(v)
	if out == v {
		return v
	}
	for _, c := range []string{")", "]"} {
		if strings.HasSuffix(v, c) && !strings.HasSuffix(out, c) {
			out += c
		}
	}
	return out
}

// testIdentity returns the stored identity of a test: its key (or db.NameKey of its name). When
// that carries a secret it is masked like the label, plus a keyed hash of the original value:
// the secret never reaches the database, and two tests that differ only in the secret keep
// separate histories (and the same test keeps its history run after run).
func (s *Server) testIdentity(key, name string) (string, error) {
	if key == "" {
		key = db.NameKey(name)
	}
	visible := s.label(key)
	if visible == key {
		return key, nil
	}
	salt, err := s.Store.IdentitySalt()
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, salt)
	mac.Write([]byte(key))
	return truncate(visible, 980) + " #" + hex.EncodeToString(mac.Sum(nil))[:16], nil
}

// eventTime is the X-TraceReports-Timestamp header (Unix ms): when the event really happened. A
// recording made without a server (tracereports report / push) is replayed later, and this keeps
// its times instead of the replay's. Absent or invalid → 0 (now).
func eventTime(r *http.Request) int64 {
	ms, err := strconv.ParseInt(strings.TrimSpace(r.Header.Get("X-TraceReports-Timestamp")), 10, 64)
	if err != nil || ms <= 0 {
		return 0
	}
	return ms
}

// parseTimestamp accepts Unix ms / seconds (number) or an RFC 3339 string. Empty → 0 (now).
func parseTimestamp(raw json.RawMessage) (int64, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, nil
	}
	var num float64
	if err := json.Unmarshal(raw, &num); err == nil {
		if num < 1e11 { // looks like seconds
			return int64(num * 1000), nil
		}
		return int64(num), nil
	}
	var str string
	if err := json.Unmarshal(raw, &str); err != nil {
		return 0, errors.New("timestamp must be a number (unix ms) or RFC 3339 string")
	}
	if str == "" {
		return 0, nil
	}
	t, err := time.Parse(time.RFC3339Nano, str)
	if err != nil {
		return 0, errors.New("timestamp must be RFC 3339, e.g. 2026-01-02T15:04:05Z")
	}
	return t.UnixMilli(), nil
}

func pathID(w http.ResponseWriter, r *http.Request, key string) (int64, bool) {
	id, err := strconv.ParseInt(chi.URLParam(r, key), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid "+key)
		return 0, false
	}
	return id, true
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONBody)
	return decodeLimited(w, r, v)
}

// decodeLimited decodes a JSON body whose size limit was already set by the caller.
func decodeLimited(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// respondErr writes an error response if err != nil and reports whether it did.
func respondErr(w http.ResponseWriter, err error, what string) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, db.ErrNotFound) {
		writeError(w, http.StatusNotFound, what+" not found")
	} else {
		serverError(w, err)
	}
	return true
}

func serverError(w http.ResponseWriter, err error) {
	slog.Error("request failed", "err", err)
	writeError(w, http.StatusInternalServerError, "internal error")
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func noDirListing(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "" || strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
		h.ServeHTTP(w, r)
	})
}
