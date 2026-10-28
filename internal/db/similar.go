package db

import (
	"database/sql"
	"errors"
	"strings"
)

// Índice de fallos clasificados (#44): la firma de cada test con veredicto, para encontrar los
// incidentes parecidos a uno nuevo (internal/similar). Una fila por test (su último veredicto); la
// fila se va con el test cuando la retención lo borra (ON DELETE CASCADE).
const similarSchema = `
CREATE TABLE IF NOT EXISTS similar_index (
	test_id    INTEGER PRIMARY KEY REFERENCES tests(id) ON DELETE CASCADE,
	run_id     INTEGER NOT NULL,
	project    TEXT    NOT NULL DEFAULT '',
	terms      TEXT    NOT NULL,   -- términos de la firma, separados por espacio
	verdict    TEXT    NOT NULL,
	comment    TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_similar_project ON similar_index(project);
`

// SimilarEntry is a classified failure of the index.
type SimilarEntry struct {
	TestID    int64    `json:"test_id"`
	RunID     int64    `json:"run_id"`
	TestName  string   `json:"test_name"`
	Terms     []string `json:"-"`
	Verdict   string   `json:"verdict"`
	Comment   string   `json:"comment"`
	CreatedAt int64    `json:"created_at"`
}

// IndexVerdict stores (or replaces) the signature of a classified test.
func (s *Store) IndexVerdict(v *Verdict, project string, terms []string) error {
	_, err := s.db.Exec(`INSERT INTO similar_index(test_id, run_id, project, terms, verdict, comment, created_at) VALUES(?,?,?,?,?,?,?)
		ON CONFLICT(test_id) DO UPDATE SET run_id=excluded.run_id, project=excluded.project, terms=excluded.terms,
			verdict=excluded.verdict, comment=excluded.comment, created_at=excluded.created_at`,
		v.TestID, v.RunID, project, strings.Join(terms, " "), v.Verdict, v.Comment, v.CreatedAt)
	return err
}

// SimilarCandidates returns the classified failures of a project, except the test itself.
func (s *Store) SimilarCandidates(project string, exceptTest int64) ([]SimilarEntry, error) {
	rows, err := s.db.Query(`SELECT i.test_id, i.run_id, COALESCE(t.name, ''), i.terms, i.verdict, i.comment, i.created_at
		FROM similar_index i LEFT JOIN tests t ON t.id = i.test_id
		WHERE i.project = ? AND i.test_id != ? ORDER BY i.test_id`, project, exceptTest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SimilarEntry
	for rows.Next() {
		var e SimilarEntry
		var terms string
		if err := rows.Scan(&e.TestID, &e.RunID, &e.TestName, &terms, &e.Verdict, &e.Comment, &e.CreatedAt); err != nil {
			return nil, err
		}
		e.Terms = strings.Fields(terms)
		out = append(out, e)
	}
	return out, rows.Err()
}

// IndexedTerms returns the stored terms of a test (nil if it has no verdict indexed).
func (s *Store) IndexedTerms(testID int64) ([]string, error) {
	var terms string
	err := s.db.QueryRow(`SELECT terms FROM similar_index WHERE test_id = ?`, testID).Scan(&terms)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return strings.Fields(terms), err
}
