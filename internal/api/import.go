package api

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/allure"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/junit"
)

const (
	maxImportBody   = 50 << 20  // 50 MiB entre todos los archivos JUnit
	maxAllureBody   = 200 << 20 // 200 MiB: un allure-results comprimido trae capturas
	maxAllureFiles  = 50_000    // entradas del ZIP (protección contra ZIPs patológicos)
	maxImportOutput = 16 << 10  // system-out / system-err / adjunto de texto que se guarda como paso
)

// Lo que se lee descomprimido de un ZIP de Allure (no lo que el ZIP declara): todo junto, y
// los result.json, que quedan en memoria mientras dura la importación. Variables: las pruebas
// los bajan.
var (
	maxAllureUnzipped int64 = 1 << 30   // 1 GiB
	maxAllureJSON     int64 = 256 << 20 // 256 MiB
	maxAllureParallel       = 2         // importaciones de Allure a la vez (cada una usa disco y CPU)
)

// ---------- partes comunes de las importaciones ----------

// importRun is a run being imported: created with the context of the query string, closed with
// the times written in the report.
type importRun struct {
	s          *Server
	id         int64
	start, end time.Time
	counts     map[string]int
	total      int
}

// newImportRun creates the run. Query: name, environment, project, branch, commit, framework.
func (s *Server) newImportRun(q url.Values, defaultName, defaultFramework string) (*importRun, error) {
	name := strings.TrimSpace(q.Get("name"))
	if name == "" {
		name = defaultName
	}
	framework := strings.TrimSpace(q.Get("framework"))
	if framework == "" {
		framework = defaultFramework
	}
	commit, _ := runCommit(q.Get("commit")) // un commit que no es un id de Git no se guarda
	meta := db.RunMeta{Project: clean(s.label(q.Get("project")), 200), Branch: clean(s.label(q.Get("branch")), 200),
		Commit: commit, Framework: clean(framework, 60)}
	id, err := s.Store.CreateRunWithMeta(clean(s.label(name), 500), clean(s.label(q.Get("environment")), 200), meta)
	if err != nil {
		return nil, err
	}
	s.publish("run", id, 0, map[string]string{"action": "created"})
	return &importRun{s: s, id: id, counts: map[string]int{}}, nil
}

// test creates one finished-to-be test of the run.
func (ir *importRun) test(key, name, category, description, suite, params string) (int64, error) {
	s := ir.s
	identity, err := s.testIdentity(clean(key, 1000), name)
	if err != nil {
		return 0, err
	}
	meta := db.TestMeta{Key: identity, Suite: clean(s.label(suite), 500), Params: clean(s.redactor().Text(params), 500)}
	return s.Store.CreateTestWithMeta(ir.id, clean(s.label(name), 1000), clean(s.label(category), 500), s.redactor().Text(clean(description, 4000)), meta)
}

// finishTest closes a test with its result and its real times, and queues its AI diagnosis.
func (ir *importRun) finishTest(id int64, status, message, trace string, attempts int, start, end time.Time) error {
	s := ir.s
	if attempts > 1 {
		if err := s.Store.SetAttempts(id, min(attempts, 100)); err != nil {
			return err
		}
	}
	red := s.redactor()
	errMsg := ""
	if status == "FAIL" {
		errMsg = message
	}
	t, _, err := s.Store.FinishTestChange(id, status, red.Text(errMsg), red.Text(trace))
	if err != nil {
		return err
	}
	if end.Before(start) {
		end = start
	}
	if err := s.Store.SetTestTimes(id, start.UnixMilli(), end.UnixMilli()); err != nil {
		return err
	}
	if ir.start.IsZero() || start.Before(ir.start) {
		ir.start = start
	}
	if end.After(ir.end) {
		ir.end = end
	}
	ir.counts[t.Status]++
	ir.total++
	if t.Status == "FAIL" {
		s.AI.AnalyzeAsync(id)
	}
	s.publish("test", ir.id, id, map[string]string{"action": "finished", "status": t.Status})
	return nil
}

// abort closes a run whose import failed halfway: it does not stay open forever.
func (ir *importRun) abort() {
	if _, first, err := ir.s.Store.CloseRun(ir.id, true); err == nil {
		ir.s.runClosed(ir.id, first)
	}
}

// finish closes the run with the report's times and answers the import.
func (ir *importRun) finish(w http.ResponseWriter) {
	s := ir.s
	run, first, err := s.Store.CloseRun(ir.id, false)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ir.start.IsZero() {
		if err := s.Store.SetRunTimes(ir.id, ir.start.UnixMilli(), ir.end.UnixMilli()); err != nil {
			serverError(w, err)
			return
		}
	}
	s.runClosed(ir.id, first)
	report := fmt.Sprintf("/#run=%d&view=tests", ir.id)
	if s.Notify != nil && s.Notify.PublicURL() != "" {
		report = strings.TrimRight(s.Notify.PublicURL(), "/") + report
	}
	writeJSON(w, http.StatusCreated, map[string]any{"run_id": ir.id, "status": run.Status, "tests": ir.total,
		"passed": ir.counts["PASS"], "failed": ir.counts["FAIL"], "skipped": ir.counts["SKIP"], "report": report})
}

// importBodyError answers a body that could not be read (too large or invalid).
func importBodyError(w http.ResponseWriter, err error, limit int) {
	var tooBig *http.MaxBytesError
	if errors.As(err, &tooBig) {
		writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("the upload is larger than %d MiB", limit>>20))
		return
	}
	writeError(w, http.StatusBadRequest, err.Error())
}

// ---------- JUnit XML ----------

// importJUnit creates a finished run from one or more JUnit XML reports: the body itself, or the
// "file" fields of a multipart form. Run context comes in the query string (name, environment,
// project, branch, commit, framework). The tests get the same treatment as the ones the clients
// send (masking, identity and history, AI diagnosis of failures, run summary and notifications),
// with the times written in the report.
func (s *Server) importJUnit(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportBody)
	suites, err := readJUnit(r)
	if err != nil {
		importBodyError(w, err, maxImportBody)
		return
	}
	total := 0
	var span time.Duration // se suman los tests y no el time de cada suite: los generadores no siempre coinciden
	for _, su := range suites {
		total += len(su.Cases)
		for _, c := range su.Cases {
			span += c.Duration
		}
	}
	if total == 0 {
		writeError(w, http.StatusBadRequest, "the report has no test cases")
		return
	}
	name := "JUnit"
	if len(suites) == 1 && suites[0].Name != "" {
		name = suites[0].Name
	}
	ir, err := s.newImportRun(r.URL.Query(), name, "junit")
	if err != nil {
		serverError(w, err)
		return
	}
	// Horario: cada suite empieza en su timestamp (o donde terminó la anterior) y sus tests van
	// uno tras otro. Sin ningún timestamp, la ejecución termina ahora.
	cursor := time.Now().Add(-span)
	for _, su := range suites {
		if !su.Timestamp.IsZero() {
			cursor = su.Timestamp
		}
		for _, c := range su.Cases {
			start, end := cursor, cursor.Add(c.Duration)
			cursor = end
			if err := s.importCase(ir, su, c, start, end); err != nil {
				ir.abort()
				serverError(w, err)
				return
			}
		}
	}
	ir.finish(w)
}

// importCase stores one JUnit test case with its output and result as steps.
func (s *Server) importCase(ir *importRun, su junit.Suite, c junit.Case, start, end time.Time) error {
	suite := su.Name
	if suite == "" {
		suite = c.Classname
	}
	id, err := ir.test(c.Key(), c.Name, "", c.File, suite, "")
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
	return ir.finishTest(id, c.Status, c.Message, c.Trace, c.Attempts, start, end)
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

// ---------- Allure ----------

// importAllure creates a finished run from an allure-results folder sent as a ZIP (the body, or
// the "file" field of a multipart form). Same query and treatment as importJUnit, plus the
// steps (nested), screenshots and text attachments of each test.
func (s *Server) importAllure(w http.ResponseWriter, r *http.Request) {
	// pocas a la vez: el resto espera su turno (o se va si el cliente corta)
	select {
	case allureSlots <- struct{}{}:
		defer func() { <-allureSlots }()
	case <-r.Context().Done():
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxAllureBody)
	// el ZIP va a un archivo temporal, no a memoria; se borra al terminar, falle o no
	tmp, size, err := uploadToTemp(r, "tracereports-allure-*.zip")
	if tmp != nil {
		defer func() { tmp.Close(); os.Remove(tmp.Name()) }()
	}
	if err != nil {
		importBodyError(w, err, maxAllureBody)
		return
	}
	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		writeError(w, http.StatusBadRequest, "send the allure-results folder as a ZIP: "+err.Error())
		return
	}
	if len(zr.File) > maxAllureFiles {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("the ZIP has more than %d files", maxAllureFiles))
		return
	}
	budget := &allure.Budget{Total: maxAllureUnzipped, JSON: maxAllureJSON}
	rep, err := allure.ParseBudget(zr, budget)
	if errors.Is(err, allure.ErrBudget) {
		writeError(w, http.StatusRequestEntityTooLarge, allureBudgetMsg)
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(rep.Results) == 0 {
		writeError(w, http.StatusBadRequest, "the allure results have no tests")
		return
	}
	name, framework := rep.Build, "allure"
	if name == "" {
		name = "Allure"
	}
	if f := rep.Results[0].Framework; f != "" {
		framework = f
	}
	ir, err := s.newImportRun(r.URL.Query(), name, framework)
	if err != nil {
		serverError(w, err)
		return
	}
	now := time.Now()
	for _, res := range rep.Results {
		if err := r.Context().Err(); err != nil { // el cliente se fue: no queda una ejecución a medias
			ir.abort()
			return
		}
		if err := s.importResult(ir, res, now); err != nil {
			ir.abort()
			if errors.Is(err, allure.ErrBudget) {
				writeError(w, http.StatusRequestEntityTooLarge, allureBudgetMsg)
				return
			}
			serverError(w, err)
			return
		}
	}
	ir.finish(w)
}

var (
	allureSlots     = make(chan struct{}, maxAllureParallel)
	allureBudgetMsg = fmt.Sprintf("the allure results decompress to more than %d MiB (or their result files to more than %d MiB): import them in parts", maxAllureUnzipped>>20, maxAllureJSON>>20)
)

// uploadToTemp copies the body (or the "file" field of a multipart form, read as a stream) to a
// temporary file. The caller closes and removes it, also when err is not nil.
func uploadToTemp(r *http.Request, pattern string) (*os.File, int64, error) {
	var src io.Reader = r.Body
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		mr, err := r.MultipartReader()
		if err != nil {
			return nil, 0, fmt.Errorf("invalid multipart form: %w", err)
		}
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil, 0, errors.New(`multipart field "file" is required`)
			}
			if err != nil {
				return nil, 0, fmt.Errorf("invalid multipart form: %w", err)
			}
			if part.FormName() == "file" {
				src = part
				break
			}
		}
	}
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, 0, err
	}
	n, err := io.Copy(f, src)
	if err != nil {
		return f, 0, err
	}
	return f, n, nil
}

// importResult stores one Allure result: its steps (indented by depth), attachments and result.
func (s *Server) importResult(ir *importRun, res allure.Result, now time.Time) error {
	start, end := msTime(res.Start, now), msTime(res.Stop, now)
	if res.Start == 0 {
		start = end
	}
	id, err := ir.test(res.Key(), res.Name, strings.Join(res.Tags, ","), res.Description, res.Suite, res.Params)
	if err != nil {
		return err
	}
	if err := s.importSteps(id, res.Steps, 0, start); err != nil {
		return err
	}
	if err := s.importAttachments(id, res.Attachments, "INFO", end); err != nil {
		return err
	}
	if res.Status != allure.Pass && res.Message != "" {
		if _, err := s.Store.AddLog(id, res.Status, s.redactor().Text(res.Message), end.UnixMilli(), ""); err != nil {
			return err
		}
	}
	return ir.finishTest(id, res.Status, res.Message, res.Trace, res.Attempts, start, end)
}

func (s *Server) importSteps(testID int64, steps []allure.Step, depth int, fallback time.Time) error {
	red := s.redactor()
	for _, st := range steps {
		at := msTime(st.Start, fallback)
		msg := strings.Repeat("  ", depth) + st.Name
		if st.Message != "" && st.Status != allure.Pass {
			msg += ": " + st.Message
		}
		if _, err := s.Store.AddLog(testID, st.Status, red.Text(truncate(msg, maxImportOutput)), at.UnixMilli(), ""); err != nil {
			return err
		}
		if err := s.importSteps(testID, st.Steps, depth+1, at); err != nil {
			return err
		}
		status := "INFO"
		if st.Status == allure.Fail {
			status = allure.Fail
		}
		if err := s.importAttachments(testID, st.Attachments, status, msTime(st.Stop, at)); err != nil {
			return err
		}
	}
	return nil
}

// importAttachments stores images as screenshots and short text attachments as steps; anything
// else (videos, traces, HTML) is only mentioned. A missing or unreadable file is skipped.
func (s *Server) importAttachments(testID int64, atts []allure.Attachment, status string, at time.Time) error {
	for _, a := range atts {
		name := a.Name
		if name == "" {
			name = a.Source
		}
		data, err := a.Read()
		switch {
		case errors.Is(err, allure.ErrBudget):
			return err
		case err != nil:
			continue
		case a.IsImage():
			if _, err := s.storeScreenshot(testID, data, status, name, at.UnixMilli()); err != nil && !errors.Is(err, errNotImage) {
				return err
			}
			continue
		}
		msg := "Attachment: " + name
		if a.Type != "" {
			msg += " (" + a.Type + ")"
		}
		if strings.HasPrefix(a.Type, "text/") || a.Type == "application/json" {
			msg = name + ":\n" + truncate(string(data), maxImportOutput)
		}
		if _, err := s.Store.AddLog(testID, "INFO", s.redactor().Text(msg), at.UnixMilli(), ""); err != nil {
			return err
		}
	}
	return nil
}

func msTime(ms int64, fallback time.Time) time.Time {
	if ms <= 0 {
		return fallback
	}
	return time.UnixMilli(ms)
}
