package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// A database created before evidence_file existed must be migrated on Open.
func TestOpenMigratesOldNetworkTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
	old, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE network (id INTEGER PRIMARY KEY AUTOINCREMENT, test_id INTEGER NOT NULL, seq INTEGER NOT NULL,
		method TEXT NOT NULL DEFAULT '', url TEXT NOT NULL DEFAULT '', status INTEGER NOT NULL DEFAULT 0, status_text TEXT NOT NULL DEFAULT '',
		mime_type TEXT NOT NULL DEFAULT '', resource_type TEXT NOT NULL DEFAULT '', failed INTEGER NOT NULL DEFAULT 0,
		error_text TEXT NOT NULL DEFAULT '', started_at INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER,
		request_headers TEXT NOT NULL DEFAULT '{}', post_data TEXT NOT NULL DEFAULT '', post_data_via_cdp INTEGER NOT NULL DEFAULT 0,
		response_headers TEXT NOT NULL DEFAULT '{}', response_body TEXT NOT NULL DEFAULT '', body_size INTEGER NOT NULL DEFAULT 0,
		body_truncated INTEGER NOT NULL DEFAULT 0)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open old db: %v", err)
	}
	defer s.Close()
	runID, _ := s.CreateRun("r", "")
	testID, _ := s.CreateTest(runID, "t", "", "")
	if err := s.AddNetwork(testID, []NetConn{{Method: "GET", URL: "u", EvidenceFile: "x.json"}}); err != nil {
		t.Fatalf("insert after migration: %v", err)
	}
	conns, _ := s.ListNetwork(testID)
	if len(conns) != 1 || conns[0].EvidenceFile != "x.json" {
		t.Fatalf("evidence_file not persisted: %+v", conns)
	}
}
