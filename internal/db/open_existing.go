package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// OpenExisting opens a database that must already exist, without applying the schema nor the
// migrations (unlike Open) and without creating it. readOnly opens it in SQLite read-only mode:
// nothing can be written, not even by mistake. Meant for tools that inspect or touch a few rows
// of a database they must not upgrade (tracereports secrets).
func OpenExisting(path string, readOnly bool) (*Store, error) {
	if _, err := os.Stat(path); err != nil {
		if old := LegacyDatabase(path); old != "" {
			return nil, fmt.Errorf("no database at %s (an older version kept it at %s: see LegacyDatabase): %w", path, old, err)
		}
		return nil, fmt.Errorf("no database at %s: %w", path, err)
	}
	uri, err := pathURI(path)
	if err != nil {
		return nil, err
	}
	dsn := uri + "?_pragma=busy_timeout(5000)"
	if readOnly {
		dsn = uri + "?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"
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

// SQLite reads the database path as a file: URI: '#' starts a fragment, '?' the parameters and
// %XX is decoded. The path is made absolute and percent-encoded, so the database opened is exactly
// the file asked for, whatever characters its folders have.

// pathURI returns the file: URI of path (absolute).
func pathURI(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return fileURI(filepath.ToSlash(abs)), nil
}

// fileURI builds the URI of an absolute slash path: /home/x -> file:///home/x, C:/x ->
// file:///C:/x (SQLite drops the slash before a Windows drive), //server/share -> file:////server/share.
func fileURI(p string) string {
	var b strings.Builder
	b.WriteString("file://")
	if !strings.HasPrefix(p, "/") {
		b.WriteByte('/')
	}
	const hex = "0123456789ABCDEF"
	for i := 0; i < len(p); i++ {
		c := p[i]
		if 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z' || '0' <= c && c <= '9' || strings.IndexByte("-._~/:", c) >= 0 {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}
	return b.String()
}

// legacyLocation is the file that versions before this fix opened for path ("" when it is the
// same): they wrote "file:" + path without escaping, so SQLite cut it at the first '#' or '?'
// and decoded the %XX sequences.
func legacyLocation(path string) string {
	p := filepath.ToSlash(path)
	old := p
	if i := strings.IndexAny(old, "#?"); i >= 0 {
		old = old[:i]
	}
	var b strings.Builder
	for i := 0; i < len(old); i++ {
		if old[i] == '%' && i+2 < len(old) && isHex(old[i+1]) && isHex(old[i+2]) {
			b.WriteByte(unhex(old[i+1])<<4 | unhex(old[i+2]))
			i += 2
			continue
		}
		b.WriteByte(old[i])
	}
	if b.String() == p {
		return ""
	}
	return filepath.FromSlash(b.String())
}

func isHex(c byte) bool { return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F' }

func unhex(c byte) byte {
	switch {
	case c <= '9':
		return c - '0'
	case c <= 'F':
		return c - 'A' + 10
	}
	return c - 'a' + 10
}

// LegacyDatabase returns where an older version kept the database of path, when that path is
// affected (it has '#', '?' or %XX), the database is not at path yet and an SQLite file is
// there: its data would otherwise look gone. "" otherwise. Nothing is moved: the operator
// stops the server and moves that file (with its -wal and -shm) to path.
func LegacyDatabase(path string) string {
	old := legacyLocation(path)
	if old == "" {
		return ""
	}
	if _, err := os.Stat(path); err == nil {
		return ""
	}
	f, err := os.Open(old)
	if err != nil {
		return ""
	}
	defer f.Close()
	head := make([]byte, 16)
	if n, _ := f.Read(head); n < 16 || string(head) != "SQLite format 3\x00" {
		return ""
	}
	return old
}

// HasTable reports whether the database has a table (old databases may lack newer ones).
func (s *Store) HasTable(name string) (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	return n > 0, err
}
