package db

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

const insightsSchema = `
CREATE INDEX IF NOT EXISTS idx_tests_name ON tests(name, run_id);

CREATE TABLE IF NOT EXISTS run_triage (
	run_id     INTEGER PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE,
	state      TEXT    NOT NULL,            -- PENDING | DONE | ERROR
	headline   TEXT    NOT NULL DEFAULT '',
	summary    TEXT    NOT NULL DEFAULT '',
	incidents  TEXT    NOT NULL DEFAULT '[]',
	ai         INTEGER NOT NULL DEFAULT 0,
	error      TEXT    NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL
);
`

// historyWindow is how many past executions of a test are considered for flakiness.
const historyWindow = 10

// sameContext restricts the runs alias r to the context (project, environment and branch) of
// the run given as its three arguments: staging never mixes with production, nor a feature
// branch with main.
const sameContext = `r.project = (SELECT project FROM runs WHERE id = ?)
	AND r.environment = (SELECT environment FROM runs WHERE id = ?)
	AND r.branch = (SELECT branch FROM runs WHERE id = ?)`

func ctxArgs(runID int64) []any { return []any{runID, runID, runID} }

// ---------- History & flakiness ----------

// HistoryEntry is one past execution of a test: same identity (test_key) and same context.
type HistoryEntry struct {
	TestID     int64  `json:"test_id"`
	RunID      int64  `json:"run_id"`
	RunName    string `json:"run_name"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"started_at"`
	DurationMs *int64 `json:"duration_ms"`
	Attempts   int    `json:"attempts"`
	Error      string `json:"error,omitempty"`    // primera línea del error, si falló
	Category   string `json:"category,omitempty"` // causa según la IA, si falló y se diagnosticó
}

// TestHistory returns the last `limit` finished executions of the test with identity `key` in
// the context of run `uptoRunID`, up to (and including) that run, newest first. When a run has
// the same key more than once, only its last execution counts.
func (s *Store) TestHistory(key string, uptoRunID int64, limit int) ([]HistoryEntry, error) {
	args := append([]any{key, uptoRunID}, ctxArgs(uptoRunID)...)
	rows, err := s.db.Query(`
		SELECT t.id, t.run_id, r.name, t.status, t.started_at, t.ended_at, t.error_message, t.attempts,
			COALESCE((SELECT a.category FROM ai_triage a WHERE a.test_id = t.id AND a.state = 'DONE'), '')
		FROM tests t JOIN runs r ON r.id = t.run_id
		WHERE t.test_key = ? AND t.run_id <= ? AND t.status <> 'RUNNING' AND `+sameContext+`
		ORDER BY t.run_id DESC, t.id DESC LIMIT ?`, append(args, limit*2)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HistoryEntry{}
	lastRun := int64(0)
	for rows.Next() {
		var h HistoryEntry
		var ended sql.NullInt64
		var errMsg string
		if err := rows.Scan(&h.TestID, &h.RunID, &h.RunName, &h.Status, &h.StartedAt, &ended, &errMsg, &h.Attempts, &h.Category); err != nil {
			return nil, err
		}
		if h.RunID == lastRun || len(out) == limit {
			continue
		}
		lastRun = h.RunID
		if h.Status == "FAIL" {
			h.Error = firstLine(errMsg)
		}
		if ended.Valid {
			d := ended.Int64 - h.StartedAt
			h.DurationMs = &d
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// Stability kinds of a test history.
const (
	// StabilityFlaky: alternates between passing and failing, or passed only after a retry.
	StabilityFlaky = "flaky"
	// StabilityPersistent: fails in its latest executions in a row; a regression, not flakiness.
	StabilityPersistent = "persistent"
)

const (
	minFlakySample   = 5  // con menos ejecuciones la conclusión es débil (low_data)
	persistentStreak = 3  // fallos seguidos más recientes que indican una falla persistente
	flakyScoreWindow = 20 // ejecuciones usadas para la tasa de fallo
)

// Classify reads a history (newest first) and tells whether it looks flaky, persistently failing
// or neither (""). WARNING counts as not failed and SKIP is ignored. retried is how many of those
// executions passed only after a retry: that is direct evidence of flakiness.
func Classify(statuses []string, retried int) (kind string, flips, streak int) {
	var seq []bool // true = failed
	for _, st := range statuses {
		switch st {
		case "FAIL":
			seq = append(seq, true)
		case "PASS", "WARNING":
			seq = append(seq, false)
		}
	}
	for _, failed := range seq {
		if !failed {
			break
		}
		streak++
	}
	if len(seq) > historyWindow {
		seq = seq[:historyWindow]
	}
	for i := 1; i < len(seq); i++ {
		if seq[i] != seq[i-1] {
			flips++
		}
	}
	switch {
	case streak >= persistentStreak:
		return StabilityPersistent, flips, streak
	case retried > 0 || flips >= 2:
		return StabilityFlaky, flips, streak
	}
	return "", flips, streak
}

// IsFlaky reports whether a history (newest first) alternates between passing and failing:
// at least two flips PASS<->FAIL within the window, and not a persistent failure.
func IsFlaky(statuses []string) bool {
	kind, _, _ := Classify(statuses, 0)
	return kind == StabilityFlaky
}

// FlakyInfo describes the stability of a test over its last executions in the same context.
type FlakyInfo struct {
	Kind     string   `json:"kind"`      // flaky | persistent
	Fails    int      `json:"fails"`     // ejecuciones que fallaron en la ventana
	Runs     int      `json:"runs"`      // ejecuciones consideradas (ventana)
	FailRate int      `json:"fail_rate"` // % de ejecuciones que fallaron: NO es un "% de inestabilidad"
	Flips    int      `json:"flips"`     // cambios pasa<->falla en las últimas historyWindow
	Retried  int      `json:"retried"`   // ejecuciones que pasaron solo tras reintentar
	Streak   int      `json:"streak"`    // fallos seguidos más recientes
	LowData  bool     `json:"low_data"`  // menos de minFlakySample ejecuciones: conclusión débil
	Recent   []string `json:"recent"`    // últimos 10 estados, del más viejo al más nuevo (sparkline)
}

// flakyByKey computes the stability of every test identity present in run `runID`, over its
// executions in the same context (one per run). Tests that are neither flaky nor persistently
// failing are left out.
func (s *Store) flakyByKey(runID int64) (map[string]*FlakyInfo, error) {
	args := append([]any{runID}, ctxArgs(runID)...)
	rows, err := s.db.Query(`
		SELECT t.test_key, t.run_id, t.status, t.attempts FROM tests t JOIN runs r ON r.id = t.run_id
		WHERE t.run_id <= ? AND t.status IN ('PASS','FAIL','WARNING') AND `+sameContext+`
		  AND t.test_key IN (SELECT test_key FROM tests WHERE run_id = ?)
		ORDER BY t.test_key, t.run_id DESC, t.id DESC`, append(args, runID)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type exec struct {
		status  string
		retried bool
	}
	byKey := map[string][]exec{}
	lastRun := map[string]int64{}
	for rows.Next() {
		var key, st string
		var run int64
		var attempts int
		if err := rows.Scan(&key, &run, &st, &attempts); err != nil {
			return nil, err
		}
		if lastRun[key] == run || len(byKey[key]) >= flakyScoreWindow {
			continue
		}
		lastRun[key] = run
		byKey[key] = append(byKey[key], exec{st, st != "FAIL" && attempts > 1})
	}
	out := make(map[string]*FlakyInfo, len(byKey))
	for key, execs := range byKey {
		var sts []string
		retried := 0
		for i, e := range execs {
			sts = append(sts, e.status)
			if e.retried && i < historyWindow {
				retried++
			}
		}
		recent := sts
		if len(recent) > historyWindow {
			recent = recent[:historyWindow]
		}
		kind, flips, streak := Classify(sts, retried)
		if kind == "" {
			continue
		}
		info := &FlakyInfo{Kind: kind, Runs: len(sts), Flips: flips, Retried: retried, Streak: streak,
			LowData: len(sts) < minFlakySample && retried == 0}
		for _, st := range sts {
			if st == "FAIL" {
				info.Fails++
			}
		}
		info.FailRate = info.Fails * 100 / info.Runs
		for i := len(recent) - 1; i >= 0; i-- {
			info.Recent = append(info.Recent, recent[i])
		}
		out[key] = info
	}
	return out, rows.Err()
}

// ---------- Run comparison ----------

// CompareItem is a test whose outcome changed between two runs.
type CompareItem struct {
	Name           string `json:"name"`
	Key            string `json:"key"`
	TestID         int64  `json:"test_id"`
	Status         string `json:"status"`
	BaseStatus     string `json:"base_status,omitempty"`
	DurationMs     int64  `json:"duration_ms"`
	BaseDurationMs int64  `json:"base_duration_ms,omitempty"`
}

// Base run choice, reported so the UI can say what it compares against.
const (
	BaseSameContext = "same_context" // la anterior del mismo proyecto, ambiente y rama
	BaseOtherBranch = "other_branch" // no hay de la misma rama: la anterior del mismo proyecto y ambiente
	BaseChosen      = "chosen"       // elegida por la persona
)

// Comparison lists what changed in a run with respect to a base run.
type Comparison struct {
	BaseRun      *Run          `json:"base_run"`
	BaseReason   string        `json:"base_reason,omitempty"`
	NewFailures  []CompareItem `json:"new_failures"`
	Fixed        []CompareItem `json:"fixed"`
	StillFailing []CompareItem `json:"still_failing"`
	NewTests     []CompareItem `json:"new_tests"`
	Slower       []CompareItem `json:"slower"`
}

// PreviousRunID returns the most recent earlier finished run of the same context that shares at
// least one test identity with runID. When the branch has no such run, it falls back to the same
// project and environment on another branch (reason BaseOtherBranch). 0 if none.
func (s *Store) PreviousRunID(runID int64) (int64, string, error) {
	var id sql.NullInt64
	args := append([]any{runID}, ctxArgs(runID)...)
	err := s.db.QueryRow(`SELECT MAX(t.run_id) FROM tests t JOIN runs r ON r.id = t.run_id
		WHERE t.run_id < ? AND r.status != 'RUNNING' AND `+sameContext+`
		  AND t.test_key IN (SELECT test_key FROM tests WHERE run_id = ?)`, append(args, runID)...).Scan(&id)
	if err != nil || id.Int64 != 0 {
		return id.Int64, BaseSameContext, err
	}
	err = s.db.QueryRow(`SELECT MAX(t.run_id) FROM tests t JOIN runs r ON r.id = t.run_id
		WHERE t.run_id < ? AND r.status != 'RUNNING'
		  AND r.project = (SELECT project FROM runs WHERE id = ?) AND r.environment = (SELECT environment FROM runs WHERE id = ?)
		  AND t.test_key IN (SELECT test_key FROM tests WHERE run_id = ?)`, runID, runID, runID, runID).Scan(&id)
	return id.Int64, BaseOtherBranch, err
}

// CompatibleRuns lists the finished runs of the same project and environment (any branch) that
// can be chosen as the base of a comparison, newest first.
func (s *Store) CompatibleRuns(runID int64, limit int) ([]Run, error) {
	// una sola consulta: las ejecuciones elegidas y sus contadores agrupados (los mismos que
	// GetRun/countersOf), en vez de una consulta por ejecución
	rows, err := s.db.Query(`
		SELECT r.id, r.name, r.environment, r.status, r.started_at, r.ended_at,
		       r.project, r.branch, r.commit_sha, r.framework, r.incomplete,
		       COUNT(t.id),
		       COALESCE(SUM(t.status='PASS'),0),
		       COALESCE(SUM(t.status='FAIL'),0),
		       COALESCE(SUM(t.status='SKIP'),0),
		       COALESCE(SUM(t.status='WARNING'),0),
		       COALESCE(SUM(t.status='RUNNING'),0),
		       COALESCE(SUM(t.status='FAIL' AND `+activeQuarantine+`),0)
		FROM (SELECT c.id FROM runs c
		      WHERE c.id != ? AND c.status != 'RUNNING'
		        AND c.project = (SELECT project FROM runs WHERE id = ?) AND c.environment = (SELECT environment FROM runs WHERE id = ?)
		      ORDER BY c.id DESC LIMIT ?) sel
		JOIN runs r ON r.id = sel.id
		LEFT JOIN tests t ON t.run_id = r.id
		GROUP BY r.id ORDER BY r.id DESC`, NowMs(), runID, runID, runID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Name, &r.Environment, &r.Status, &r.StartedAt, &r.EndedAt,
			&r.Project, &r.Branch, &r.Commit, &r.Framework, &r.Incomplete,
			&r.Total, &r.Passed, &r.Failed, &r.Skipped, &r.Warning, &r.Running, &r.Quarantined); err != nil {
			return nil, err
		}
		r.deriveStatus()
		out = append(out, r)
	}
	return out, rows.Err()
}

type testOutcome struct {
	id       int64
	name     string
	status   string
	duration int64
	finished bool
}

func (s *Store) outcomesByKey(runID int64) (map[string]testOutcome, error) {
	rows, err := s.db.Query(`SELECT id, name, test_key, status, started_at, ended_at FROM tests WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]testOutcome{}
	for rows.Next() {
		var o testOutcome
		var key string
		var started int64
		var ended sql.NullInt64
		if err := rows.Scan(&o.id, &o.name, &key, &o.status, &started, &ended); err != nil {
			return nil, err
		}
		if ended.Valid {
			o.duration, o.finished = ended.Int64-started, true
		}
		out[key] = o // the last execution of an identity in the run wins
	}
	return out, rows.Err()
}

// CompareRuns compares runID against baseID (0 = the previous run of the same context).
func (s *Store) CompareRuns(runID, baseID int64) (*Comparison, error) {
	if _, err := s.GetRun(runID); err != nil {
		return nil, err
	}
	c := &Comparison{NewFailures: []CompareItem{}, Fixed: []CompareItem{}, StillFailing: []CompareItem{}, NewTests: []CompareItem{}, Slower: []CompareItem{}}
	if baseID == 0 {
		var err error
		if baseID, c.BaseReason, err = s.PreviousRunID(runID); err != nil {
			return nil, err
		}
	} else {
		c.BaseReason = BaseChosen
	}
	if baseID == 0 {
		c.BaseReason = ""
		return c, nil
	}
	base, err := s.GetRun(baseID)
	if err != nil {
		return nil, err
	}
	c.BaseRun = base
	cur, err := s.outcomesByKey(runID)
	if err != nil {
		return nil, err
	}
	prev, err := s.outcomesByKey(baseID)
	if err != nil {
		return nil, err
	}
	for key, o := range cur {
		item := CompareItem{Name: o.name, Key: key, TestID: o.id, Status: o.status, DurationMs: o.duration}
		p, existed := prev[key]
		if !existed {
			c.NewTests = append(c.NewTests, item)
			continue
		}
		item.BaseStatus, item.BaseDurationMs = p.status, p.duration
		switch {
		case o.status == "FAIL" && p.status == "FAIL":
			c.StillFailing = append(c.StillFailing, item)
		case o.status == "FAIL":
			c.NewFailures = append(c.NewFailures, item)
		case p.status == "FAIL" && (o.status == "PASS" || o.status == "WARNING"):
			c.Fixed = append(c.Fixed, item)
		}
		// 50% más lento y al menos 1 s más: evita marcar ruido de milisegundos
		if o.finished && p.finished && o.status != "FAIL" && p.duration > 0 &&
			o.duration > p.duration*3/2 && o.duration-p.duration >= 1000 {
			c.Slower = append(c.Slower, item)
		}
	}
	for _, list := range [][]CompareItem{c.NewFailures, c.Fixed, c.StillFailing, c.NewTests, c.Slower} {
		sort.Slice(list, func(i, j int) bool { return list[i].TestID < list[j].TestID })
	}
	return c, nil
}

// ---------- Endpoint ranking ----------

// EndpointStat aggregates the calls to one backend endpoint during a run.
type EndpointStat struct {
	Method string `json:"method"`
	Host   string `json:"host"`
	Path   string `json:"path"` // normalized: ids replaced by :id
	Count  int    `json:"count"`
	Errors int    `json:"errors"`
	AvgMs  int64  `json:"avg_ms"`
	P95Ms  int64  `json:"p95_ms"`
	MaxMs  int64  `json:"max_ms"`
	TestID int64  `json:"test_id"` // a test where it was called (to jump to its network tab)
}

var idSegment = regexp.MustCompile(`^(\d+|[0-9a-fA-F]{8}-[0-9a-fA-F-]{27,}|[0-9a-fA-F]{24,})$`)

// NormalizeEndpoint returns host and path with variable segments (numeric ids, UUIDs, hashes)
// replaced by ":id", so /users/42 and /users/7 count as the same endpoint.
func NormalizeEndpoint(raw string) (string, string) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", raw
	}
	segs := strings.Split(u.Path, "/")
	for i, seg := range segs {
		if idSegment.MatchString(seg) {
			segs[i] = ":id"
		}
	}
	return u.Host, strings.Join(segs, "/")
}

// RunEndpoints ranks the API/document endpoints called during a run: endpoints with errors
// first, then by p95 duration. Static assets (scripts, images, fonts, css) are ignored.
func (s *Store) RunEndpoints(runID int64, limit int) ([]EndpointStat, error) {
	rows, err := s.db.Query(`
		SELECT n.method, n.url, n.status, ((n.failed = 1 OR n.status >= 400) AND n.expected = 0), n.duration_ms, n.test_id
		FROM network n JOIN tests t ON t.id = n.test_id
		WHERE t.run_id = ? AND n.resource_type IN ('xhr', 'fetch', 'document', '')`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type acc struct {
		EndpointStat
		durations []int64
	}
	byKey := map[string]*acc{}
	for rows.Next() {
		var method, rawURL string
		var status int
		var failed bool
		var dur sql.NullInt64
		var testID int64
		if err := rows.Scan(&method, &rawURL, &status, &failed, &dur, &testID); err != nil {
			return nil, err
		}
		host, path := NormalizeEndpoint(rawURL)
		key := method + " " + host + path
		a := byKey[key]
		if a == nil {
			a = &acc{EndpointStat: EndpointStat{Method: method, Host: host, Path: path, TestID: testID}}
			byKey[key] = a
		}
		a.Count++
		if failed {
			a.Errors++
			a.TestID = testID // prefer a test where it failed
		}
		if dur.Valid {
			a.durations = append(a.durations, dur.Int64)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]EndpointStat, 0, len(byKey))
	for _, a := range byKey {
		if n := len(a.durations); n > 0 {
			sort.Slice(a.durations, func(i, j int) bool { return a.durations[i] < a.durations[j] })
			var sum int64
			for _, d := range a.durations {
				sum += d
			}
			a.AvgMs = sum / int64(n)
			a.P95Ms = a.durations[(n*95+99)/100-1]
			a.MaxMs = a.durations[n-1]
		}
		out = append(out, a.EndpointStat)
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].Errors > 0) != (out[j].Errors > 0) {
			return out[i].Errors > 0
		}
		if out[i].P95Ms != out[j].P95Ms {
			return out[i].P95Ms > out[j].P95Ms
		}
		return out[i].Count > out[j].Count
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// ---------- Run triage (diagnosis of the whole run) ----------

// Incident groups failed tests that share discriminating evidence (the same failing backend
// call before the failure, or the same error signature). Title, Evidence, Exception and Location
// are observed facts; Cause and Action are a hypothesis written by the AI (never confirmed).
type Incident struct {
	Key       string     `json:"key"`
	Title     string     `json:"title"`
	Kind      string     `json:"kind"` // backend | error
	Host      string     `json:"host,omitempty"`
	Exception string     `json:"exception,omitempty"`
	Location  string     `json:"location,omitempty"`
	Category  string     `json:"category,omitempty"` // categoría de la IA del primer test (informativa)
	TestIDs   []int64    `json:"test_ids"`
	TestNames []string   `json:"test_names"`
	Evidence  []Evidence `json:"evidence,omitempty"`
	Cause     string     `json:"cause"`  // hipótesis de la IA
	Action    string     `json:"action"` // siguiente paso sugerido
	// Insufficient: the AI said the evidence does not support a cause (it abstained).
	Insufficient bool `json:"insufficient,omitempty"`
}

// Evidence is an observed fact behind an incident, with a reference to the artifact.
type Evidence struct {
	TestID int64  `json:"test_id"`
	Kind   string `json:"kind"`          // network | error | screenshot
	Ref    int64  `json:"ref,omitempty"` // id de la conexión o del paso
	Text   string `json:"text"`
	URL    string `json:"url,omitempty"` // captura
}

// RunTriage is the diagnosis of a whole run: a headline for humans plus failure incidents.
// Incidents always holds every group; only the first AIIncidents went to the AI.
type RunTriage struct {
	State       string     `json:"state"`
	Headline    string     `json:"headline"`
	Summary     string     `json:"summary"`
	Incidents   []Incident `json:"incidents"`
	AI          bool       `json:"ai"`
	AIIncidents int        `json:"ai_incidents"`
	// PendingTests: failed tests whose own AI diagnosis was still running when this summary was
	// written (it is rewritten when they finish).
	PendingTests int    `json:"pending_tests"`
	Error        string `json:"error,omitempty"`
	UpdatedAt    int64  `json:"updated_at"`
}

// SetRunTriagePending marks a run as awaiting its diagnosis.
func (s *Store) SetRunTriagePending(runID int64) error {
	_, err := s.db.Exec(`INSERT INTO run_triage(run_id, state, updated_at) VALUES(?, 'PENDING', ?)
		ON CONFLICT(run_id) DO UPDATE SET state='PENDING', error='', updated_at=excluded.updated_at`, runID, NowMs())
	return err
}

// SaveRunTriage stores a finished run diagnosis.
func (s *Store) SaveRunTriage(runID int64, rt *RunTriage) error {
	inc, err := json.Marshal(rt.Incidents)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO run_triage(run_id, state, headline, summary, incidents, ai, error, updated_at, ai_incidents, pending_tests)
		VALUES(?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(run_id) DO UPDATE SET state=excluded.state, headline=excluded.headline, summary=excluded.summary,
			incidents=excluded.incidents, ai=excluded.ai, error=excluded.error, updated_at=excluded.updated_at,
			ai_incidents=excluded.ai_incidents, pending_tests=excluded.pending_tests`,
		runID, rt.State, rt.Headline, rt.Summary, string(inc), rt.AI, rt.Error, NowMs(), rt.AIIncidents, rt.PendingTests)
	return err
}

// GetRunTriage returns the run diagnosis, or nil if there is none.
func (s *Store) GetRunTriage(runID int64) (*RunTriage, error) {
	var rt RunTriage
	var inc string
	err := s.db.QueryRow(`SELECT state, headline, summary, incidents, ai, error, updated_at, ai_incidents, pending_tests
		FROM run_triage WHERE run_id=?`, runID).
		Scan(&rt.State, &rt.Headline, &rt.Summary, &inc, &rt.AI, &rt.Error, &rt.UpdatedAt, &rt.AIIncidents, &rt.PendingTests)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(inc), &rt.Incidents)
	if rt.Incidents == nil {
		rt.Incidents = []Incident{}
	}
	return &rt, nil
}

// PendingTestTriage counts failed tests of a run whose AI analysis is still running.
func (s *Store) PendingTestTriage(runID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM ai_triage a JOIN tests t ON t.id = a.test_id
		WHERE t.run_id = ? AND a.state = 'PENDING'`, runID).Scan(&n)
	return n, err
}

// FirstNetworkError returns the first failed connection of a test (nil if none).
func (s *Store) FirstNetworkError(testID int64) (*NetConn, error) {
	conns, err := s.ListNetworkErrors(testID, 1)
	if err != nil || len(conns) == 0 {
		return nil, err
	}
	return &conns[0], nil
}
