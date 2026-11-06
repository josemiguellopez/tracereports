package db

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// En vivo: lo que corre ahora, agrupado por ejecución, con lo que el servidor ya recibe de cualquier
// cliente (inicio, fin, worker, pasos). Sin cambios en el cliente; execution_id y planned lo mejoran.

// TouchTest records that something of the test arrived now (server clock): "no signal" in Live does not
// depend on the client's clock.
func (s *Store) TouchTest(testID int64) error {
	now := NowMs()
	_, err := s.db.Exec(`UPDATE tests SET last_signal = ? WHERE id = ? AND last_signal < ?`, now, testID, now)
	return err
}

// LiveRun is a run of an execution in Live.
type LiveRun struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Environment  string     `json:"environment"`
	Project      string     `json:"project"`
	Branch       string     `json:"branch"`
	Commit       string     `json:"commit"`
	ExecutionID  string     `json:"execution_id,omitempty"`
	Status       string     `json:"status"`
	StartedAt    int64      `json:"started_at"`
	EndedAt      *int64     `json:"ended_at,omitempty"`
	Planned      int        `json:"planned"`
	LastActivity int64      `json:"last_activity"`
	CancelAt     int64      `json:"cancel_at,omitempty"`
	CancelBy     string     `json:"cancel_by,omitempty"`
	CancelReason string     `json:"cancel_reason,omitempty"`
	Tests        []LiveTest `json:"tests"`
}

// LiveTest is a test of a run in Live, with its last step and its usual duration.
type LiveTest struct {
	ID         int64  `json:"id"`
	RunID      int64  `json:"run_id"`
	Name       string `json:"name"`
	Key        string `json:"-"`
	Worker     string `json:"worker"`
	Status     string `json:"status"`
	StartedAt  int64  `json:"started_at"`
	EndedAt    *int64 `json:"ended_at,omitempty"`
	Attempts   int    `json:"attempts"`
	LastSignal int64  `json:"last_signal"` // hora del servidor de lo último que llegó del test
	LastStep   string `json:"last_step"`
	LastStepAt int64  `json:"last_step_at,omitempty"`
	// P50 y P90 de la duración de este test en sus ejecuciones anteriores que terminaron (0 sin historial)
	P50  int64 `json:"p50,omitempty"`
	P90  int64 `json:"p90,omitempty"`
	Hist int   `json:"hist,omitempty"`
}

const liveRunCols = `r.id, r.name, r.environment, r.project, r.branch, r.commit_sha, r.execution_id, r.status, r.started_at,
	r.ended_at, r.planned, r.last_activity, r.cancel_at, r.cancel_by, r.cancel_reason`

func scanLiveRun(rows *sql.Rows) (LiveRun, error) {
	var r LiveRun
	var ended sql.NullInt64
	err := rows.Scan(&r.ID, &r.Name, &r.Environment, &r.Project, &r.Branch, &r.Commit, &r.ExecutionID, &r.Status, &r.StartedAt,
		&ended, &r.Planned, &r.LastActivity, &r.CancelAt, &r.CancelBy, &r.CancelReason)
	if ended.Valid {
		r.EndedAt = &ended.Int64
	}
	return r, err
}

// LiveCandidates are the runs Live can show: those still running and those that ended after since.
func (s *Store) LiveCandidates(since int64) ([]LiveRun, error) {
	rows, err := s.db.Query(`SELECT `+liveRunCols+` FROM runs r
		WHERE r.status = 'RUNNING' OR r.ended_at >= ? OR r.started_at >= ?
		ORDER BY r.started_at, r.id LIMIT 2000`, since, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LiveRun
	for rows.Next() {
		r, err := scanLiveRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Execution is a group of runs that belong together: the same execution_id, or, without it, the same
// project, environment, branch and commit started one after another (a launcher that creates a run per
// file, pytest-xdist workers with their own run).
type Execution struct {
	Key  string    `json:"key"`
	Runs []LiveRun `json:"runs"`
}

// ExecutionGap: without execution_id, a run joins the previous one of the same context if it started
// before that group went quiet for this long.
const ExecutionGap int64 = 10 * 60 * 1000

// GroupExecutions groups runs (oldest first) into executions. The key is "x:<execution_id>" or
// "g:<id of the first run>", stable while the execution grows.
func GroupExecutions(runs []LiveRun, now int64) []Execution {
	var out []Execution
	byKey := map[string]int{}
	type open struct {
		idx  int
		last int64
	}
	lastOf := map[string]*open{} // contexto → grupo abierto
	for _, r := range runs {
		if r.ExecutionID != "" {
			key := "x:" + r.ExecutionID
			if i, ok := byKey[key]; ok {
				out[i].Runs = append(out[i].Runs, r)
				continue
			}
			byKey[key] = len(out)
			out = append(out, Execution{Key: key, Runs: []LiveRun{r}})
			continue
		}
		ctx := strings.Join([]string{r.Project, r.Environment, r.Branch, r.Commit}, "\x00")
		end := now
		if r.EndedAt != nil {
			end = *r.EndedAt
		}
		if o := lastOf[ctx]; o != nil && r.StartedAt <= o.last+ExecutionGap {
			out[o.idx].Runs = append(out[o.idx].Runs, r)
			o.last = max(o.last, end)
			continue
		}
		lastOf[ctx] = &open{idx: len(out), last: end}
		out = append(out, Execution{Key: fmt.Sprintf("g:%d", r.ID), Runs: []LiveRun{r}})
	}
	return out
}

// LiveTests fills the tests of the runs, with the last step of each one.
func (s *Store) LiveTests(runs []LiveRun) error {
	if len(runs) == 0 {
		return nil
	}
	ids := make([]any, len(runs))
	idx := map[int64]int{}
	for i, r := range runs {
		ids[i] = r.ID
		idx[r.ID] = i
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")
	rows, err := s.db.Query(`SELECT t.id, t.run_id, t.name, t.test_key, t.worker, t.status, t.started_at, t.ended_at, t.attempts, t.last_signal,
			COALESCE(l.message, ''), COALESCE(l.ts, 0)
		FROM tests t
		LEFT JOIN (SELECT test_id, message, MAX(timestamp) AS ts FROM logs WHERE test_id IN (SELECT id FROM tests WHERE run_id IN (`+marks+`)) GROUP BY test_id) l
			ON l.test_id = t.id
		WHERE t.run_id IN (`+marks+`) ORDER BY t.id`, append(ids, ids...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var t LiveTest
		var ended sql.NullInt64
		if err := rows.Scan(&t.ID, &t.RunID, &t.Name, &t.Key, &t.Worker, &t.Status, &t.StartedAt, &ended, &t.Attempts, &t.LastSignal,
			&t.LastStep, &t.LastStepAt); err != nil {
			return err
		}
		if ended.Valid {
			t.EndedAt = &ended.Int64
		}
		if t.LastSignal == 0 { // bases anteriores: el inicio o el último paso
			t.LastSignal = max(t.StartedAt, t.LastStepAt)
		}
		r := &runs[idx[t.RunID]]
		r.Tests = append(r.Tests, t)
	}
	return rows.Err()
}

// DurationStats are the p50 and p90 of the duration of a test (by key) in its last finished
// executions of the same project and environment before beforeRun, and how many there were.
func (s *Store) DurationStats(key, project, environment string, beforeRun int64, limit int) (p50, p90 int64, n int, err error) {
	rows, err := s.db.Query(`SELECT t.ended_at - t.started_at FROM tests t JOIN runs r ON r.id = t.run_id
		WHERE t.test_key = ? AND t.run_id < ? AND r.project = ? AND r.environment = ?
		  AND t.status IN ('PASS', 'WARNING', 'FAIL') AND t.ended_at IS NOT NULL
		ORDER BY t.run_id DESC LIMIT ?`, key, beforeRun, project, environment, limit)
	if err != nil {
		return 0, 0, 0, err
	}
	defer rows.Close()
	var d []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return 0, 0, 0, err
		}
		if v >= 0 {
			d = append(d, v)
		}
	}
	if err := rows.Err(); err != nil || len(d) == 0 {
		return 0, 0, 0, err
	}
	sort.Slice(d, func(i, j int) bool { return d[i] < d[j] })
	at := func(q float64) int64 { return d[min(len(d)-1, int(q*float64(len(d))))] }
	return at(0.5), at(0.9), len(d), nil
}

// RequestCancel marks a running run to be cancelled: clients with remote control see it between tests
// (GET /runs/{id}/control), skip the rest and close the run. Nothing is killed.
func (s *Store) RequestCancel(runID int64, by, reason string) (bool, error) {
	res, err := s.db.Exec(`UPDATE runs SET cancel_at = ?, cancel_by = ?, cancel_reason = ? WHERE id = ? AND status = 'RUNNING' AND cancel_at = 0`,
		NowMs(), by, reason, runID)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// RunControl is what a client with remote control asks between tests.
type RunControl struct {
	Cancel bool   `json:"cancel"`
	At     int64  `json:"at,omitempty"`
	By     string `json:"by,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// GetRunControl returns the control flags of a run (ErrNotFound if it does not exist).
func (s *Store) GetRunControl(runID int64) (RunControl, error) {
	var c RunControl
	err := s.db.QueryRow(`SELECT cancel_at, cancel_by, cancel_reason FROM runs WHERE id = ?`, runID).Scan(&c.At, &c.By, &c.Reason)
	if err == sql.ErrNoRows {
		return c, ErrNotFound
	}
	c.Cancel = c.At > 0
	return c, err
}

// SetPlanned records how many tests the client collected (it may arrive after the run was created).
func (s *Store) SetPlanned(runID int64, planned int) error {
	res, err := s.db.Exec(`UPDATE runs SET planned = ? WHERE id = ?`, planned, runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
