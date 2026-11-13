package db

import (
	"os"
	"path/filepath"
	"testing"
)

// SQLite's temporary files go next to the database, not to /tmp: in the container /tmp is a 256 MB
// tmpfs and deleting runs with a lot of evidence (or VACUUM) failed with SQLITE_FULL.
func TestTempFilesNextToTheDatabase(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, "tmp", "etilqs_leftover")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "tracereports.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	got, err := s.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "tmp"); got != want {
		t.Fatalf("temp dir = %q, want %q", got, want)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("a leftover temp file from a previous run was not removed: %v", err)
	}
	if err := s.Compact(); err != nil { // VACUUM escribe su copia en esa carpeta
		t.Fatal(err)
	}
}
