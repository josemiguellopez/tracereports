package db

import (
	"testing"
	"time"
)

// Un caso real: con la única conexión que escribe ocupada (una suite guardando resultados),
// abrir Mantenimiento > Almacenamiento o En vivo no espera a que se suelte: lee por las conexiones de
// solo lectura. Y al revés, una lectura larga en curso no frena a quien escribe.
func TestHeavyReadsDoNotWaitForTheWriter(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.CreateRun("r1", "qa"); err != nil {
		t.Fatal(err)
	}
	hold, held := make(chan struct{}), make(chan struct{})
	go s.Atomic(func(tx *Store) (*Idem, error) { // toma la conexión que escribe
		if _, err := tx.CreateRun("en curso", "qa"); err != nil {
			t.Error(err)
		}
		close(held)
		<-hold
		return nil, nil
	})
	<-held
	defer close(hold)
	done := make(chan error, 1)
	go func() {
		if _, err := s.FilesByProject(); err != nil {
			done <- err
			return
		}
		if _, err := s.StorageCounts(); err != nil {
			done <- err
			return
		}
		_, err := s.LiveCandidates(0)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the heavy reads waited for the writer")
	}
}

func TestALongReadDoesNotBlockWrites(t *testing.T) {
	s := openTestStore(t)
	for i := range 3 {
		if _, err := s.CreateRun("r", "qa"); err != nil {
			t.Fatal(i, err)
		}
	}
	rows, err := s.reader().Query(`SELECT id FROM runs`) // una lectura abierta (a medio recorrer)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	rows.Next()
	done := make(chan error, 1)
	go func() { _, err := s.CreateRun("nueva", "qa"); done <- err }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a write waited for a long read")
	}
}

// Los lectores no escriben, ni por error.
func TestReadersAreReadOnly(t *testing.T) {
	s := openTestStore(t)
	if _, err := s.ro.Exec(`DELETE FROM runs`); err == nil {
		t.Fatal("the read-only connections accepted a write")
	}
}
