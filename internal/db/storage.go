package db

// Almacenamiento: cuánto ocupa la base, qué archivos (capturas, adjuntos) son de qué proyecto y qué
// liberaría la retención. Las capturas viven fuera de la base: aquí solo están sus URLs; el tamaño
// de cada archivo lo mide quien tiene la carpeta (internal/api/storage.go).

// StorageCounts is what the database holds.
type StorageCounts struct {
	DBBytes   int64 `json:"db_bytes"` // archivo de la base (páginas en uso y libres)
	Runs      int   `json:"runs"`
	Tests     int   `json:"tests"`
	OldestRun int64 `json:"oldest_run"` // started_at de la más antigua (ms); 0 sin ejecuciones
	NewestRun int64 `json:"newest_run"`
}

// StorageCounts returns the size of the database and how many runs and tests it holds.
func (s *Store) StorageCounts() (StorageCounts, error) {
	var c StorageCounts
	var pages, size int64
	if err := s.db.QueryRow(`PRAGMA page_count`).Scan(&pages); err != nil {
		return c, err
	}
	if err := s.db.QueryRow(`PRAGMA page_size`).Scan(&size); err != nil {
		return c, err
	}
	c.DBBytes = pages * size
	err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(started_at), 0), COALESCE(MAX(started_at), 0) FROM runs`).
		Scan(&c.Runs, &c.OldestRun, &c.NewestRun)
	if err != nil {
		return c, err
	}
	err = s.db.QueryRow(`SELECT COUNT(*) FROM tests`).Scan(&c.Tests)
	return c, err
}

// ProjectFiles is, for one project, how many runs it has and the URLs of its files.
type ProjectFiles struct {
	Project string
	Runs    int
	Tests   int
	URLs    []string
}

// FilesByProject lists the files (screenshots and attachments) of every project, with its runs and
// tests, to measure what each one takes.
func (s *Store) FilesByProject() ([]ProjectFiles, error) {
	rows, err := s.db.Query(`SELECT r.project, COUNT(DISTINCT r.id), COUNT(t.id) FROM runs r
		LEFT JOIN tests t ON t.run_id = r.id GROUP BY r.project`)
	if err != nil {
		return nil, err
	}
	byProject := map[string]*ProjectFiles{}
	var out []*ProjectFiles
	for rows.Next() {
		p := &ProjectFiles{}
		if err := rows.Scan(&p.Project, &p.Runs, &p.Tests); err != nil {
			rows.Close()
			return nil, err
		}
		byProject[p.Project] = p
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	files, err := s.db.Query(`SELECT r.project, l.screenshot FROM logs l JOIN tests t ON t.id = l.test_id
		JOIN runs r ON r.id = t.run_id WHERE l.screenshot != ''
		UNION ALL
		SELECT r.project, a.url FROM artifacts a JOIN tests t ON t.id = a.test_id JOIN runs r ON r.id = t.run_id`)
	if err != nil {
		return nil, err
	}
	defer files.Close()
	for files.Next() {
		var project, url string
		if err := files.Scan(&project, &url); err != nil {
			return nil, err
		}
		if p := byProject[project]; p != nil {
			p.URLs = append(p.URLs, url)
		}
	}
	res := make([]ProjectFiles, 0, len(out))
	for _, p := range out {
		res = append(res, *p)
	}
	return res, files.Err()
}

// RetentionPreview is what deleting the runs started before a cutoff would remove (the same runs
// PurgeRunsBefore deletes: finished ones).
type RetentionPreview struct {
	Runs  int      `json:"runs"`
	Tests int      `json:"tests"`
	URLs  []string `json:"-"`
}

// PreviewPurgeBefore counts the runs, tests and files that PurgeRunsBefore(cutoff) would delete,
// without deleting anything.
func (s *Store) PreviewPurgeBefore(cutoff int64) (RetentionPreview, error) {
	var p RetentionPreview
	err := s.db.QueryRow(`SELECT COUNT(DISTINCT r.id), COUNT(t.id) FROM runs r LEFT JOIN tests t ON t.run_id = r.id
		WHERE r.started_at < ? AND r.status != 'RUNNING'`, cutoff).Scan(&p.Runs, &p.Tests)
	if err != nil || p.Runs == 0 {
		return p, err
	}
	rows, err := s.db.Query(`SELECT l.screenshot FROM logs l JOIN tests t ON t.id = l.test_id JOIN runs r ON r.id = t.run_id
		WHERE r.started_at < ? AND r.status != 'RUNNING' AND l.screenshot != ''
		UNION ALL
		SELECT a.url FROM artifacts a JOIN tests t ON t.id = a.test_id JOIN runs r ON r.id = t.run_id
		WHERE r.started_at < ? AND r.status != 'RUNNING'`, cutoff, cutoff)
	if err != nil {
		return p, err
	}
	defer rows.Close()
	for rows.Next() {
		var url string
		if err := rows.Scan(&url); err != nil {
			return p, err
		}
		p.URLs = append(p.URLs, url)
	}
	return p, rows.Err()
}
