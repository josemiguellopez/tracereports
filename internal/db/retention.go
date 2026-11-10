package db

import (
	"database/sql"
	"strings"
)

// PurgeRunsBefore deletes the finished runs that started before cutoff (Unix ms) with all their
// evidence (tests, steps, network, DOM, diagnoses, escalations, artifacts: ON DELETE CASCADE). It
// returns how many runs were deleted and the URLs ("/screenshots/<file>") of the screenshots and
// artifacts (traces, videos) they referenced, so the caller can delete the files.
//
// It goes in small batches (purgeBatch). Each batch is one transaction that takes the write lock from
// its first statement: the set of runs is re-checked, their files are read and exactly those runs are
// deleted, while no other writer can close a run or add evidence in between (that left files nobody
// would ever delete). A run closed after the purge started is left for the next pass, with its files.
func (s *Store) PurgeRunsBefore(cutoff int64) (int, []string, error) {
	// los candidatos se eligen sin tomar la escritura; cada tanda se vuelve a comprobar adentro
	candidates, err := int64s(s.db, `SELECT id FROM runs WHERE started_at < ? AND status != 'RUNNING' ORDER BY id`, cutoff)
	if err != nil || len(candidates) == 0 {
		return 0, nil, err
	}
	return s.deleteInBatches(candidates, `started_at < ? AND status != 'RUNNING'`, cutoff)
}

// purgeBatch: cuántas ejecuciones borra cada transacción. Borrar cientos de ejecuciones de una base de
// varios GB en una sola transacción tenía tomada la única conexión que escribe por minutos (los clientes
// esperaban) y hacía crecer la WAL con todo lo borrado de una vez (secure_delete reescribe cada página):
// con el disco justo, fallaba. De a tandas, entre una y otra escriben los clientes y la WAL se recicla.
const purgeBatch = 25

// deleteInBatches deletes the runs among ids that still match cond (re-checked inside each batch's
// transaction) and returns how many it deleted and their files. On an error it returns what the
// batches already committed deleted (their files included), so they are removed from disk too.
func (s *Store) deleteInBatches(ids []int64, cond string, condArgs ...any) (int, []string, error) {
	deleted := 0
	var files []string
	for start := 0; start < len(ids); start += purgeBatch {
		n, f, err := s.deleteBatch(ids[start:min(start+purgeBatch, len(ids))], cond, condArgs...)
		deleted += n
		files = append(files, f...)
		if err != nil {
			return deleted, files, err
		}
		if start+purgeBatch < len(ids) {
			// lo borrado ya está en el archivo: la WAL se reutiliza en vez de crecer (sin esperar a nadie)
			_, _ = s.db.Exec(`PRAGMA wal_checkpoint(PASSIVE)`)
		}
	}
	return deleted, files, nil
}

func (s *Store) deleteBatch(batch []int64, cond string, condArgs ...any) (int, []string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	// una escritura que no cambia nada: toma el lock de escritura ya, no al llegar al DELETE
	if _, err := tx.Exec(`DELETE FROM runs WHERE 0`); err != nil {
		return 0, nil, err
	}
	in, args := inList(batch)
	ids, err := int64s(tx, `SELECT id FROM runs WHERE id IN `+in+` AND `+cond+` ORDER BY id`, append(args, condArgs...)...)
	if err != nil || len(ids) == 0 {
		return 0, nil, err
	}
	in, args = inList(ids)
	files, err := strs(tx, `SELECT l.screenshot FROM logs l JOIN tests t ON t.id = l.test_id
		WHERE t.run_id IN `+in+` AND l.screenshot != ''
		UNION ALL
		SELECT a.url FROM artifacts a JOIN tests t ON t.id = a.test_id WHERE t.run_id IN `+in, append(args, args...)...)
	if err != nil {
		return 0, nil, err
	}
	if purgeHook != nil {
		purgeHook()
	}
	res, err := tx.Exec(`DELETE FROM runs WHERE id IN `+in, args...)
	if err != nil {
		return 0, nil, err
	}
	n, _ := res.RowsAffected()
	if err := tx.Commit(); err != nil {
		return 0, nil, err
	}
	return int(n), files, nil
}

// Compact rewrites the database without free pages (VACUUM) and empties the WAL (checkpoint), so
// what a purge deleted is not left anywhere in the files: secure_delete already zeroes the deleted
// rows, VACUUM drops the free pages and the checkpoint, the old copies of the pages that the WAL
// keeps. It takes the write lock while it runs; retention calls it only after deleting runs.
func (s *Store) Compact() error {
	if _, err := s.db.Exec(`VACUUM`); err != nil {
		return err
	}
	_, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// rowsQuerier runs a query: the database or a transaction.
type rowsQuerier interface {
	Query(query string, args ...any) (*sql.Rows, error)
}

// purgeChunk keeps each IN (...) well below SQLite's limit of bound parameters.
const purgeChunk = 500

func inList(ids []int64) (string, []any) {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(?" + strings.Repeat(",?", len(ids)-1) + ")", args
}

func int64s(q rowsQuerier, query string, args ...any) ([]int64, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var v int64
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func strs(q rowsQuerier, query string, args ...any) ([]string, error) {
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// purgeHook runs between choosing the evidence and deleting the runs (tests only).
var purgeHook func()
