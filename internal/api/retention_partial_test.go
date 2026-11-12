package api

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// A cleanup that fails halfway still records what it deleted: "Última limpieza" said "0 ejecución(es)"
// after a retention that deleted 875 runs and then failed on a later batch (SQLITE_FULL).
func TestRetentionCutHalfwayRecordsWhatItDeleted(t *testing.T) {
	t.Setenv("TRACEREPORTS_RETENTION_DAYS", "")
	srv, _ := newTestServer(t)
	dbPath := filepath.Join(srv.ScreenshotsDir, "t.db")
	srv.ScreenshotsDir = t.TempDir()
	for i := 0; i < 20; i++ { // las dos primeras tandas (de 10) se borran enteras
		storageRun(t, srv, "p", 40, 10)
	}
	storageRun(t, srv, "boom", 40, 10) // la tercera tanda falla
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(`CREATE TRIGGER fail_boom BEFORE DELETE ON runs WHEN OLD.name = 'suite boom'
		BEGIN SELECT RAISE(ABORT, 'database or disk is full'); END`); err != nil {
		t.Fatal(err)
	}

	run, err := srv.PurgeOld(30, "auto", "")
	if err == nil || run.Runs != 20 {
		t.Fatalf("purge: runs=%d err=%v, want 20 and an error", run.Runs, err)
	}
	last := srv.lastRetention()
	if last == nil || last.Runs != 20 || last.Files != 20 || last.Error == "" {
		t.Fatalf("last cleanup = %+v, want the 20 runs it deleted and the error", last)
	}
}
