package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Una instalación que usó una carpeta con # tenía la base en otro lugar: el arranque no crea una
// vacía (los datos parecerían perdidos), explica dónde está y cómo moverla.
func TestStartStopsWhenTheDatabaseIsAtTheOldLocation(t *testing.T) {
	root := t.TempDir()
	dataDir := filepath.Join(root, "tr#prod")
	os.MkdirAll(dataDir, 0o755)
	want := filepath.Join(dataDir, "tracereports.db")
	old := filepath.Join(root, "tr") // donde la abría la versión anterior: cortada en el #
	s, err := db.Open(old)
	if err != nil {
		t.Fatal(err)
	}
	s.CreateRun("history", "qa")
	s.Close()

	err = checkLegacyDB(want)
	if err == nil || !strings.Contains(err.Error(), old) || !strings.Contains(err.Error(), want) {
		t.Fatalf("explains both paths: %v", err)
	}
	if _, err := os.Stat(want); err == nil {
		t.Fatal("no new database is created")
	}
	t.Setenv("TRACEREPORTS_LEGACY_DB", "ignore")
	if err := checkLegacyDB(want); err != nil {
		t.Fatalf("explicit override: %v", err)
	}
	t.Setenv("TRACEREPORTS_LEGACY_DB", "")
	// movida a su lugar: arranca y conserva los datos
	for _, sfx := range []string{"", "-wal", "-shm"} {
		os.Rename(old+sfx, want+sfx)
	}
	if err := checkLegacyDB(want); err != nil {
		t.Fatal(err)
	}
	s, err = db.Open(want)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if runs, _ := s.ListRuns(10); len(runs) != 1 {
		t.Fatalf("data kept after the move: %d runs", len(runs))
	}
}

// Una ruta sin caracteres afectados no busca nada.
func TestPlainDataDirHasNoLegacyLocation(t *testing.T) {
	if err := checkLegacyDB(filepath.Join(t.TempDir(), "data", "tracereports.db")); err != nil {
		t.Fatal(err)
	}
}
