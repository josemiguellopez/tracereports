package db

// Artifacts of a test: the Playwright trace (trace.zip) and the video of the execution. The files
// live next to the screenshots (same protected route, same retention); this table says which
// belong to which test.
const artifactsSchema = `
CREATE TABLE IF NOT EXISTS artifacts (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	test_id    INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	kind       TEXT    NOT NULL,   -- trace | video
	name       TEXT    NOT NULL DEFAULT '',
	url        TEXT    NOT NULL,   -- /screenshots/<archivo>
	size       INTEGER NOT NULL DEFAULT 0,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifacts_test ON artifacts(test_id);
`

// Artifact is a trace or video of a test.
type Artifact struct {
	ID        int64  `json:"id"`
	TestID    int64  `json:"test_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	URL       string `json:"url"`
	Size      int64  `json:"size"`
	CreatedAt int64  `json:"created_at"`
}

// AddArtifact records an artifact already written to disk.
func (s *Store) AddArtifact(a *Artifact) error {
	if a.CreatedAt == 0 {
		a.CreatedAt = NowMs()
	}
	res, err := s.db.Exec(`INSERT INTO artifacts(test_id, kind, name, url, size, created_at) VALUES(?,?,?,?,?,?)`,
		a.TestID, a.Kind, a.Name, a.URL, a.Size, a.CreatedAt)
	if err != nil {
		return err
	}
	a.ID, err = res.LastInsertId()
	return err
}

// ArtifactsOf lists the artifacts of a test in upload order.
func (s *Store) ArtifactsOf(testID int64) ([]Artifact, error) {
	rows, err := s.db.Query(`SELECT id, test_id, kind, name, url, size, created_at FROM artifacts WHERE test_id=? ORDER BY id`, testID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Artifact
	for rows.Next() {
		var a Artifact
		if err := rows.Scan(&a.ID, &a.TestID, &a.Kind, &a.Name, &a.URL, &a.Size, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
