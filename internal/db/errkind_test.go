package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"syscall"
	"testing"
)

func TestErrKindNamesWhatAPersonCanFix(t *testing.T) {
	if k := ErrKind(fmt.Errorf("save screenshot: %w", syscall.ENOSPC)); k != ErrKindDiskFull {
		t.Fatalf("no space left: %q", k)
	}
	s := openTestStore(t)
	if _, err := s.ro.Exec(`DELETE FROM runs`); ErrKind(err) != ErrKindReadOnly {
		t.Fatalf("write on a read-only connection: %v → %q", err, ErrKind(err))
	}
	// otra conexión con la escritura tomada: sin esperar, la base está ocupada
	path := filepath.Join(t.TempDir(), "busy.db")
	a, _ := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=journal_mode(WAL)")
	b, _ := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?_pragma=busy_timeout(0)")
	defer a.Close()
	defer b.Close()
	a.SetMaxOpenConns(1)
	if _, err := a.Exec(`CREATE TABLE x(v)`); err != nil {
		t.Fatal(err)
	}
	tx, err := a.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO x VALUES (1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Exec(`INSERT INTO x VALUES (2)`); ErrKind(err) != ErrKindBusy {
		t.Fatalf("busy: %v → %q", err, ErrKind(err))
	}
	if ErrKind(fmt.Errorf("something else")) != "" || ErrKind(nil) != "" {
		t.Fatal("unknown errors have no kind")
	}
}
