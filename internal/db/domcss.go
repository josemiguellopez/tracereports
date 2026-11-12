package db

import (
	"database/sql"
	"errors"
)

// Hojas de estilo del modo HTML del Inspector: cada una se guarda una vez por su SHA-256 (las de una
// aplicación son las mismas en todas sus páginas y cambian solo cuando se despliega), y cada test anota
// las que usan sus páginas. Una hoja que ningún test usa se borra con la retención. El CSS no es dato
// de la página: no se sella como los snapshots.
//
// dom_fidelity: cuánto se parece la página reconstruida a la captura del fallo, medido por el
// Inspector, por modo (structure o html) para comparar ambos en la misma suite.
const domCSSSchema = `
CREATE TABLE IF NOT EXISTS dom_css (
	hash       TEXT    PRIMARY KEY,
	css        TEXT    NOT NULL,
	bytes      INTEGER NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS dom_css_refs (
	test_id INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	hash    TEXT    NOT NULL,
	PRIMARY KEY (test_id, hash)
);
CREATE INDEX IF NOT EXISTS idx_dom_css_refs_hash ON dom_css_refs(hash);
CREATE TABLE IF NOT EXISTS dom_fidelity (
	test_id  INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	mode     TEXT    NOT NULL,
	fidelity REAL    NOT NULL,
	PRIMARY KEY (test_id, mode)
);
`

// SaveDOMCSS stores the new sheets of a snapshot (hash → cleaned CSS; one already stored stays) and
// notes the ones the test's pages use.
func (s *Store) SaveDOMCSS(testID int64, sheets map[string]string, used []string) error {
	now := NowMs()
	for hash, css := range sheets {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO dom_css(hash, css, bytes, created_at) VALUES(?,?,?,?)`, hash, css, len(css), now); err != nil {
			return err
		}
	}
	for _, hash := range used {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO dom_css_refs(test_id, hash) VALUES(?,?)`, testID, hash); err != nil {
			return err
		}
	}
	return nil
}

// GetDOMCSS returns a stored sheet ("" when it is not there).
func (s *Store) GetDOMCSS(hash string) (string, error) {
	var css string
	err := s.db.QueryRow(`SELECT css FROM dom_css WHERE hash=?`, hash).Scan(&css)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return css, err
}

// pruneDOMCSS deletes the sheets no test uses any more (after deleting runs).
func (s *Store) pruneDOMCSS() error {
	_, err := s.db.Exec(`DELETE FROM dom_css WHERE NOT EXISTS (SELECT 1 FROM dom_css_refs r WHERE r.hash = dom_css.hash)`)
	return err
}

// SetDOMFidelity stores how much the redrawn failure page matches its screenshot (0..1), as the
// Inspector measured it, for the snapshot mode it was drawn from (ErrNotFound without a snapshot).
func (s *Store) SetDOMFidelity(testID int64, fidelity float64, mode string) error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM test_dom WHERE test_id=?`, testID).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	_, err := s.db.Exec(`INSERT INTO dom_fidelity(test_id, mode, fidelity) VALUES(?,?,?)
		ON CONFLICT(test_id, mode) DO UPDATE SET fidelity=excluded.fidelity`, testID, mode, fidelity)
	return err
}

// DOMFidelity is the fidelity of a run's failure pages for one snapshot mode.
type DOMFidelity struct {
	Mode  string  `json:"mode"`
	Tests int     `json:"tests"`
	Avg   float64 `json:"avg"`
	Min   float64 `json:"min"`
}

// RunDOMFidelity: the measured fidelity of a run's failure pages, by snapshot mode (to compare
// structure with html on the same suite).
func (s *Store) RunDOMFidelity(runID int64) ([]DOMFidelity, error) {
	rows, err := s.db.Query(`SELECT f.mode, COUNT(*), AVG(f.fidelity), MIN(f.fidelity) FROM dom_fidelity f
		JOIN tests t ON t.id = f.test_id WHERE t.run_id=? GROUP BY f.mode ORDER BY f.mode`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DOMFidelity{}
	for rows.Next() {
		var f DOMFidelity
		if err := rows.Scan(&f.Mode, &f.Tests, &f.Avg, &f.Min); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
