package db

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/josemiguellopez/tracereports/internal/atrest"
)

const domSchema = `
CREATE TABLE IF NOT EXISTS test_dom (
	test_id    INTEGER PRIMARY KEY REFERENCES tests(id) ON DELETE CASCADE,
	snapshot   TEXT    NOT NULL,   -- JSON de locator.Snapshot (elementos + posiciones)
	created_at INTEGER NOT NULL
);
`

// SaveDOM stores the page snapshot taken when the test failed (one per test, last wins).
func (s *Store) SaveDOM(testID int64, snapshotJSON string) error {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE id=?`, testID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	// el DOM muestra lo que había en la pantalla: se sella como las capturas
	if s.vault.Encrypting() {
		sealed, err := s.vault.Seal(domName(testID), snapshotJSON)
		if err != nil {
			return err
		}
		snapshotJSON = sealed
	}
	_, err := s.db.Exec(`INSERT INTO test_dom(test_id, snapshot, created_at) VALUES(?,?,?)
		ON CONFLICT(test_id) DO UPDATE SET snapshot=excluded.snapshot, created_at=excluded.created_at`,
		testID, snapshotJSON, NowMs())
	return err
}

// GetDOM returns the stored snapshot JSON ("" if none).
func (s *Store) GetDOM(testID int64) (string, error) {
	var snap string
	err := s.db.QueryRow(`SELECT snapshot FROM test_dom WHERE test_id=?`, testID).Scan(&snap)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return s.vault.Open(domName(testID), snap)
}

// domName binds a sealed snapshot to its test: it cannot be moved to another row.
func domName(testID int64) string { return fmt.Sprintf("test_dom:%d", testID) }

// TestRunID returns the run of a test (used to route live events).
func (s *Store) TestRunID(testID int64) (int64, error) {
	var runID int64
	err := s.db.QueryRow(`SELECT run_id FROM tests WHERE id=?`, testID).Scan(&runID)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	return runID, err
}

// ─── Network drift: latencia del test vs. su historial ─────────────────────

// NetDrift compares the p95 latency of a test's backend calls with its own history.
type NetDrift struct {
	CurrentP95  int64 `json:"current_p95"`
	BaselineP95 int64 `json:"baseline_p95"` // mediana del p95 en ejecuciones anteriores
	DeltaMs     int64 `json:"delta_ms"`
	Runs        int   `json:"runs"` // ejecuciones anteriores usadas como referencia
}

// EndpointDrift is the latency change of one endpoint inside a test.
type EndpointDrift struct {
	Method      string `json:"method"`
	Host        string `json:"host,omitempty"` // dos servicios con el mismo path son endpoints distintos
	Path        string `json:"path"`
	Count       int    `json:"count"`
	CurrentP95  int64  `json:"current_p95"`
	BaselineP95 int64  `json:"baseline_p95"` // 0 = endpoint nuevo
	DeltaMs     int64  `json:"delta_ms"`
}

const driftWindow = 10

// driftSignificant: al menos 300 ms y 30% peor que la referencia (evita ruido de red).
func driftSignificant(cur, base int64) bool {
	return base > 0 && cur-base >= 300 && cur*10 >= base*13
}

func p95(ds []int64) int64 {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]int64(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[(len(sorted)*95+99)/100-1]
}

func median(ds []int64) int64 {
	if len(ds) == 0 {
		return 0
	}
	sorted := append([]int64(nil), ds...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	return sorted[len(sorted)/2]
}

type netSample struct {
	testID, runID int64
	key, method   string
	url           string
	dur           int64
}

// networkSamples returns the API/document call durations of the tests matching where (aliases
// t = tests, r = runs), newest first.
func (s *Store) networkSamples(where string, args ...any) ([]netSample, error) {
	rows, err := s.db.Query(`
		SELECT t.id, t.run_id, t.test_key, n.method, n.url, n.duration_ms
		FROM network n JOIN tests t ON t.id = n.test_id JOIN runs r ON r.id = t.run_id
		WHERE n.duration_ms IS NOT NULL AND n.failed = 0 AND n.resource_type IN ('xhr','fetch','document','')
		  AND `+where+` ORDER BY t.run_id DESC, t.id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []netSample
	for rows.Next() {
		var x netSample
		if err := rows.Scan(&x.testID, &x.runID, &x.key, &x.method, &x.url, &x.dur); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// networkDrift flags the tests of a run whose backend p95 got significantly slower than
// the median p95 of their previous executions.
func (s *Store) networkDrift(runID int64) (map[int64]*NetDrift, error) {
	args := append([]any{runID}, ctxArgs(runID)...)
	samples, err := s.networkSamples(`t.run_id <= ? AND `+sameContext+` AND t.test_key IN (SELECT test_key FROM tests WHERE run_id = ?)`,
		append(args, runID)...)
	if err != nil {
		return nil, err
	}
	// identidad -> test (en orden, más nuevo primero) -> duraciones
	type testDur struct {
		id, run int64
		durs    []int64
	}
	byName := map[string][]*testDur{}
	for _, x := range samples {
		list := byName[x.key]
		if n := len(list); n == 0 || list[n-1].id != x.testID {
			list = append(list, &testDur{id: x.testID, run: x.runID})
			byName[x.key] = list
		}
		cur := list[len(list)-1]
		cur.durs = append(cur.durs, x.dur)
	}
	out := map[int64]*NetDrift{}
	for _, list := range byName {
		if len(list) < 2 || list[0].run != runID {
			continue
		}
		var prev []int64
		for _, td := range list[1:] {
			if len(prev) == driftWindow {
				break
			}
			prev = append(prev, p95(td.durs))
		}
		cur, base := p95(list[0].durs), median(prev)
		if driftSignificant(cur, base) {
			out[list[0].id] = &NetDrift{CurrentP95: cur, BaselineP95: base, DeltaMs: cur - base, Runs: len(prev)}
		}
	}
	return out, nil
}

// TestDrift details, endpoint by endpoint, how a test's latency compares with its history.
func (s *Store) TestDrift(testID int64) ([]EndpointDrift, error) {
	t, err := s.GetTest(testID)
	if err != nil {
		return nil, err
	}
	samples, err := s.networkSamples(`t.test_key = ? AND t.run_id <= ? AND `+sameContext, append([]any{t.Key, t.RunID}, ctxArgs(t.RunID)...)...)
	if err != nil {
		return nil, err
	}
	type acc struct {
		cur  []int64
		prev map[int64][]int64 // test anterior -> duraciones
	}
	// identidad del endpoint: método, host y path normalizado (los ids variables se agrupan)
	type endpoint struct{ method, host, path string }
	byEP := map[endpoint]*acc{}
	var order []endpoint
	prevTests := map[int64]bool{}
	for _, x := range samples {
		host, path := NormalizeEndpoint(x.url)
		key := endpoint{x.method, host, path}
		a := byEP[key]
		if a == nil {
			a = &acc{prev: map[int64][]int64{}}
			byEP[key] = a
			order = append(order, key)
		}
		if x.testID == testID {
			a.cur = append(a.cur, x.dur)
		} else if len(prevTests) < driftWindow || prevTests[x.testID] {
			prevTests[x.testID] = true
			a.prev[x.testID] = append(a.prev[x.testID], x.dur)
		}
	}
	out := []EndpointDrift{}
	for _, key := range order {
		a := byEP[key]
		if len(a.cur) == 0 {
			continue
		}
		var prevP95 []int64
		for _, d := range a.prev {
			prevP95 = append(prevP95, p95(d))
		}
		ed := EndpointDrift{Method: key.method, Host: key.host, Path: key.path, Count: len(a.cur), CurrentP95: p95(a.cur), BaselineP95: median(prevP95)}
		if ed.BaselineP95 > 0 {
			ed.DeltaMs = ed.CurrentP95 - ed.BaselineP95
		}
		out = append(out, ed)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DeltaMs > out[j].DeltaMs })
	return out, nil
}

// DOMKeys counts the DOM snapshots by how they are stored: "" = in clear, otherwise the id of the
// key that sealed them (tracereports evidence status; never their content).
func (s *Store) DOMKeys() (map[string]int, error) {
	rows, err := s.db.Query(`SELECT snapshot FROM test_dom`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var snap string
		if err := rows.Scan(&snap); err != nil {
			return nil, err
		}
		out[atrest.SealedKeyID(snap)]++
	}
	return out, rows.Err()
}

// ResealDOM seals with the current key of v the snapshots stored in clear or sealed with another
// key it can open (encryption turned on later, or a key rotation), one row at a time. It returns
// how many changed and how many it cannot open (sealed with a key that is not configured).
func (s *Store) ResealDOM(v *atrest.Vault) (changed, unreadable int, err error) {
	var ids []int64
	rows, err := s.db.Query(`SELECT test_id FROM test_dom ORDER BY test_id`)
	if err != nil {
		return 0, 0, err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		var snap string
		if err := s.db.QueryRow(`SELECT snapshot FROM test_dom WHERE test_id=?`, id).Scan(&snap); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				continue // la borró la retención mientras tanto
			}
			return changed, unreadable, err
		}
		if atrest.Sealed(snap) && atrest.SealedKeyID(snap) == v.KeyID() {
			continue
		}
		plain, err := v.Open(domName(id), snap)
		if err != nil {
			unreadable++
			continue
		}
		sealed, err := v.Seal(domName(id), plain)
		if err != nil {
			return changed, unreadable, err
		}
		if _, err := s.db.Exec(`UPDATE test_dom SET snapshot=? WHERE test_id=?`, sealed, id); err != nil {
			return changed, unreadable, err
		}
		changed++
	}
	return changed, unreadable, nil
}
