package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports"
	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/offline"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

const reportUsage = `Usage: tracereports report [flags] INPUT...

Builds a static HTML report, without a server: open index.html with a double click, no internet
needed. Each INPUT is one of:

  a recording folder   what a client saved when there was no server (or the token was wrong)
  a JUnit XML file     pytest --junitxml, Maven/Gradle, Playwright, Jest, Cypress, .NET...
  a folder of *.xml    several JUnit XML reports, imported together as one run
  allure-results       an Allure results folder (or a ZIP of it): steps, screenshots, retries

Flags:
`

const pushUsage = `Usage: tracereports push [flags] RECORDING...

Uploads a recording folder to a TraceReports server (for example after fixing the token). Pushing
the same recording again does not duplicate anything.

Flags:
`

// runReport implements "tracereports report".
func runReport(args []string) error {
	fl := flag.NewFlagSet("report", flag.ContinueOnError)
	fl.Usage = func() { fmt.Fprint(fl.Output(), reportUsage); fl.PrintDefaults() }
	out := fl.String("o", "tracereports-report", "output folder")
	zipOut := fl.String("zip", "", "also write the report as a ZIP file (for CI artifacts or email)")
	name := fl.String("name", "", "run name for JUnit XML and Allure inputs (default: the suite or build name)")
	project := fl.String("project", "", "project for JUnit XML and Allure inputs")
	branch := fl.String("branch", "", "branch for JUnit XML and Allure inputs")
	environment := fl.String("environment", "", "environment for JUnit XML and Allure inputs")
	useAI := fl.Bool("ai", false, "diagnose failures with the AI configured in the environment (AI_PROVIDER, AI_API_KEY...)")
	inputs, err := parseInterspersed(fl, args)
	if err != nil {
		return err
	}
	if len(inputs) == 0 {
		fl.Usage()
		return errors.New("no input: pass a recording folder or a JUnit XML file")
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))

	ls, err := newLocalServer(*useAI)
	if err != nil {
		return err
	}
	defer ls.close()
	local, analyzer := ls.target, ls.analyzer

	importQuery := url.Values{}
	for k, v := range map[string]string{"name": *name, "project": *project, "branch": *branch, "environment": *environment} {
		if v != "" {
			importQuery.Set(k, v)
		}
	}
	var runs []int64
	for _, in := range inputs {
		ids, err := loadInput(local, in, importQuery)
		if err != nil {
			return fmt.Errorf("%s: %w", in, err)
		}
		runs = append(runs, ids...)
	}
	if len(runs) == 0 {
		return errors.New("the inputs did not create any run")
	}

	wait := time.Minute
	if *useAI {
		fmt.Fprintln(os.Stderr, "Waiting for the AI diagnosis...")
		wait = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), wait)
	analyzer.Wait(ctx)
	cancel()

	for i, id := range runs {
		data, err := exportZip(local, id)
		if err != nil {
			return err
		}
		dir, zipPath := *out, *zipOut
		if len(runs) > 1 { // una carpeta (y un ZIP) por ejecución
			suffix := fmt.Sprintf("run-%d", i+1)
			dir = filepath.Join(*out, suffix)
			if zipPath != "" {
				zipPath = strings.TrimSuffix(zipPath, ".zip") + "-" + suffix + ".zip"
			}
		}
		if err := unzipTo(data, dir); err != nil {
			return err
		}
		if zipPath != "" {
			if err := os.WriteFile(zipPath, data, 0o644); err != nil {
				return err
			}
		}
		abs, _ := filepath.Abs(filepath.Join(dir, "index.html"))
		fmt.Printf("Report: %s\n", abs)
		if zipPath != "" {
			fmt.Printf("ZIP:    %s\n", zipPath)
		}
	}
	return nil
}

// localServer is a TraceReports server in memory (temporary SQLite, no network): report and
// pr-comment --from load their inputs into it with the same code as the real server.
type localServer struct {
	target   offline.Target
	analyzer *ai.Analyzer
	close    func()
}

func newLocalServer(useAI bool) (*localServer, error) {
	tmp, err := os.MkdirTemp("", "tracereports-report-")
	if err != nil {
		return nil, err
	}
	store, err := db.Open(filepath.Join(tmp, "report.db"))
	if err != nil {
		os.RemoveAll(tmp)
		return nil, err
	}
	cleanup := func() { store.Close(); os.RemoveAll(tmp) }
	shots := filepath.Join(tmp, "screenshots")
	if err := os.MkdirAll(shots, 0o755); err != nil {
		cleanup()
		return nil, err
	}
	webRoot, err := fs.Sub(tracereports.WebFS, "web")
	if err != nil {
		cleanup()
		return nil, err
	}
	analyzer := ai.New(store)
	analyzer.Redact = redact.FromEnv()
	if !useAI {
		_ = analyzer.SetConfig(ai.Config{}) // sin --ai no sale nada a la red
	} else if !analyzer.Enabled() {
		cleanup()
		return nil, errors.New("--ai needs an AI provider in the environment (AI_PROVIDER and AI_API_KEY, or GEMINI_API_KEY...)")
	}
	srv := &api.Server{Store: store, AI: analyzer, ScreenshotsDir: shots, Web: webRoot, Redact: analyzer.Redact}
	return &localServer{target: offline.Target{Doer: offline.Handler{Handler: localOnly(srv.Router())}}, analyzer: analyzer, close: cleanup}, nil
}

// localReads are the calls report and pr-comment make to their in-process server besides the
// evidence of a recording (offline.Ingest): importing JUnit/Allure and reading the result.
var localReads = []struct {
	method string
	path   *regexp.Regexp
}{
	{http.MethodPost, regexp.MustCompile(`^/api/v1/import/(junit|allure)$`)},
	{http.MethodGet, regexp.MustCompile(`^/api/v1/runs/[0-9]{1,19}/(export|compare)$`)},
	{http.MethodGet, regexp.MustCompile(`^/api/v1/runs(/[0-9]{1,19})?$`)},
	{http.MethodGet, regexp.MustCompile(`^/api/v1/config$`)},
}

// localOnly limits the temporary server of report and pr-comment to those calls: Settings, UI
// actions (escalation, tickets, Test connection) and the rest of the API answer 403, whatever a
// recording or an input file asks.
func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ok := r.URL.RawPath == "" && offline.Ingest(r.Method, r.URL.Path)
		for _, c := range localReads {
			ok = ok || (r.URL.RawPath == "" && c.method == r.Method && c.path.MatchString(r.URL.Path))
		}
		if !ok {
			http.Error(w, `{"error":"not available in the report's temporary server"}`, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// loadInput replays a recording or imports JUnit XML into the in-process server.
func loadInput(t offline.Target, in string, importQuery url.Values) ([]int64, error) {
	info, err := os.Stat(in)
	if err != nil {
		return nil, err
	}
	if info.IsDir() && offline.IsRecording(in) {
		rec, err := offline.Open(in)
		if err != nil {
			return nil, err
		}
		res, err := rec.Replay(t)
		if err != nil {
			return nil, err
		}
		for _, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "warning: %s\n", e)
		}
		return res.Runs, nil
	}
	if allureResults, _ := filepath.Glob(filepath.Join(in, "*-result.json")); info.IsDir() && len(allureResults) > 0 {
		data, err := zipFolder(in)
		if err != nil {
			return nil, err
		}
		return importZip(t, "/api/v1/import/allure", data, importQuery)
	}
	if !info.IsDir() && strings.EqualFold(filepath.Ext(in), ".zip") {
		data, err := os.ReadFile(in)
		if err != nil {
			return nil, err
		}
		return importZip(t, "/api/v1/import/allure", data, importQuery)
	}
	var files []string
	if info.IsDir() {
		files, _ = filepath.Glob(filepath.Join(in, "*.xml"))
		if len(files) == 0 {
			return nil, errors.New("not a recording, an allure-results folder or a folder with *.xml files")
		}
	} else {
		files = []string{in}
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		w, _ := mw.CreateFormFile("file", filepath.Base(f))
		w.Write(data)
	}
	mw.Close()
	req, _ := http.NewRequest(http.MethodPost, "/api/v1/import/junit?"+importQuery.Encode(), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := t.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("JUnit import: %s", apiError(raw))
	}
	var res struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return []int64{res.RunID}, nil
}

// importZip posts a ZIP (an allure-results folder) to an import endpoint of the in-process server.
func importZip(t offline.Target, endpoint string, data []byte, q url.Values) ([]int64, error) {
	req, _ := http.NewRequest(http.MethodPost, endpoint+"?"+q.Encode(), bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/zip")
	resp, err := t.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("import: %s", apiError(raw))
	}
	var res struct {
		RunID int64 `json:"run_id"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	return []int64{res.RunID}, nil
}

// zipFolder compresses the files of a folder (not its subfolders) in memory.
func zipFolder(dir string) ([]byte, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, e := range entries {
		if !e.Type().IsRegular() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		w, err := zw.Create(e.Name())
		if err != nil {
			return nil, err
		}
		if _, err := w.Write(data); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func exportZip(t offline.Target, runID int64) ([]byte, error) {
	req, _ := http.NewRequest(http.MethodGet, "/api/v1/runs/"+strconv.FormatInt(runID, 10)+"/export", nil)
	resp, err := t.Doer.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("export run %d: %s", runID, apiError(data))
	}
	return data, nil
}

// unzipTo writes the exported report into dir (created if missing).
func unzipTo(data []byte, dir string) error {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	// el ZIP exportado trae todo bajo una carpeta con el nombre de la ejecución: aquí sobra
	prefix := ""
	if len(zr.File) > 0 {
		if i := strings.IndexByte(zr.File[0].Name, '/'); i > 0 {
			prefix = zr.File[0].Name[:i+1]
			for _, f := range zr.File {
				if !strings.HasPrefix(f.Name, prefix) {
					prefix = ""
					break
				}
			}
		}
	}
	for _, f := range zr.File {
		if strings.TrimPrefix(f.Name, prefix) == "" {
			continue
		}
		name := filepath.Clean(filepath.FromSlash(strings.TrimPrefix(f.Name, prefix)))
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("unexpected path in the report: %s", f.Name)
		}
		dest := filepath.Join(dir, name)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dest, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		if err := os.WriteFile(dest, content, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// runPush implements "tracereports push".
func runPush(args []string) error {
	fl := flag.NewFlagSet("push", flag.ContinueOnError)
	fl.Usage = func() { fmt.Fprint(fl.Output(), pushUsage); fl.PrintDefaults() }
	base := fl.String("url", envOr("TRACEREPORTS_URL", "http://localhost:8080"), "server URL (default $TRACEREPORTS_URL)")
	token := fl.String("token", os.Getenv("TRACEREPORTS_TOKEN"), "write token (default $TRACEREPORTS_TOKEN)")
	force := fl.Bool("force", false, "upload a complete local copy as a separate run")
	dirs, err := parseInterspersed(fl, args)
	if err != nil {
		return err
	}
	if len(dirs) == 0 {
		fl.Usage()
		return errors.New("no recording folder")
	}
	target := offline.Target{Doer: &http.Client{Timeout: 2 * time.Minute}, BaseURL: strings.TrimRight(*base, "/"), Token: *token}
	failed := false
	for _, dir := range dirs {
		rec, err := offline.Open(dir)
		if err != nil {
			return err
		}
		if rec.Mirror != nil {
			var ids []string
			for _, run := range rec.Mirror.Runs {
				ids = append(ids, fmt.Sprintf("#%d", run.Server))
			}
			description := fmt.Sprintf("%s: local copy of run(s) %s at %s", dir, strings.Join(ids, ", "), rec.Mirror.Server)
			if rec.Mirror.Complete && !*force {
				return fmt.Errorf("%s, already delivered completely; use --force to upload as a separate new run", description)
			}
			if !rec.Mirror.Complete {
				fmt.Fprintf(os.Stderr, "warning: %s already has partial evidence; uploading a separate complete run\n", description)
			}
		}
		res, err := rec.Replay(target)
		if err != nil {
			return fmt.Errorf("%s: %w (nothing is duplicated if you push again)", dir, err)
		}
		for _, e := range res.Errors {
			fmt.Fprintf(os.Stderr, "warning: %s\n", e)
		}
		for _, id := range res.Runs {
			fmt.Printf("Report: %s/#run=%d&view=tests\n", target.BaseURL, id)
		}
		fmt.Printf("%s: %d events sent, %d rejected, %d skipped\n", dir, res.Sent, res.Rejected, res.Skipped)
		if res.Rejected > 0 || res.Skipped > 0 || len(res.Runs) == 0 {
			failed = true
		}
	}
	if failed {
		return errors.New("some events were not accepted (see the warnings)")
	}
	return nil
}

// parseInterspersed parses flags placed before or after the positional arguments
// ("report rec -o out" and "report -o out rec"); the flag package alone stops at the first one.
func parseInterspersed(fl *flag.FlagSet, args []string) ([]string, error) {
	var positional []string
	for {
		if err := fl.Parse(args); err != nil {
			return nil, err
		}
		if fl.NArg() == 0 {
			return positional, nil
		}
		positional = append(positional, fl.Arg(0))
		args = fl.Args()[1:]
	}
}

var apiErrMsg = regexp.MustCompile(`"error"\s*:\s*"([^"]*)"`)

// apiError extracts the message of an API error body.
func apiError(body []byte) string {
	if m := apiErrMsg.FindSubmatch(body); m != nil {
		return string(m[1])
	}
	return strings.TrimSpace(string(body))
}
