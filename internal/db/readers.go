package db

import (
	"database/sql"
	"fmt"
)

// La base tiene una sola conexión que escribe (SQLite admite un escritor a la vez). Con WAL, los
// lectores no esperan al escritor ni lo frenan: las consultas largas que solo leen (Mantenimiento >
// Almacenamiento, En vivo) van por estas conexiones de solo lectura. Así abrir una pantalla pesada
// mientras corre una suite no deja a los clientes esperando para guardar sus resultados.

// readerConns: cuántas lecturas pesadas a la vez.
const readerConns = 4

// reading is what a read-only query runs on.
type reading interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func openReaders(uri string) (*sql.DB, error) {
	ro, err := sql.Open("sqlite", uri+"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("open readers: %w", err)
	}
	ro.SetMaxOpenConns(readerConns)
	ro.SetMaxIdleConns(readerConns)
	if err := ro.Ping(); err != nil {
		ro.Close()
		return nil, fmt.Errorf("open readers: %w", err)
	}
	return ro, nil
}

// reader is where a long read-only query runs: the read-only connections, or the transaction when
// the Store is inside Atomic (it must see its own uncommitted writes).
func (s *Store) reader() reading {
	if _, ok := s.db.(dbHandle); ok && s.ro != nil {
		return s.ro
	}
	return s.db
}

// ReaderStats is the state of the read-only connections (diagnostics).
func (s *Store) ReaderStats() sql.DBStats {
	if s.ro == nil {
		return sql.DBStats{}
	}
	return s.ro.Stats()
}
