package db

import (
	"errors"
	"path/filepath"
	"testing"
)

func atomicStore(t *testing.T) (*Store, int64) {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "a.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	runID, _ := s.CreateRun("r", "")
	testID, _ := s.CreateTest(runID, "t", "", "")
	return s, testID
}

func countRows(t *testing.T, s *Store, table string) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestAtomicSavesTheWriteAndItsResponseTogether(t *testing.T) {
	s, testID := atomicStore(t)
	key := "POST /api/v1/tests/1/logs k1"
	err := s.Atomic(func(tx *Store) (*Idem, error) {
		if _, err := tx.AddLog(testID, "INFO", "paso", 0, ""); err != nil {
			return nil, err
		}
		return &Idem{Key: key, Method: "POST", Path: "/api/v1/tests/1/logs", Status: 201, Body: []byte(`{"id":1}`)}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if countRows(t, s, "logs") != 1 {
		t.Fatal("the write is applied")
	}
	if st, body, found, _ := s.IdempotentResponse(key); !found || st != 201 || string(body) != `{"id":1}` {
		t.Fatalf("its response is stored: %v %d %s", found, st, body)
	}
}

func TestAtomicFailureLeavesNothing(t *testing.T) {
	s, testID := atomicStore(t)
	// falla a mitad (como una caída antes del commit): ni la escritura ni la respuesta
	boom := errors.New("boom")
	err := s.Atomic(func(tx *Store) (*Idem, error) {
		tx.AddLog(testID, "INFO", "paso", 0, "")
		tx.AddNetwork(testID, []NetConn{{Method: "GET", URL: "https://x/y", Status: 500}}) // con su propia transacción (savepoint)
		return nil, boom
	})
	if !errors.Is(err, boom) || countRows(t, s, "logs") != 0 || countRows(t, s, "network") != 0 {
		t.Fatalf("rolled back: %v logs=%d network=%d", err, countRows(t, s, "logs"), countRows(t, s, "network"))
	}
	// si no se puede guardar la respuesta, tampoco queda la escritura
	if _, err := s.db.Exec(`ALTER TABLE idempotency RENAME TO idempotency_off`); err != nil {
		t.Fatal(err)
	}
	err = s.Atomic(func(tx *Store) (*Idem, error) {
		if _, err := tx.AddLog(testID, "INFO", "paso", 0, ""); err != nil {
			return nil, err
		}
		return &Idem{Key: "k", Method: "POST", Path: "/api/v1/tests/1/logs", Status: 201, Body: []byte(`{}`)}, nil
	})
	if err == nil || countRows(t, s, "logs") != 0 {
		t.Fatalf("a write whose response cannot be stored must not stay: %v logs=%d", err, countRows(t, s, "logs"))
	}
}

func TestAtomicSavepointsAndNesting(t *testing.T) {
	s, testID := atomicStore(t)
	err := s.Atomic(func(tx *Store) (*Idem, error) {
		// un método que abre su transacción y falla deshace solo lo suyo
		if err := tx.AddNetwork(testID+999, []NetConn{{Method: "GET", URL: "u"}}); err == nil {
			return nil, errors.New("expected a foreign key error")
		}
		if _, err := tx.AddLog(testID, "INFO", "sigue", 0, ""); err != nil {
			return nil, err
		}
		if err := tx.Atomic(func(*Store) (*Idem, error) { return nil, nil }); !errors.Is(err, ErrNested) {
			return nil, errors.New("nested Atomic must be refused")
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if countRows(t, s, "logs") != 1 || countRows(t, s, "network") != 0 {
		t.Fatal("savepoint semantics")
	}
}
