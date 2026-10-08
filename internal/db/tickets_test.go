package db

import (
	"database/sql"
	"path/filepath"
	"testing"
)

// La tabla tickets tal como la creaban las versiones anteriores (sin proyecto ni destino).
const legacyTickets = `
CREATE TABLE runs (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, environment TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'RUNNING', started_at INTEGER NOT NULL, ended_at INTEGER, total INTEGER NOT NULL DEFAULT 0,
	passed INTEGER NOT NULL DEFAULT 0, failed INTEGER NOT NULL DEFAULT 0, skipped INTEGER NOT NULL DEFAULT 0, warning INTEGER NOT NULL DEFAULT 0,
	project TEXT NOT NULL DEFAULT '');
CREATE TABLE tickets (id INTEGER PRIMARY KEY AUTOINCREMENT, run_id INTEGER NOT NULL, test_id INTEGER NOT NULL DEFAULT 0,
	test_key TEXT NOT NULL DEFAULT '', provider TEXT NOT NULL, ticket_key TEXT NOT NULL, url TEXT NOT NULL, created_at INTEGER NOT NULL);
CREATE INDEX idx_tickets_key ON tickets(test_key, provider);
INSERT INTO runs(id, name, status, started_at, project) VALUES (1, 'Nightly', 'FAIL', 1000, 'shop');
-- #1: su ejecución existe (proyecto recuperable); #2: su ejecución ya se borró (proyecto desconocido)
INSERT INTO tickets(run_id, test_id, test_key, provider, ticket_key, url, created_at) VALUES
	(1, 5, 'tests/test_login.py::test_login', 'github', '#1', 'https://gh/1', 1100),
	(77, 9, 'tests/test_login.py::test_login', 'github', '#2', 'https://gh/2', 1200);
`

func TestLegacyTicketsGetTheirProjectWhenItCanBeRecovered(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(legacyTickets); err != nil {
		t.Fatal(err)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer func() { s.Close() }() // cierra el último abierto
	// los registros se conservan
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM tickets`).Scan(&n)
	if n != 2 {
		t.Fatalf("legacy tickets must be kept: %d", n)
	}
	run2, _ := s.CreateRunWithMeta("Nightly", "qa", RunMeta{Project: "shop"})
	test2, _ := s.CreateTestWithMeta(run2, "test_login", "", "", TestMeta{Key: "tests/test_login.py::test_login"})

	// #1 recuperó su proyecto: se reutiliza en "shop" (destino desconocido: cuenta para cualquiera)
	got, err := s.ExistingTicket(run2, test2, "tests/test_login.py::test_login", "shop", "github", "github:acme/shop")
	if err != nil || got == nil || got.Key != "#1" || got.Project != "shop" {
		t.Fatalf("recovered legacy ticket: %+v %v", got, err)
	}
	// nunca en otro proyecto
	if got, _ := s.ExistingTicket(run2, test2, "tests/test_login.py::test_login", "blog", "github", "github:acme/shop"); got != nil {
		t.Fatalf("another project: %+v", got)
	}
	// #2 (proyecto desconocido) no se reutiliza ni se lista en otras ejecuciones, pero sigue en la suya
	list, _ := s.TicketsOfRun(run2)
	if len(list) != 1 || list[0].Key != "#1" {
		t.Fatalf("run 2 lists only the recoverable ticket: %+v", list)
	}
	if list, _ := s.TicketsOfRun(77); len(list) != 1 || list[0].Key != "#2" {
		t.Fatalf("the unknown-project ticket stays with its own run: %+v", list)
	}
	// abrir otra vez no cambia nada (migración idempotente)
	s.Close()
	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow(`SELECT COUNT(*) FROM tickets WHERE project IS NULL`).Scan(&n)
	if n != 1 {
		t.Fatalf("only the unrecoverable one stays without project: %d", n)
	}
}
