package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/junit"
)

const (
	maxImportBody   = 50 << 20 // 50 MiB entre todos los archivos
	maxImportOutput = 16 << 10 // system-out / system-err por test que se guarda como paso
)

// importJUnit creates a finished run from one or more JUnit XML reports: the body itself, or the
// "file" fields of a multipart form. Run context comes in the query string (name, environment,
// project, branch, commit, framework). The tests get the same treatment as the ones the clients
// send (masking, identity and history, AI diagnosis of failures, run summary and notifications),
// with the times written in the report.
func (s *Server) importJUnit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	suites, err := readJUnit(r)
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the reports are larger than %d MiB", maxImportBody>>20))
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	total := 0
	for _, su := range suites {
		total += len(su.Cases)
	}
	if total == 0 {
		writeError(w, http.StatusBadRequest, "the report has no test cases")
		return
	}

	q := r.URL.Query()
	name := strings.TrimSpace(q.Get("name"))
	if name == "" {
		name = "JUnit"
		if len(suites) == 1 && suites[0].Name != "" {
			name = suites[0].Name
		}
	}
	framework := q.Get("framework")
	if strings.TrimSpace(framework) == "" {
		framework = "junit"
	}
	meta := db.RunMeta{Project: clean(s.label(q.Get("project")), 200), Branch: clean(s.label(q.Get("branch")), 200),
		Commit: clean(q.Get("commit"), 80), Framework: clean(framework, 60)}
	runID, err := s.Store.CreateRunWithMeta(clean(s.label(name), 500), clean(s.label(q.Get("environment")), 200), meta)
	if err != nil {
		serverError(w, err)
		return
	}
	s.publish("run", runID, 0, map[string]string{"action": "created"})

	// Horario: cada suite empieza en su timestamp (o donde terminó la anterior) y sus tests van
	// uno tras otro. Sin ningún timestamp, la ejecución termina ahora.
	// (se suman los tests y no el time de cada suite: los generadores no siempre coinciden)
	var span time.Duration
	for _, su := range suites {
		for _, c := range su.Cases {
			span += c.Duration
		}
	}
	cursor := time.Now().Add(-span)
	runStart, runEnd := time.Time{}, time.Time{}
	counts := map[string]int{}
	for _, su := range suites {
		if !su.Timestamp.IsZero() {
			cursor = su.Timestamp
		}
		for _, c := range su.Cases {
			start, end := cursor, cursor.Add(c.Duration)
			cursor = end
			if runStart.IsZero() || start.Before(runStart) {
				runStart = start
			}
			if end.After(runEnd) {
				runEnd = end
			}
			if err := s.importCase(runID, su, c, start, end); err != nil {
				// la ejecución no queda abierta para siempre: se cierra incompleta
				if _, first, cerr := s.Store.CloseRun(runID, true); cerr == nil {
					s.runClosed(runID, first)
				}
				serverError(w, err)
				return
			}
			counts[c.Status]++
		}
	}

	run, first, err := s.Store.CloseRun(runID, false)
	if err != nil {
		serverError(w, err)
		return
	}
	if err := s.Store.SetRunTimes(runID, runStart.UnixMilli(), runEnd.UnixMilli()); err != nil {
		serverError(w, err)
		return
	}
	s.runClosed(runID, first)
	report := fmt.Sprintf("/#run=%d&view=tests", runID)
	if s.Notify != nil && s.Notify.PublicURL() != "" {
		report = strings.TrimRight(s.Notify.PublicURL(), "/") + report
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run_id": runID, "status": run.Status, "tests": total,
		"passed": counts[junit.Pass], "failed": counts[junit.Fail], "skipped": counts[junit.Skip], "report": report})
}

// importCase stores one test case as a finished test with its output and result as steps.
func (s *Server) importCase(runID int64, su junit.Suite, c junit.Case, start, end time.Time) error {
	key, err := s.testIdentity(clean(c.Key(), 1000), c.Name)
	if err != nil {
		return err
	}
	suite := su.Name
	if suite == "" {
		suite = c.Classname
	}
	meta := db.TestMeta{Key: key, Suite: clean(s.label(suite), 500)}
	id, err := s.Store.CreateTestWithMeta(runID, clean(s.label(c.Name), 1000), "", s.redactor().Text(clean(c.File, 1000)), meta)
	if err != nil {
		return err
	}
	red := s.redactor()
	for _, out := range []struct{ label, text string }{{"stdout", c.SystemOut}, {"stderr", c.SystemErr}} {
		if out.text != "" {
			if _, err := s.Store.AddLog(id, "INFO", red.Text(out.label+":\n"+truncate(out.text, maxImportOutput)), start.UnixMilli(), ""); err != nil {
				return err
			}
		}
	}
	if c.Status != junit.Pass || c.Message != "" {
		msg := c.Message
		if msg == "" {
			msg = map[string]string{junit.Skip: "Skipped", junit.Fail: "Failed"}[c.Status]
		}
		if _, err := s.Store.AddLog(id, c.Status, red.Text(msg), end.UnixMilli(), ""); err != nil {
			return err
		}
	}
	if c.Attempts > 1 {
		if err := s.Store.SetAttempts(id, min(c.Attempts, 100)); err != nil {
			return err
		}
	}
	errMsg := ""
	if c.Status == junit.Fail {
		errMsg = c.Message
	}
	t, _, err := s.Store.FinishTestChange(id, c.Status, red.Text(errMsg), red.Text(c.Trace))
	if err != nil {
		return err
	}
	if err := s.Store.SetTestTimes(id, start.UnixMilli(), end.UnixMilli()); err != nil {
		return err
	}
	if t.Status == "FAIL" {
		s.AI.AnalyzeAsync(id)
	}
	s.publish("test", runID, id, map[string]string{"action": "finished", "status": t.Status})
	return nil
}

// readJUnit parses the request body, or every "file" field when it is a multipart form.
func readJUnit(r *http.Request) ([]junit.Suite, error) {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		return junit.Parse(r.Body)
	}
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		return nil, fmt.Errorf("invalid multipart form: %w", err)
	}
	files := r.MultipartForm.File["file"]
	if len(files) == 0 {
		return nil, errors.New(`multipart field "file" is required (one per report)`)
	}
	var all []junit.Suite
	for _, fh := range files {
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		suites, err := junit.Parse(io.Reader(f))
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", fh.Filename, err)
		}
		all = append(all, suites...)
	}
	return all, nil
}
