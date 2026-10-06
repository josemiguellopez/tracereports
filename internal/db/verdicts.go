package db

// Collaborative triage: someone classifies a failure (product bug, broken test, environment...)
// with a comment. The verdict belongs to the test of that run, and the next runs of the same test
// (same project and identity) show it as the previous verdict, so nobody investigates the same
// failure twice.
const verdictsSchema = `
CREATE TABLE IF NOT EXISTS verdicts (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id     INTEGER NOT NULL,
	test_id    INTEGER NOT NULL,
	project    TEXT    NOT NULL DEFAULT '',
	test_key   TEXT    NOT NULL DEFAULT '',
	verdict    TEXT    NOT NULL,   -- product_bug | test_bug | environment | data | flaky | other
	comment    TEXT    NOT NULL DEFAULT '',
	author     TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_verdicts_test ON verdicts(test_id);
CREATE INDEX IF NOT EXISTS idx_verdicts_key ON verdicts(project, test_key);
`

// Verdicts are the accepted classifications.
var Verdicts = []string{"product_bug", "test_bug", "environment", "data", "flaky", "other"}

// Verdict is a classification of a failure.
type Verdict struct {
	ID        int64  `json:"id"`
	RunID     int64  `json:"run_id"`
	TestID    int64  `json:"test_id"`
	Verdict   string `json:"verdict"`
	Comment   string `json:"comment"`
	Author    string `json:"author"`
	CreatedAt int64  `json:"created_at"`
}

// SaveVerdict records a classification (the latest one of a test is the one shown).
func (s *Store) SaveVerdict(v *Verdict, project, key string) error {
	if v.CreatedAt == 0 {
		v.CreatedAt = NowMs()
	}
	res, err := s.db.Exec(`INSERT INTO verdicts(run_id, test_id, project, test_key, verdict, comment, author, created_at) VALUES(?,?,?,?,?,?,?,?)`,
		v.RunID, v.TestID, project, key, v.Verdict, v.Comment, v.Author, v.CreatedAt)
	if err != nil {
		return err
	}
	v.ID, err = res.LastInsertId()
	return err
}

// VerdictsOfRun returns, for the tests of a run, their latest verdict in this run (by test id) and
// the latest verdict of the same test in an earlier run (by test id too).
func (s *Store) VerdictsOfRun(runID int64) (current, previous map[int64]*Verdict, err error) {
	current, previous = map[int64]*Verdict{}, map[int64]*Verdict{}
	rows, err := s.db.Query(`SELECT v.id, v.run_id, v.test_id, v.verdict, v.comment, v.author, v.created_at FROM verdicts v
		WHERE v.run_id = ? ORDER BY v.created_at, v.id`, runID)
	if err != nil {
		return nil, nil, err
	}
	for rows.Next() {
		var v Verdict
		if err := rows.Scan(&v.ID, &v.RunID, &v.TestID, &v.Verdict, &v.Comment, &v.Author, &v.CreatedAt); err != nil {
			rows.Close()
			return nil, nil, err
		}
		current[v.TestID] = &v // ordenados: queda el último
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT t.id, v.id, v.run_id, v.test_id, v.verdict, v.comment, v.author, v.created_at
		FROM tests t JOIN runs r ON r.id = t.run_id
		JOIN verdicts v ON v.project = r.project AND v.test_key = t.test_key AND v.run_id < t.run_id
		WHERE t.run_id = ? AND t.test_key <> '' ORDER BY v.created_at, v.id`, runID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var testID int64
		var v Verdict
		if err := rows.Scan(&testID, &v.ID, &v.RunID, &v.TestID, &v.Verdict, &v.Comment, &v.Author, &v.CreatedAt); err != nil {
			return nil, nil, err
		}
		previous[testID] = &v
	}
	return current, previous, rows.Err()
}
