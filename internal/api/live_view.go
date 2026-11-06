package api

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/notify"
)

// En vivo: qué terminó, qué corre, qué está lento ("tortuga": más que su p90, o el doble de su p50) y
// qué dejó de enviar ("sin señal"), por ejecución, worker y test. Se calcula con lo que el servidor ya
// recibe de cualquier cliente; execution_id y los tests planificados del cliente lo mejoran.

const (
	maxPlanned     = 100000
	liveWindow     = 2 * time.Hour // ejecuciones terminadas que se siguen mostrando
	statsTTL       = 10 * time.Minute
	statsHistory   = 20 // ejecuciones anteriores para el p50 / p90
	minTurtleHist  = 3  // con menos historial, solo "el doble del p50"
	defaultNoSigMn = 3
)

// liveConfig are the thresholds of Live and its alerts (environment).
type liveConfig struct {
	noSignal     time.Duration // sin señal: sin nada del test hace más que esto
	alerts       bool          // TRACEREPORTS_LIVE_ALERTS=1: avisos a Teams/Slack
	alertTurtle  float64       // avisar una tortuga desde esta cantidad de veces su p90 (0 = no)
	alertFailing int           // avisar cuando una ejecución pasa de K fallos (0 = no)
}

func loadLiveConfig() liveConfig {
	c := liveConfig{noSignal: defaultNoSigMn * time.Minute}
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("TRACEREPORTS_NO_SIGNAL_MIN")), 64); err == nil && v > 0 {
		c.noSignal = time.Duration(v * float64(time.Minute))
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TRACEREPORTS_LIVE_ALERTS"))) {
	case "1", "true", "yes", "on":
		c.alerts = true
	}
	if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv("TRACEREPORTS_ALERT_TURTLE")), 64); err == nil && v > 0 {
		c.alertTurtle = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(os.Getenv("TRACEREPORTS_ALERT_FAILURES"))); err == nil && v > 0 {
		c.alertFailing = v
	}
	return c
}

// liveConf reads the thresholds once (the Server is also built as a literal in tests).
func (s *Server) liveConf() liveConfig {
	s.liveOnce.Do(func() { s.liveCfgV = loadLiveConfig() })
	return s.liveCfgV
}

// statsCache keeps the p50/p90 of each test for a while: Live asks for them on every refresh.
type statsCache struct {
	mu sync.Mutex
	m  map[string]statsEntry
}

type statsEntry struct {
	p50, p90 int64
	n        int
	at       time.Time
}

func (c *statsCache) get(store *db.Store, key, project, env string, run int64) (statsEntry, error) {
	k := key + "\x00" + project + "\x00" + env
	c.mu.Lock()
	e, ok := c.m[k]
	c.mu.Unlock()
	if ok && time.Since(e.at) < statsTTL {
		return e, nil
	}
	p50, p90, n, err := store.DurationStats(key, project, env, run, statsHistory)
	if err != nil {
		return statsEntry{}, err
	}
	e = statsEntry{p50: p50, p90: p90, n: n, at: time.Now()}
	c.mu.Lock()
	if c.m == nil {
		c.m = map[string]statsEntry{}
	}
	c.m[k] = e
	c.mu.Unlock()
	return e, nil
}

// liveTest is a test as Live shows it: its state, how long it has run and how that compares with
// its usual duration.
type liveTest struct {
	db.LiveTest
	State   string  `json:"state"` // running | turtle | no_signal | PASS | FAIL | …
	Elapsed int64   `json:"elapsed"`
	Factor  float64 `json:"factor,omitempty"` // duración / p50
	Idle    int64   `json:"idle,omitempty"`   // ms sin señal (en curso)
}

// testState applies the rules of Live to one test.
func testState(t db.LiveTest, now int64, cfg liveConfig) liveTest {
	out := liveTest{LiveTest: t, State: t.Status}
	end := now
	if t.EndedAt != nil {
		end = *t.EndedAt
	}
	out.Elapsed = max(0, end-t.StartedAt)
	if t.P50 > 0 {
		out.Factor = float64(out.Elapsed) / float64(t.P50)
	}
	if t.Status != "RUNNING" {
		return out
	}
	out.State = "running"
	out.Idle = max(0, now-t.LastSignal)
	switch {
	case out.Idle >= cfg.noSignal.Milliseconds():
		out.State = "no_signal"
	case isTurtle(t, out.Elapsed):
		out.State = "turtle"
	}
	return out
}

// isTurtle: slower than its p90 (with enough history) or than twice its p50.
func isTurtle(t db.LiveTest, elapsed int64) bool {
	if t.P50 <= 0 {
		return false
	}
	if t.Hist >= minTurtleHist && t.P90 > 0 && elapsed > t.P90 && elapsed > t.P50+30_000 {
		return true
	}
	return elapsed > 2*t.P50 && elapsed > t.P50+30_000 // unos segundos de más en un test corto no es tortuga
}

// liveExec is an execution as Live shows it.
type liveExec struct {
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	ExecutionID string         `json:"execution_id,omitempty"`
	Project     string         `json:"project"`
	Environment string         `json:"environment"`
	Branch      string         `json:"branch"`
	Commit      string         `json:"commit"`
	Running     bool           `json:"running"`
	StartedAt   int64          `json:"started_at"`
	EndedAt     int64          `json:"ended_at,omitempty"`
	Planned     int            `json:"planned"`
	Total       int            `json:"total"`
	Done        int            `json:"done"`
	Counts      map[string]int `json:"counts"`
	EtaMs       int64          `json:"eta_ms,omitempty"`
	Workers     int            `json:"workers"`
	Cancel      bool           `json:"cancel_requested,omitempty"`
	Runs        []liveRunView  `json:"runs,omitempty"`
}

type liveRunView struct {
	db.LiveRun
	Tests []liveTest `json:"tests"`
}

// executions builds Live: every execution with a running run or that ended within the window.
func (s *Server) liveExecutions(withTests bool, only string) ([]liveExec, error) {
	now := db.NowMs()
	runs, err := s.Store.LiveCandidates(now - liveWindow.Milliseconds())
	if err != nil {
		return nil, err
	}
	groups := db.GroupExecutions(runs, now)
	var out []liveExec
	for i := len(groups) - 1; i >= 0; i-- { // la más nueva primero
		g := groups[i]
		if only != "" && g.Key != only {
			continue
		}
		if err := s.Store.LiveTests(g.Runs); err != nil {
			return nil, err
		}
		e, err := s.buildExec(g, now, withTests)
		if err != nil {
			return nil, err
		}
		if !e.Running && e.EndedAt < now-liveWindow.Milliseconds() && only == "" {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

func (s *Server) buildExec(g db.Execution, now int64, withTests bool) (liveExec, error) {
	first := g.Runs[0]
	e := liveExec{Key: g.Key, ExecutionID: first.ExecutionID, Project: first.Project, Environment: first.Environment,
		Branch: first.Branch, Commit: first.Commit, StartedAt: first.StartedAt, Counts: map[string]int{}}
	workers := map[string]bool{}
	for _, r := range g.Runs {
		e.Planned += r.Planned
		if r.Status == "RUNNING" {
			e.Running = true
			if r.CancelAt > 0 {
				e.Cancel = true
			}
		}
		if r.EndedAt != nil {
			e.EndedAt = max(e.EndedAt, *r.EndedAt)
		}
		view := liveRunView{LiveRun: r}
		for _, t := range r.Tests {
			if t.Key != "" {
				st, err := s.liveStats.get(s.Store, t.Key, r.Project, r.Environment, r.ID)
				if err != nil {
					return e, err
				}
				t.P50, t.P90, t.Hist = st.p50, st.p90, st.n
			}
			lt := testState(t, now, s.liveConf())
			e.Total++
			e.Counts[lt.State]++
			if t.Status != "RUNNING" {
				e.Done++
			}
			w := t.Worker
			if w == "" {
				w = fmt.Sprintf("run %d", r.ID)
			}
			workers[w] = true
			view.Tests = append(view.Tests, lt)
		}
		view.LiveRun.Tests = nil
		if withTests {
			e.Runs = append(e.Runs, view)
		}
	}
	e.Workers = len(workers)
	if e.Running {
		e.EndedAt = 0
	}
	e.Title = e.ExecutionID
	if e.Title == "" {
		parts := []string{}
		for _, p := range []string{e.Project, e.Branch} {
			if p != "" {
				parts = append(parts, p)
			}
		}
		if e.Commit != "" {
			parts = append(parts, e.Commit[:min(7, len(e.Commit))])
		}
		if len(g.Runs) == 1 || len(parts) == 0 {
			parts = append([]string{first.Name}, parts...)
		}
		e.Title = strings.Join(parts, " · ")
	}
	// ETA: con los tests planificados, al ritmo que lleva la ejecución (ya cuenta el paralelismo)
	if e.Running && e.Planned > e.Done && e.Done > 0 {
		elapsed := now - e.StartedAt
		e.EtaMs = elapsed * int64(e.Planned-e.Done) / int64(e.Done)
	}
	return e, nil
}

// getLive lists the executions of Live (without their tests).
func (s *Server) getLive(w http.ResponseWriter, r *http.Request) {
	list, err := s.liveExecutions(false, "")
	if err != nil {
		serverError(w, err)
		return
	}
	if list == nil {
		list = []liveExec{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": db.NowMs(), "no_signal_ms": s.liveConf().noSignal.Milliseconds(), "executions": list})
}

// getLiveExecution is one execution with its runs and tests (?key=x:… | g:…).
func (s *Server) getLiveExecution(w http.ResponseWriter, r *http.Request) {
	key := r.URL.Query().Get("key")
	list, err := s.liveExecutions(true, key)
	if err != nil {
		serverError(w, err)
		return
	}
	if len(list) == 0 {
		writeError(w, http.StatusNotFound, "execution not found")
		return
	}
	e := list[0]
	// lo que pide acción arriba: sin señal, tortuga, en curso, fallos; dentro, por inicio
	rank := map[string]int{"no_signal": 0, "turtle": 1, "running": 2, "FAIL": 3, "INTERRUPTED": 4, "WARNING": 5}
	for i := range e.Runs {
		sort.SliceStable(e.Runs[i].Tests, func(a, b int) bool {
			ra, ok := rank[e.Runs[i].Tests[a].State]
			if !ok {
				ra = 9
			}
			rb, ok := rank[e.Runs[i].Tests[b].State]
			if !ok {
				rb = 9
			}
			return ra < rb
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": db.NowMs(), "no_signal_ms": s.liveConf().noSignal.Milliseconds(), "execution": e})
}

// setPlanned: the client says how many tests it collected (pytest_collection_finish).
func (s *Server) setPlanned(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	var in struct {
		Planned int `json:"planned"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Planned < 0 || in.Planned > maxPlanned {
		writeError(w, http.StatusBadRequest, "planned out of range")
		return
	}
	if respondErr(w, s.Store.SetPlanned(id, in.Planned), "run") {
		return
	}
	s.publish("run", id, 0, map[string]string{"action": "planned"})
	writeJSON(w, http.StatusOK, map[string]int{"planned": in.Planned})
}

// runControl is what a client with remote control asks between tests (TRACEREPORTS_REMOTE_CONTROL=1).
func (s *Server) runControl(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "run_id")
	if !ok {
		return
	}
	c, err := s.Store.GetRunControl(id)
	if respondErr(w, err, "run") {
		return
	}
	writeJSON(w, http.StatusOK, c)
}

// cancelExecution asks the clients of an execution to stop: each running run is marked, and the
// clients with remote control skip the tests left and close the run. Nothing is killed. Admin only.
func (s *Server) cancelExecution(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		Key    string `json:"key"`
		Reason string `json:"reason"`
	}
	if !decode(w, r, &in) {
		return
	}
	list, err := s.liveExecutions(true, in.Key)
	if err != nil {
		serverError(w, err)
		return
	}
	if in.Key == "" || len(list) == 0 {
		writeError(w, http.StatusNotFound, "execution not found")
		return
	}
	reason := clean(s.redactor().Text(in.Reason), 300)
	marked := 0
	for _, run := range list[0].Runs {
		if run.Status != "RUNNING" {
			continue
		}
		ok, err := s.Store.RequestCancel(run.ID, s.uiUser(r), reason)
		if err != nil {
			serverError(w, err)
			return
		}
		if ok {
			marked++
			s.publish("run", run.ID, 0, map[string]string{"action": "cancel"})
		}
	}
	if marked == 0 {
		writeError(w, http.StatusConflict, "nothing is running in this execution")
		return
	}
	slog.Info("live: cancel requested", "execution", in.Key, "runs", marked, "by", s.uiUser(r))
	writeJSON(w, http.StatusOK, map[string]int{"runs": marked})
}

// testForWrite is the test a client write goes to (/api/v1/tests/{id}/…), 0 if none.
func testForWrite(path string) int64 {
	rest, ok := strings.CutPrefix(path, "/api/v1/tests/")
	if !ok {
		return 0
	}
	id, _, _ := strings.Cut(rest, "/")
	n, _ := strconv.ParseInt(id, 10, 64)
	return n
}

// ---------- alertas ----------

// StartLiveAlerts checks Live every 30 s and alerts (Teams/Slack) a test without signal, a test much
// slower than usual and an execution with too many failures, once each. Off unless
// TRACEREPORTS_LIVE_ALERTS=1 and a webhook is configured.
func (s *Server) StartLiveAlerts(ctx context.Context) {
	if !s.liveConf().alerts || s.Notify == nil || !s.Notify.Enabled() {
		return
	}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.checkLiveAlerts(ctx)
			}
		}
	}()
}

// liveAlert is one alert to send: its key (sent once) and message.
type liveAlert struct {
	key string
	msg notify.Message
}

// liveAlerts are the alerts the executions call for now (pure: tested without a webhook).
func liveAlerts(list []liveExec, cfg liveConfig, url func(runID, testID int64) string) []liveAlert {
	var out []liveAlert
	for _, e := range list {
		if !e.Running {
			continue
		}
		failed := 0
		for _, r := range e.Runs {
			for _, t := range r.Tests {
				if t.Status == "FAIL" {
					failed++
				}
				switch {
				case t.State == "no_signal":
					out = append(out, liveAlert{key: fmt.Sprintf("nosignal:%d", t.ID), msg: notify.Message{
						Title:   fmt.Sprintf("Sin señal: %s", t.Name),
						Stats:   fmt.Sprintf("%s · %s · nada del test hace %s", e.Title, workerOf(t, r), idleText(time.Duration(t.Idle)*time.Millisecond)),
						Summary: lastStepText(t),
						URL:     url(r.ID, t.ID),
					}})
				case cfg.alertTurtle > 0 && t.State == "turtle" && t.P90 > 0 && float64(t.Elapsed) >= cfg.alertTurtle*float64(t.P90):
					out = append(out, liveAlert{key: fmt.Sprintf("turtle:%d", t.ID), msg: notify.Message{
						Warning: true,
						Title:   fmt.Sprintf("Test lento: %s", t.Name),
						Stats:   fmt.Sprintf("%s · %s · lleva %s, %.1f× lo habitual", e.Title, workerOf(t, r), idleText(time.Duration(t.Elapsed)*time.Millisecond), t.Factor),
						Summary: lastStepText(t),
						URL:     url(r.ID, t.ID),
					}})
				}
			}
		}
		if cfg.alertFailing > 0 && failed > cfg.alertFailing {
			first := e.Runs[0].ID
			out = append(out, liveAlert{key: fmt.Sprintf("failures:%s", e.Key), msg: notify.Message{
				Title: fmt.Sprintf("%d fallos en %s", failed, e.Title),
				Stats: fmt.Sprintf("La ejecución sigue en curso y ya pasó de %d fallos.", cfg.alertFailing),
				URL:   url(first, 0),
			}})
		}
	}
	return out
}

func workerOf(t liveTest, r liveRunView) string {
	if t.Worker != "" {
		return t.Worker
	}
	return r.Name
}

func lastStepText(t liveTest) string {
	if t.LastStep == "" {
		return ""
	}
	return "Último paso: " + clipText(t.LastStep, 200)
}

func (s *Server) checkLiveAlerts(ctx context.Context) {
	list, err := s.liveExecutions(true, "")
	if err != nil {
		slog.Warn("live alerts", "err", err)
		return
	}
	base := ""
	if s.Notify != nil {
		base = strings.TrimRight(s.Notify.PublicURL(), "/")
	}
	url := func(runID, testID int64) string {
		if base == "" {
			return ""
		}
		if testID > 0 {
			return fmt.Sprintf("%s/#run=%d&view=tests&test=%d", base, runID, testID)
		}
		return fmt.Sprintf("%s/#view=live", base)
	}
	for _, a := range liveAlerts(list, s.liveConf(), url) {
		m := a.msg
		s.Notify.LiveAlert(ctx, a.key, &m)
	}
}
