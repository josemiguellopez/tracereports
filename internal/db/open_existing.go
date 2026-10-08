package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
)

// OpenExisting opens a database that must already exist, without applying the schema nor the
// migrations (unlike Open) and without creating it. readOnly opens it in SQLite read-only mode:
// nothing can be written, not even by mistake. Meant for tools that inspect or touch a few rows
// of a database they must not upgrade (tracereports secrets).
func OpenExisting(path string, readOnly bool) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no database at %s: %w", path, err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)", filepath.ToSlash(path))
	if readOnly {
		dsn = fmt.Sprintf("file:%s?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)", filepath.ToSlash(path))
	}
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	sqldb.SetMaxOpenConns(1)
	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("open %s: %w", path, err)
	}
	return &Store{db: dbHandle{sqldb}}, nil
}

// HasTable reports whether the database has a table (old databases may lack newer ones).
func (s *Store) HasTable(name string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}
