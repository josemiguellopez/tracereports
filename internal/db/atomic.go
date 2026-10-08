package db

import (
	"database/sql"
	"errors"
)

// handle is what a Store runs its SQL on: the database or, inside Atomic, one transaction.
type handle interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Begin() (txHandle, error)
}

// txHandle is a transaction as the Store methods use it.
type txHandle interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
	Prepare(query string) (*sql.Stmt, error)
	Commit() error
	Rollback() error
}

// dbHandle is the database itself.
type dbHandle struct{ *sql.DB }

func (d dbHandle) Begin() (txHandle, error) { return d.DB.Begin() }

// inTx is a Store bound to a transaction: the transactions its methods open become savepoints,
// so a method keeps its own all-or-nothing behavior inside the bigger transaction.
type inTx struct{ *sql.Tx }

func (t inTx) Begin() (txHandle, error) {
	if _, err := t.Tx.Exec(`SAVEPOINT store_method`); err != nil {
		return nil, err
	}
	return &savepoint{Tx: t.Tx}, nil
}

type savepoint struct {
	*sql.Tx
	done bool
}

func (p *savepoint) Commit() error {
	if p.done {
		return sql.ErrTxDone
	}
	p.done = true
	_, err := p.Tx.Exec(`RELEASE store_method`)
	return err
}

// Rollback undoes only the savepoint (a no-op after Commit, like sql.Tx).
func (p *savepoint) Rollback() error {
	if p.done {
		return sql.ErrTxDone
	}
	p.done = true
	if _, err := p.Tx.Exec(`ROLLBACK TO store_method`); err != nil {
		return err
	}
	_, err := p.Tx.Exec(`RELEASE store_method`)
	return err
}

// Idem is the idempotent response of a write, saved in the same transaction as the write.
type Idem struct {
	Key          string // método + ruta + Idempotency-Key
	Method, Path string
	Status       int
	Body         []byte
}

// ErrNested: Atomic was called on a Store that is already inside Atomic.
var ErrNested = errors.New("db: nested Atomic")

// Atomic runs fn on a Store bound to a single transaction and commits it only if fn succeeds.
// When fn returns an *Idem, that response is stored in the same transaction: the write and the
// record that it was applied are saved together or not at all, so a retry after a crash either
// finds the stored response or applies the write for the first time.
//
// fn must use only the Store it receives: the database has one connection, so using another
// Store (or anything that does, like the AI analyzer) inside fn would wait for this transaction
// forever. Side effects (live events, AI, notifications, files) go after Atomic returns.
func (s *Store) Atomic(fn func(tx *Store) (*Idem, error)) error {
	db, ok := s.db.(dbHandle)
	if !ok {
		return ErrNested
	}
	tx, err := db.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	idem, err := fn(&Store{db: inTx{tx}})
	if err != nil {
		return err
	}
	if idem != nil && idem.Key != "" {
		if err := saveIdempotent(tx, idem.Key, idem.Status, idem.Body, runForWrite(tx, idem.Method, idem.Path, idem.Body)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
