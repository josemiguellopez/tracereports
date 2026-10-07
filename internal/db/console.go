package db

// Browser console of a test: console.error / console.warn messages and uncaught page errors
// (pageerror). A failure that looks like "the button did not appear" is often a JavaScript error
// the page logged a second earlier.
const consoleSchema = `
CREATE TABLE IF NOT EXISTS console (
	id        INTEGER PRIMARY KEY AUTOINCREMENT,
	test_id   INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	seq       INTEGER NOT NULL,
	level     TEXT    NOT NULL,            -- error | warning | pageerror | info | log | debug
	text      TEXT    NOT NULL DEFAULT '',
	location  TEXT    NOT NULL DEFAULT '', -- url:línea:columna
	timestamp INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_console_test ON console(test_id);
`

// ConsoleLevels are the accepted levels.
var ConsoleLevels = map[string]bool{"error": true, "warning": true, "pageerror": true, "info": true, "log": true, "debug": true}

// MaxConsolePerTest caps the console entries kept per test (a page can log in a loop).
const MaxConsolePerTest = 500

// ConsoleEntry is one console message or page error.
type ConsoleEntry struct {
	Seq       int    `json:"seq"`
	Level     string `json:"level"`
	Text      string `json:"text"`
	Location  string `json:"location,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// AddConsole appends entries to a test, up to MaxConsolePerTest in total. Returns how many were
// stored.
func (s *Store) AddConsole(testID int64, entries []ConsoleEntry) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var have, last int
	if err := tx.QueryRow(`SELECT COUNT(*), COALESCE(MAX(seq),0) FROM console WHERE test_id=?`, testID).Scan(&have, &last); err != nil {
		return 0, err
	}
	stored := 0
	for _, e := range entries {
		if have+stored >= MaxConsolePerTest {
			break
		}
		if e.Timestamp == 0 {
			e.Timestamp = NowMs()
		}
		last++
		if _, err := tx.Exec(`INSERT INTO console(test_id, seq, level, text, location, timestamp) VALUES(?,?,?,?,?,?)`,
			testID, last, e.Level, e.Text, e.Location, e.Timestamp); err != nil {
			return 0, err
		}
		stored++
	}
	return stored, tx.Commit()
}

// ConsoleOf lists the console of a test in order.
func (s *Store) ConsoleOf(testID int64) ([]ConsoleEntry, error) {
	rows, err := s.db.Query(`SELECT seq, level, text, location, timestamp FROM console WHERE test_id=? ORDER BY seq`, testID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConsoleEntry
	for rows.Next() {
		var e ConsoleEntry
		if err := rows.Scan(&e.Seq, &e.Level, &e.Text, &e.Location, &e.Timestamp); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
