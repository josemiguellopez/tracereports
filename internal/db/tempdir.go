package db

import (
	"database/sql"
	"fmt"
	"os"
	"strings"
)

// useTempDir makes SQLite write its temporary files (the statement journal of a large DELETE, the
// sorts of a big query, the copy VACUUM builds) in dir, next to the database, instead of /tmp.
//
// In the container /tmp is a small tmpfs (256 MB, docker-compose.yml) and the filesystem is read
// only: deleting runs with a lot of evidence, or compacting a database of several GB, needed more
// temporary space than that and failed with "database or disk is full" (SQLITE_FULL) while the data
// disk had hundreds of GB free. Next to the database the temporary space is the data disk's: what
// the compaction space check measures.
//
// The setting is global to the process (sqlite3_temp_directory), set once before any query.
// SQLITE_TMPDIR is not enough: the pure-Go driver reads the environment once, when it starts.
func useTempDir(db *sql.DB, dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("sqlite temp dir: %w", err)
	}
	// los temporales de una corrida anterior que terminó mal (en Linux se borran solos al abrirse)
	if entries, err := os.ReadDir(dir); err == nil {
		for _, e := range entries {
			if strings.HasPrefix(e.Name(), "etilqs_") {
				_ = os.Remove(dir + string(os.PathSeparator) + e.Name())
			}
		}
	}
	if _, err := db.Exec(`PRAGMA temp_store_directory = '` + strings.ReplaceAll(dir, "'", "''") + `'`); err != nil {
		return fmt.Errorf("sqlite temp dir: %w", err)
	}
	return nil
}

// TempDir is the directory SQLite writes its temporary files to ("" = the system's).
func (s *Store) TempDir() (string, error) {
	var dir string
	err := s.db.QueryRow(`PRAGMA temp_store_directory`).Scan(&dir)
	return dir, err
}
