package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// v1Schema is the schema of the first release (commit 460f910), before the stable identity,
// the run context, expected responses, idempotency and the run-triage counters.
const v1Schema = `
CREATE TABLE runs (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'RUNNING', started_at INTEGER NOT NULL, ended_at INTEGER, total INTEGER NOT NULL DEFAULT 0,
	passed INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0, skipped INTEGER NOT NULL DEFAULT 0, warning INTEGER NOT NULL DEFAULT 0);
CREATE TABLE tests (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	name TEXT NOT NULL, category TEXT NOT NULL DEFAULT '', description TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'RUNNING',
	started_at INTEGER NOT NULL, ended_at INTEGER, error_message TEXT NOT NULL DEFAULT '', error_trace TEXT NOT NULL DEFAULT '');
CREATE INDEX idx_tests_run ON tests(run_id);
CREATE TABLE logs (id INTEGER PRIMARY KEY AUTOINCREMENT, test_id INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	status TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', timestamp INTEGER NOT NULL, screenshot TEXT NOT NULL DEFAULT '');
CREATE TABLE ai_triage (test_id INTEGER PRIMARY KEY REFERENCES tests(id) ON DELETE CASCADE, state TEXT NOT NULL,
	category TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', suggestion TEXT NOT NULL DEFAULT '',
	error TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);
CREATE TABLE network (id INTEGER PRIMARY KEY AUTOINCREMENT, test_id INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	seq INTEGER NOT NULL, method TEXT NOT NULL DEFAULT '', url TEXT NOT NULL DEFAULT '', status INTEGER NOT NULL DEFAULT 0,
	status_text TEXT NOT NULL DEFAULT '', mime_type TEXT NOT NULL DEFAULT '', resource_type TEXT NOT NULL DEFAULT '',
	failed INTEGER NOT NULL DEFAULT 0, error_text TEXT NOT NULL DEFAULT '', started_at INTEGER NOT NULL DEFAULT 0, duration_ms INTEGER,
	request_headers TEXT NOT NULL DEFAULT '{}', post_data TEXT NOT NULL DEFAULT '', post_data_via_cdp INTEGER NOT NULL DEFAULT 0,
	response_headers TEXT NOT NULL DEFAULT '{}', response_body TEXT NOT NULL DEFAULT '', body_size INTEGER NOT NULL DEFAULT 0,
	body_truncated INTEGER NOT NULL DEFAULT 0, evidence_file TEXT NOT NULL DEFAULT '');
CREATE TABLE run_triage (run_id INTEGER PRIMARY KEY REFERENCES runs(id) ON DELETE CASCADE, state TEXT NOT NULL,
	headline TEXT NOT NULL DEFAULT '', summary TEXT NOT NULL DEFAULT '', incidents TEXT NOT NULL DEFAULT '[]',
	ai INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '', updated_at INTEGER NOT NULL);

INSERT INTO runs(id, name, environment, status, started_at, ended_at, total, passed, failed) VALUES
	(1, 'Regresión', 'qa', 'FAIL', 1000, 2000, 1, 0, 1),
	(2, 'Regresión', 'qa', 'PASS', 3000, 4000, 1, 1, 0);
INSERT INTO tests(id, run_id, name, status, started_at, ended_at, error_message) VALUES
	(1, 1, 'Login', 'FAIL', 1000, 1500, 'AssertionError: x'),
	(2, 2, 'Login', 'PASS', 3000, 3500, '');
INSERT INTO logs(test_id, status, message, timestamp, screenshot) VALUES (1, 'FAIL', 'falló', 1400, '/screenshots/a.png');
INSERT INTO ai_triage(test_id, state, category, summary, suggestion, updated_at) VALUES (1, 'DONE', 'LOGIC_BUG', 's', 'x', 1600);
INSERT INTO network(test_id, seq, method, url, status) VALUES (1, 1, 'POST', 'https://app/api/login', 500);
INSERT INTO run_triage(run_id, state, headline, updated_at) VALUES (1, 'DONE', '1 test falló', 2100);
`

func TestMigrationFromFirstReleaseSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v1.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(v1Schema); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path) // migra
	if err != nil {
		t.Fatalf("open a first-release database: %v", err)
	}
	d, err := s.GetRunDetail(2)
	if err != nil || len(d.Tests) != 1 || d.Tests[0].Key != "name:Login" || !d.Tests[0].KeyApprox || d.Tests[0].Attempts != 1 {
		t.Fatalf("migrated test: %+v %v", d, err)
	}
	if h, _ := s.TestHistory("name:Login", 2, 10); len(h) != 2 || h[1].Status != "FAIL" || h[1].Category != "LOGIC_BUG" {
		t.Fatalf("history kept after migration (by name, approximate): %+v", h)
	}
	if c, _ := s.CompareRuns(2, 0); c.BaseRun == nil || c.BaseRun.ID != 1 || len(c.Fixed) != 1 {
		t.Fatalf("comparison after migration: %+v", c)
	}
	conns, _ := s.ListNetworkErrors(1, 10)
	if len(conns) != 1 || conns[0].Expected {
		t.Fatalf("old network rows are errors, not expected: %+v", conns)
	}
	if rt, _ := s.GetRunTriage(1); rt == nil || rt.Headline != "1 test falló" || rt.AIIncidents != 0 {
		t.Fatalf("run triage after migration: %+v", rt)
	}
	if _, err := s.Metrics(MetricsQuery{From: 1, To: 10_000}); err != nil {
		t.Fatalf("metrics after migration: %v", err)
	}
	// y se puede seguir escribiendo con el esquema nuevo
	id, _ := s.CreateRunWithMeta("Regresión", "qa", RunMeta{Project: "p"})
	if _, err := s.CreateTestWithMeta(id, "Login", "", "", TestMeta{Key: "t.py::test_login"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FinishRun(id); err != nil {
		t.Fatal(err)
	}
	// abrir otra vez no rompe nada (migraciones idempotentes)
	s.Close()
	if s, err = Open(path); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	s.Close()
}
