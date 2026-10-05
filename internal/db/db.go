// Package db is the SQLite persistence layer for TraceReports.
package db

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// ErrNotFound is returned when a run or test does not exist.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS runs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	name        TEXT    NOT NULL,
	environment TEXT    NOT NULL DEFAULT '',
	status      TEXT    NOT NULL DEFAULT 'RUNNING',
	started_at  INTEGER NOT NULL,
	ended_at    INTEGER,
	total       INTEGER NOT NULL DEFAULT 0,
	passed      INTEGER NOT NULL DEFAULT 0,
	failed      INTEGER NOT NULL DEFAULT 0,
	skipped     INTEGER NOT NULL DEFAULT 0,
	warning     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS tests (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id        INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	name          TEXT    NOT NULL,
	category      TEXT    NOT NULL DEFAULT '',
	description   TEXT    NOT NULL DEFAULT '',
	status        TEXT    NOT NULL DEFAULT 'RUNNING',
	started_at    INTEGER NOT NULL,
	ended_at      INTEGER,
	error_message TEXT    NOT NULL DEFAULT '',
	error_trace   TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_tests_run ON tests(run_id);

CREATE TABLE IF NOT EXISTS logs (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	test_id    INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	status     TEXT    NOT NULL,
	message    TEXT    NOT NULL DEFAULT '',
	timestamp  INTEGER NOT NULL,
	screenshot TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_logs_test ON logs(test_id);

CREATE TABLE IF NOT EXISTS ai_triage (
	test_id    INTEGER PRIMARY KEY REFERENCES tests(id) ON DELETE CASCADE,
	state      TEXT    NOT NULL,            -- PENDING | DONE | ERROR
	category   TEXT    NOT NULL DEFAULT '',
	summary    TEXT    NOT NULL DEFAULT '',
	suggestion TEXT    NOT NULL DEFAULT '',
	error      TEXT    NOT NULL DEFAULT '',
	updated_at INTEGER NOT NULL
);
`

// Open opens (or creates) the database at path and applies the schema.
func Open(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", filepath.ToSlash(path))
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite allows a single writer; one connection avoids SQLITE_BUSY under concurrent writes.
	sqldb.SetMaxOpenConns(1)
	if _, err := sqldb.Exec(schema + networkSchema + insightsSchema + domSchema + settingsSchema + escalationSchema + idempotencySchema); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Columns added after the first release (CREATE TABLE IF NOT EXISTS skips existing tables).
	for _, c := range [][3]string{
		{"network", "evidence_file", "TEXT NOT NULL DEFAULT ''"},
		{"network", "expected", "INTEGER NOT NULL DEFAULT 0"},
		{"ai_triage", "locator_pick", "TEXT NOT NULL DEFAULT ''"},
		{"ai_triage", "locator_reason", "TEXT NOT NULL DEFAULT ''"},
		// contexto de la ejecución: los historiales solo comparan ejecuciones compatibles
		{"runs", "project", "TEXT NOT NULL DEFAULT ''"},
		{"runs", "branch", "TEXT NOT NULL DEFAULT ''"},
		{"runs", "commit_sha", "TEXT NOT NULL DEFAULT ''"},
		{"runs", "framework", "TEXT NOT NULL DEFAULT ''"},
		{"runs", "incomplete", "INTEGER NOT NULL DEFAULT 0"},
		// identidad estable del test (p. ej. el nodeid de pytest), aparte del nombre visible
		{"tests", "test_key", "TEXT NOT NULL DEFAULT ''"},
		{"tests", "suite", "TEXT NOT NULL DEFAULT ''"},
		{"tests", "params", "TEXT NOT NULL DEFAULT ''"},
		{"tests", "worker", "TEXT NOT NULL DEFAULT ''"},
		{"tests", "attempts", "INTEGER NOT NULL DEFAULT 1"},
		// versión del resultado: sube cada vez que cambia (un diagnóstico de una versión vieja no se guarda)
		{"tests", "result_rev", "INTEGER NOT NULL DEFAULT 0"},
		{"run_triage", "ai_incidents", "INTEGER NOT NULL DEFAULT 0"},
		{"run_triage", "pending_tests", "INTEGER NOT NULL DEFAULT 0"},
		// la clave de idempotencia vive lo mismo que la evidencia de su ejecución
		{"idempotency", "run_id", "INTEGER REFERENCES runs(id) ON DELETE CASCADE"},
	} {
		if err := ensureColumn(sqldb, c[0], c[1], c[2]); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("migrate: %w", err)
		}
	}
	if _, err := sqldb.Exec(migrations); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	if err := backfillIdempotencyRuns(sqldb); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrate idempotency: %w", err)
	}
	return &Store{db: sqldb}, nil
}

// migrations run on every start (idempotent). Tests stored before the stable identity keep
// their history, matched by name: their key is "name:<name>" and the UI marks it approximate.
const migrations = `
UPDATE tests SET test_key = 'name:' || name WHERE test_key = '';
CREATE INDEX IF NOT EXISTS idx_tests_key ON tests(test_key, run_id);
CREATE INDEX IF NOT EXISTS idx_runs_context ON runs(project, environment, branch, id);
`

// NameKey is the identity of a test reported without its own key: only its name, so two
// tests with the same name in different files share it (approximate history).
func NameKey(name string) string { return "name:" + name }

// ensureColumn adds column to table when an older database does not have it yet.
func ensureColumn(sqldb *sql.DB, table, column, definition string) error {
	rows, err := sqldb.Query(`SELECT name FROM pragma_table_info(?)`, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	_, err = sqldb.Exec(fmt.Sprintf(`ALTER TABLE %s ADD COLUMN %s %s`, table, column, definition))
	return err
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// NowMs returns the current time in Unix milliseconds.
func NowMs() int64 { return time.Now().UnixMilli() }

// ---------- Models ----------

// Counters aggregates test outcomes.
type Counters struct {
	Total   int `json:"total"`
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
	Warning int `json:"warning"`
	Running int `json:"running"`
}

// Run is a test execution / suite.
type Run struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment"`
	Status      string `json:"status"`
	StartedAt   int64  `json:"started_at"`
	EndedAt     *int64 `json:"ended_at"`
	RunMeta
	// Incomplete: the run was closed with tests that never finished (crashed worker,
	// cancellation) or the client reported an interruption.
	Incomplete bool `json:"incomplete"`
	Counters
}

// RunMeta is the context of a run. History, flakiness and comparisons only use runs of the same
// project, environment and branch, so staging never mixes with production.
type RunMeta struct {
	Project   string `json:"project"`
	Branch    string `json:"branch"`
	Commit    string `json:"commit"`
	Framework string `json:"framework"`
}

// Test is a single test case inside a run.
type Test struct {
	ID           int64  `json:"id"`
	RunID        int64  `json:"run_id"`
	Name         string `json:"name"`
	Category     string `json:"category"`
	Description  string `json:"description"`
	Status       string `json:"status"`
	StartedAt    int64  `json:"started_at"`
	EndedAt      *int64 `json:"ended_at"`
	ErrorMessage string `json:"error_message"`
	ErrorTrace   string `json:"error_trace"`
	TestMeta
	// Attempts is how many times the runner executed it (>1: retried, e.g. pytest-rerunfailures).
	Attempts int `json:"attempts"`
	// KeyApprox: reported without its own key; its history is matched by name only.
	KeyApprox bool `json:"key_approx,omitempty"`
	// Conexiones de red capturadas y cuántas no terminaron OK (HTTP >= 400 o sin respuesta).
	NetworkTotal  int `json:"network_total"`
	NetworkErrors int `json:"network_errors"`
	// Flaky: en sus últimas ejecuciones (mismo nombre) alterna entre pasar y fallar.
	Flaky     bool       `json:"flaky"`
	FlakyInfo *FlakyInfo `json:"flaky_info,omitempty"`
	// NetDrift: el p95 de su red empeoró frente a su propio historial.
	NetDrift *NetDrift `json:"net_drift,omitempty"`
	Triage   *Triage   `json:"triage"`
	Logs     []Log     `json:"logs,omitempty"`
}

// TestMeta identifies a test beyond its visible name.
type TestMeta struct {
	Key    string `json:"key"`    // identidad estable dentro del proyecto (nodeid de pytest, paquete/Test de Go...)
	Suite  string `json:"suite"`  // archivo o clase
	Params string `json:"params"` // parámetros normalizados de un test parametrizado
	Worker string `json:"worker"` // worker o shard que lo ejecutó (pytest-xdist: gw0...)
}

// Log is a step inside a test.
type Log struct {
	ID         int64  `json:"id"`
	TestID     int64  `json:"test_id"`
	Status     string `json:"status"`
	Message    string `json:"message"`
	Timestamp  int64  `json:"timestamp"`
	Screenshot string `json:"screenshot"`
}

// Triage is the AI diagnosis of a failed test.
type Triage struct {
	State      string `json:"state"`
	Category   string `json:"category"`
	Summary    string `json:"summary"`
	Suggestion string `json:"suggestion"`
	Error      string `json:"error,omitempty"`
	UpdatedAt  int64  `json:"updated_at"`
	// Locator recomendado por la IA entre los candidatos del snapshot (si el fallo fue de locator).
	LocatorPick   string `json:"locator_pick,omitempty"`
	LocatorReason string `json:"locator_reason,omitempty"`
}

// RunDetail is a run with its tests and step statistics (for the dashboard).
type RunDetail struct {
	Run
	Tests     []Test         `json:"tests"`
	StepStats map[string]int `json:"step_stats"`
	// Summary is the diagnosis of the whole run (nil until the run is finished).
	Summary *RunTriage `json:"summary"`
}

// ---------- Runs ----------

// CreateRun inserts a new run in RUNNING state.
func (s *Store) CreateRun(name, environment string) (int64, error) {
	return s.CreateRunWithMeta(name, environment, RunMeta{})
}

// CreateRunWithMeta inserts a new run with its context (project, branch, commit, framework).
func (s *Store) CreateRunWithMeta(name, environment string, m RunMeta) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO runs(name, environment, started_at, project, branch, commit_sha, framework) VALUES(?,?,?,?,?,?,?)`,
		name, environment, NowMs(), m.Project, m.Branch, m.Commit, m.Framework)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// querier is what *sql.DB and *sql.Tx share: the helpers below run inside or outside a transaction.
type querier interface {
	Exec(query string, args ...any) (sql.Result, error)
	QueryRow(query string, args ...any) *sql.Row
}

// countersFor computes live counters from the tests of a run.
func (s *Store) countersFor(runID int64) (Counters, error) { return countersOf(s.db, runID) }

func countersOf(q querier, runID int64) (Counters, error) {
	var c Counters
	err := q.QueryRow(`
		SELECT COUNT(*),
		       COALESCE(SUM(status='PASS'),0),
		       COALESCE(SUM(status='FAIL'),0),
		       COALESCE(SUM(status='SKIP'),0),
		       COALESCE(SUM(status='WARNING'),0),
		       COALESCE(SUM(status='RUNNING'),0)
		FROM tests WHERE run_id = ?`, runID).
		Scan(&c.Total, &c.Passed, &c.Failed, &c.Skipped, &c.Warning, &c.Running)
	return c, err
}

// InterruptedMessage is the error of the tests a run was closed without (they never finished).
const InterruptedMessage = "Interrumpido: el test no terminó antes de cerrar la ejecución (worker caído, timeout o cancelación)."

// FinishRun closes a run and persists consolidated counters.
func (s *Store) FinishRun(runID int64) (*Run, error) { return s.FinishRunWith(runID, false) }

// runStatus is the aggregate status of a closed run. Any FAIL wins; an incomplete run (interrupted
// now or in an earlier close) or one with tests still RUNNING is never green.
func runStatus(c Counters, incomplete bool) string {
	switch {
	case c.Failed > 0:
		return "FAIL"
	case incomplete || c.Running > 0 || c.Warning > 0:
		return "WARNING"
	case c.Total > 0 && c.Skipped == c.Total:
		return "SKIP"
	}
	return "PASS"
}

// refreshClosedRun recomputes status and counters of an already closed run after one of its tests
// changed (late result recovered from a spool, test created after the close). Open runs are left
// alone: their status is set when they close.
func refreshClosedRun(q querier, runID int64) error {
	var ended sql.NullInt64
	var incomplete bool
	if err := q.QueryRow(`SELECT ended_at, incomplete FROM runs WHERE id=?`, runID).Scan(&ended, &incomplete); err != nil {
		return err
	}
	if !ended.Valid {
		return nil
	}
	c, err := countersOf(q, runID)
	if err != nil {
		return err
	}
	_, err = q.Exec(`UPDATE runs SET status=?, total=?, passed=?, failed=?, skipped=?, warning=? WHERE id=?`,
		runStatus(c, incomplete), c.Total, c.Passed, c.Failed, c.Skipped, c.Warning, runID)
	return err
}

// FinishRunWith closes a run. Tests still RUNNING are closed as FAIL with InterruptedMessage and
// the run is marked incomplete (also when the client reports interrupted): an unfinished run
// never looks successful.
func (s *Store) FinishRunWith(runID int64, interrupted bool) (*Run, error) {
	run, _, err := s.CloseRun(runID, interrupted)
	return run, err
}

// CloseRun is FinishRunWith that also reports whether this call closed the run (first is false
// when it was already closed: a repeated close, e.g. a replayed spool). A repeated close keeps
// the first close time and the incomplete mark of any earlier close. It runs in one transaction,
// so a test result arriving at the same time is counted either before or after, never half.
func (s *Store) CloseRun(runID int64, interrupted bool) (run *Run, first bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	var ended sql.NullInt64
	var wasIncomplete bool
	err = tx.QueryRow(`SELECT ended_at, incomplete FROM runs WHERE id=?`, runID).Scan(&ended, &wasIncomplete)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, ErrNotFound
	}
	if err != nil {
		return nil, false, err
	}
	res, err := tx.Exec(`UPDATE tests SET status='FAIL', ended_at=?, error_message=? WHERE run_id=? AND status='RUNNING'`,
		NowMs(), InterruptedMessage, runID)
	if err != nil {
		return nil, false, err
	}
	if n, _ := res.RowsAffected(); n > 0 {
		interrupted = true
	}
	incomplete := interrupted || wasIncomplete
	c, err := countersOf(tx, runID)
	if err != nil {
		return nil, false, err
	}
	_, err = tx.Exec(`UPDATE runs SET status=?, ended_at=COALESCE(ended_at, ?), total=?, passed=?, failed=?, skipped=?, warning=?,
		incomplete=? WHERE id=?`,
		runStatus(c, incomplete), NowMs(), c.Total, c.Passed, c.Failed, c.Skipped, c.Warning, incomplete, runID)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	run, err = s.GetRun(runID)
	return run, !ended.Valid, err
}

// GetRun returns a run with live counters.
func (s *Store) GetRun(runID int64) (*Run, error) {
	var r Run
	err := s.db.QueryRow(`SELECT id, name, environment, status, started_at, ended_at, project, branch, commit_sha, framework, incomplete
		FROM runs WHERE id=?`, runID).
		Scan(&r.ID, &r.Name, &r.Environment, &r.Status, &r.StartedAt, &r.EndedAt,
			&r.Project, &r.Branch, &r.Commit, &r.Framework, &r.Incomplete)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if r.Counters, err = s.countersFor(runID); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListRuns returns the most recent runs with live counters.
func (s *Store) ListRuns(limit int) ([]Run, error) {
	rows, err := s.db.Query(`
		SELECT r.id, r.name, r.environment, r.status, r.started_at, r.ended_at,
		       r.project, r.branch, r.commit_sha, r.framework, r.incomplete,
		       COUNT(t.id),
		       COALESCE(SUM(t.status='PASS'),0),
		       COALESCE(SUM(t.status='FAIL'),0),
		       COALESCE(SUM(t.status='SKIP'),0),
		       COALESCE(SUM(t.status='WARNING'),0),
		       COALESCE(SUM(t.status='RUNNING'),0)
		FROM runs r LEFT JOIN tests t ON t.run_id = r.id
		GROUP BY r.id ORDER BY r.id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []Run{}
	for rows.Next() {
		var r Run
		if err := rows.Scan(&r.ID, &r.Name, &r.Environment, &r.Status, &r.StartedAt, &r.EndedAt,
			&r.Project, &r.Branch, &r.Commit, &r.Framework, &r.Incomplete, &r.Total, &r.Passed, &r.Failed, &r.Skipped, &r.Warning, &r.Running); err != nil {
			return nil, err
		}
		runs = append(runs, r)
	}
	return runs, rows.Err()
}

// GetRunDetail returns a run, its tests (with triage, without logs) and step stats.
func (s *Store) GetRunDetail(runID int64) (*RunDetail, error) {
	run, err := s.GetRun(runID)
	if err != nil {
		return nil, err
	}
	d := &RunDetail{Run: *run, Tests: []Test{}, StepStats: map[string]int{}}

	rows, err := s.db.Query(testSelect+` WHERE t.run_id = ? ORDER BY t.id`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		t, err := scanTest(rows)
		if err != nil {
			return nil, err
		}
		d.Tests = append(d.Tests, *t)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	flaky, err := s.flakyByKey(runID)
	if err != nil {
		return nil, err
	}
	drift, err := s.networkDrift(runID)
	if err != nil {
		return nil, err
	}
	for i := range d.Tests {
		if info := flaky[d.Tests[i].Key]; info != nil {
			d.Tests[i].FlakyInfo = info
			d.Tests[i].Flaky = info.Kind == StabilityFlaky
		}
		d.Tests[i].NetDrift = drift[d.Tests[i].ID]
	}
	if d.Summary, err = s.GetRunTriage(runID); err != nil {
		return nil, err
	}

	srows, err := s.db.Query(`SELECT l.status, COUNT(*) FROM logs l JOIN tests t ON t.id = l.test_id
		WHERE t.run_id = ? GROUP BY l.status`, runID)
	if err != nil {
		return nil, err
	}
	defer srows.Close()
	for srows.Next() {
		var st string
		var n int
		if err := srows.Scan(&st, &n); err != nil {
			return nil, err
		}
		d.StepStats[st] = n
	}
	return d, srows.Err()
}

// ---------- Tests ----------

const testSelect = `
	SELECT t.id, t.run_id, t.name, t.category, t.description, t.status, t.started_at, t.ended_at,
	       t.error_message, t.error_trace, t.test_key, t.suite, t.params, t.worker, t.attempts,
	       (SELECT COUNT(*) FROM network n WHERE n.test_id = t.id),
	       (SELECT COUNT(*) FROM network n WHERE n.test_id = t.id AND (n.failed = 1 OR n.status >= 400) AND n.expected = 0),
	       a.state, a.category, a.summary, a.suggestion, a.error, a.updated_at, a.locator_pick, a.locator_reason
	FROM tests t LEFT JOIN ai_triage a ON a.test_id = t.id`

type scanner interface{ Scan(dest ...any) error }

func scanTest(sc scanner) (*Test, error) {
	var t Test
	var aState, aCat, aSum, aSug, aErr, aPick, aReason sql.NullString
	var aUpd sql.NullInt64
	if err := sc.Scan(&t.ID, &t.RunID, &t.Name, &t.Category, &t.Description, &t.Status, &t.StartedAt, &t.EndedAt,
		&t.ErrorMessage, &t.ErrorTrace, &t.Key, &t.Suite, &t.Params, &t.Worker, &t.Attempts, &t.NetworkTotal, &t.NetworkErrors, &aState, &aCat, &aSum, &aSug, &aErr, &aUpd, &aPick, &aReason); err != nil {
		return nil, err
	}
	t.KeyApprox = strings.HasPrefix(t.Key, "name:")
	if aState.Valid {
		t.Triage = &Triage{State: aState.String, Category: aCat.String, Summary: aSum.String,
			Suggestion: aSug.String, Error: aErr.String, UpdatedAt: aUpd.Int64, LocatorPick: aPick.String, LocatorReason: aReason.String}
	}
	return &t, nil
}

// CreateTest starts a test inside a run.
func (s *Store) CreateTest(runID int64, name, category, description string) (int64, error) {
	return s.CreateTestWithMeta(runID, name, category, description, TestMeta{})
}

// CreateTestWithMeta starts a test with its identity. Without m.Key the identity is the name
// (NameKey): homonymous tests share their history.
func (s *Store) CreateTestWithMeta(runID int64, name, category, description string, m TestMeta) (int64, error) {
	if _, err := s.GetRun(runID); err != nil {
		return 0, err
	}
	if m.Key == "" {
		m.Key = NameKey(name)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`INSERT INTO tests(run_id, name, category, description, started_at, test_key, suite, params, worker)
		VALUES(?,?,?,?,?,?,?,?,?)`,
		runID, name, category, description, NowMs(), m.Key, m.Suite, m.Params, m.Worker)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	// creado después del cierre (spool recuperado): la ejecución deja de verse verde mientras corre
	if err := refreshClosedRun(tx, runID); err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// GetTest returns a test with its logs and triage.
func (s *Store) GetTest(testID int64) (*Test, error) {
	t, err := scanTest(s.db.QueryRow(testSelect+` WHERE t.id = ?`, testID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT id, test_id, status, message, timestamp, screenshot FROM logs WHERE test_id=? ORDER BY timestamp, id`, testID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	t.Logs = []Log{}
	for rows.Next() {
		var l Log
		if err := rows.Scan(&l.ID, &l.TestID, &l.Status, &l.Message, &l.Timestamp, &l.Screenshot); err != nil {
			return nil, err
		}
		t.Logs = append(t.Logs, l)
	}
	return t, rows.Err()
}

// SetAttempts records how many times the runner executed the test (retries).
func (s *Store) SetAttempts(testID int64, attempts int) error {
	if attempts < 1 {
		attempts = 1
	}
	_, err := s.db.Exec(`UPDATE tests SET attempts=? WHERE id=?`, attempts, testID)
	return err
}

// FinishTest closes a test. If status is empty it is derived from the logged steps. A result
// that arrives after its run was closed (spool recovery) is accepted and the run aggregate is
// recomputed in the same transaction: a closed run never stays PASS with a failed test.
func (s *Store) FinishTest(testID int64, status, errMsg, errTrace string) (*Test, error) {
	t, _, err := s.FinishTestChange(testID, status, errMsg, errTrace)
	return t, err
}

// ResultChange says what a FinishTestChange changed.
type ResultChange struct {
	Changed bool // el resultado guardado es distinto (estado, error o traza); un replay idéntico no
	Late    bool // y además la ejecución ya estaba cerrada: su resumen describe evidencia vieja
}

// FinishTestChange is FinishTest that also reports what changed. Late: the run was already closed
// and this result changed the test (status, error or trace). Then the run summary describes old
// evidence and must be rebuilt; an identical replay is not a change. When a finished test gets a
// different result, its AI diagnosis is dropped (it described the previous error) and its result
// version goes up, so an analysis of the old result still running cannot save over it.
func (s *Store) FinishTestChange(testID int64, status, errMsg, errTrace string) (t *Test, ch ResultChange, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, ResultChange{}, err
	}
	defer tx.Rollback()
	var runID int64
	var prevStatus, prevMsg, prevTrace string
	var runEnded sql.NullInt64
	err = tx.QueryRow(`SELECT t.run_id, t.status, t.error_message, t.error_trace, r.ended_at FROM tests t JOIN runs r ON r.id = t.run_id WHERE t.id=?`, testID).
		Scan(&runID, &prevStatus, &prevMsg, &prevTrace, &runEnded)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ResultChange{}, ErrNotFound
	}
	if err != nil {
		return nil, ResultChange{}, err
	}
	if status == "" {
		var fails, warns int
		if err := tx.QueryRow(`SELECT COALESCE(SUM(status='FAIL'),0), COALESCE(SUM(status='WARNING'),0) FROM logs WHERE test_id=?`, testID).
			Scan(&fails, &warns); err != nil {
			return nil, ResultChange{}, err
		}
		status = "PASS"
		if fails > 0 {
			status = "FAIL"
		} else if warns > 0 {
			status = "WARNING"
		}
	}
	changed := status != prevStatus || errMsg != prevMsg || errTrace != prevTrace
	if _, err := tx.Exec(`UPDATE tests SET status=?, ended_at=?, error_message=?, error_trace=?, result_rev = result_rev + ? WHERE id=?`,
		status, NowMs(), errMsg, errTrace, changed, testID); err != nil {
		return nil, ResultChange{}, err
	}
	if changed && prevStatus != "RUNNING" {
		if _, err := tx.Exec(`DELETE FROM ai_triage WHERE test_id=?`, testID); err != nil {
			return nil, ResultChange{}, err
		}
	}
	if err := refreshClosedRun(tx, runID); err != nil {
		return nil, ResultChange{}, err
	}
	if err := tx.Commit(); err != nil {
		return nil, ResultChange{}, err
	}
	ch = ResultChange{Changed: changed, Late: runEnded.Valid && changed}
	t, err = s.GetTest(testID)
	return t, ch, err
}

// ---------- Logs ----------

// AddLog appends a step to a test. timestamp is Unix ms (0 = now).
func (s *Store) AddLog(testID int64, status, message string, timestamp int64, screenshot string) (*Log, error) {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE id=?`, testID).Scan(&exists); err != nil {
		return nil, err
	}
	if exists == 0 {
		return nil, ErrNotFound
	}
	if timestamp == 0 {
		timestamp = NowMs()
	}
	res, err := s.db.Exec(`INSERT INTO logs(test_id, status, message, timestamp, screenshot) VALUES(?,?,?,?,?)`,
		testID, status, message, timestamp, screenshot)
	if err != nil {
		return nil, err
	}
	id, _ := res.LastInsertId()
	return &Log{ID: id, TestID: testID, Status: status, Message: message, Timestamp: timestamp, Screenshot: screenshot}, nil
}

// ---------- AI triage ----------

// SetTriagePending marks a test as awaiting AI analysis.
func (s *Store) SetTriagePending(testID int64) error {
	_, err := s.db.Exec(`INSERT INTO ai_triage(test_id, state, updated_at) VALUES(?, 'PENDING', ?)
		ON CONFLICT(test_id) DO UPDATE SET state='PENDING', category='', summary='', suggestion='', error='', updated_at=excluded.updated_at`,
		testID, NowMs())
	return err
}

// SaveTriage stores a completed AI analysis.
func (s *Store) SaveTriage(testID int64, category, summary, suggestion, locatorPick, locatorReason string) error {
	_, err := s.db.Exec(`UPDATE ai_triage SET state='DONE', category=?, summary=?, suggestion=?, error='', updated_at=?,
		locator_pick=?, locator_reason=? WHERE test_id=?`,
		category, summary, suggestion, NowMs(), locatorPick, locatorReason, testID)
	return err
}

// NeedsDiagnosis reports whether a test's current result is a failure (the only one diagnosed).
// When it is not, a PENDING diagnosis left by a superseded failure is dropped.
func (s *Store) NeedsDiagnosis(testID int64) (bool, error) {
	var status string
	err := s.db.QueryRow(`SELECT status FROM tests WHERE id=?`, testID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrNotFound
	}
	if err != nil || status == "FAIL" {
		return status == "FAIL", err
	}
	_, err = s.db.Exec(`DELETE FROM ai_triage WHERE test_id=? AND state='PENDING'`, testID)
	return false, err
}

// TestResultRev returns the version of a test's result (see FinishTestChange).
func (s *Store) TestResultRev(testID int64) (int64, error) {
	var rev int64
	err := s.db.QueryRow(`SELECT result_rev FROM tests WHERE id=?`, testID).Scan(&rev)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return rev, err
}

// SaveTriageAt is SaveTriage for the analysis of result version rev: if the result changed in
// the meantime, or is no longer a failure, nothing is saved (saved=false).
func (s *Store) SaveTriageAt(testID, rev int64, category, summary, suggestion, locatorPick, locatorReason string) (saved bool, err error) {
	res, err := s.db.Exec(`UPDATE ai_triage SET state='DONE', category=?, summary=?, suggestion=?, error='', updated_at=?,
		locator_pick=?, locator_reason=? WHERE test_id=?
		AND (SELECT result_rev FROM tests WHERE id=? AND status='FAIL') = ?`,
		category, summary, suggestion, NowMs(), locatorPick, locatorReason, testID, testID, rev)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// SaveTriageErrorAt is SaveTriageError for the analysis of result version rev (see SaveTriageAt).
func (s *Store) SaveTriageErrorAt(testID, rev int64, msg string) error {
	_, err := s.db.Exec(`UPDATE ai_triage SET state='ERROR', error=?, updated_at=? WHERE test_id=?
		AND (SELECT result_rev FROM tests WHERE id=? AND status='FAIL') = ?`, msg, NowMs(), testID, testID, rev)
	return err
}

// SaveTriageSkipped records that a test was not analyzed automatically (per-run budget).
func (s *Store) SaveTriageSkipped(testID int64, msg string) error {
	_, err := s.db.Exec(`INSERT INTO ai_triage(test_id, state, error, updated_at) VALUES(?, 'SKIPPED', ?, ?)
		ON CONFLICT(test_id) DO UPDATE SET state='SKIPPED', error=excluded.error, updated_at=excluded.updated_at`,
		testID, msg, NowMs())
	return err
}

// TriageBudget returns the triage state of a test ("" if never analyzed), how many tests of its
// run already used the AI (pending, done or failed: not skipped) and the run id.
func (s *Store) TriageBudget(testID int64) (state string, used int, runID int64, err error) {
	if runID, err = s.TestRunID(testID); err != nil {
		return
	}
	err = s.db.QueryRow(`SELECT COALESCE((SELECT state FROM ai_triage WHERE test_id = ?), ''),
		(SELECT COUNT(*) FROM ai_triage a JOIN tests t ON t.id = a.test_id WHERE t.run_id = ? AND a.state != 'SKIPPED')`,
		testID, runID).Scan(&state, &used)
	return
}

// PendingWork lists the per-test analyses and run diagnoses left PENDING (e.g. by a restart).
func (s *Store) PendingWork() (tests, runs []int64, err error) {
	collect := func(q string) ([]int64, error) {
		rows, err := s.db.Query(q)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		var ids []int64
		for rows.Next() {
			var id int64
			if err := rows.Scan(&id); err != nil {
				return nil, err
			}
			ids = append(ids, id)
		}
		return ids, rows.Err()
	}
	if tests, err = collect(`SELECT test_id FROM ai_triage WHERE state = 'PENDING' ORDER BY test_id`); err != nil {
		return
	}
	runs, err = collect(`SELECT run_id FROM run_triage WHERE state = 'PENDING' ORDER BY run_id`)
	return
}

// SaveTriageError records that the AI analysis failed.
func (s *Store) SaveTriageError(testID int64, msg string) error {
	_, err := s.db.Exec(`UPDATE ai_triage SET state='ERROR', error=?, updated_at=? WHERE test_id=?`, msg, NowMs(), testID)
	return err
}
