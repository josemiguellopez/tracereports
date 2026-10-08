package db

import (
	"database/sql"
	"errors"
)

// Budget of automatic AI analyses (TRACEREPORTS_AI_MAX_PER_RUN). The unit is one automatic
// diagnosis per test RESULT: a test and its result revision (tests.result_rev, which grows each
// time a finished result really changes). It is recorded in its own ledger, not counted from
// ai_triage: replacing a result deletes the previous diagnosis but never the slot it used, so
// re-sending results cannot pay the provider again and again.
//
//   - An identical resend keeps the revision: no new slot.
//   - PENDING or ERROR of the same revision (restart, provider failure) reuse their slot.
//   - Manual re-analysis does not go through the ledger: it is outside the automatic limit.
const aiBudgetSchema = `
CREATE TABLE IF NOT EXISTS ai_budget (
	run_id     INTEGER NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
	test_id    INTEGER NOT NULL,
	result_rev INTEGER NOT NULL,
	created_at INTEGER NOT NULL,
	PRIMARY KEY (run_id, test_id, result_rev)
);
`

// aiBudgetInitSchema records that the ledger was initialized (created AND backfilled). It is
// written in the same transaction as both, so its presence proves the migration finished.
const aiBudgetInitSchema = `
CREATE TABLE IF NOT EXISTS ai_budget_init (
	id         INTEGER PRIMARY KEY CHECK (id = 1),
	done_at    INTEGER NOT NULL,
	backfilled INTEGER NOT NULL,           -- diagnósticos anteriores copiados al registro
	how        TEXT    NOT NULL            -- migration | verified | recovered-backfill | recovered-keep
);
`

// missingFromLedger counts the diagnoses present (not SKIPPED) of the current result of a test
// that have no ledger row: before the migration, what was already spent.
const missingFromLedger = `SELECT COUNT(*) FROM ai_triage a JOIN tests t ON t.id = a.test_id
	WHERE a.state != 'SKIPPED' AND NOT EXISTS (
		SELECT 1 FROM ai_budget b WHERE b.run_id = t.run_id AND b.test_id = t.id AND b.result_rev = t.result_rev)`

const backfillLedger = `INSERT OR IGNORE INTO ai_budget(run_id, test_id, result_rev, created_at)
	SELECT t.run_id, t.id, t.result_rev, a.updated_at FROM ai_triage a JOIN tests t ON t.id = a.test_id
	WHERE a.state != 'SKIPPED'`

// ensureAIBudget creates the ledger. On an upgrade (no ledger yet) the analyses already spent
// count once, as before: the diagnoses present that are not SKIPPED. Table, backfill and the
// ai_budget_init mark are one transaction: an interruption leaves nothing and the next start
// repeats it all. Afterwards it is never backfilled again (manual re-analyses must not end up
// counted).
//
// A ledger without the mark was left by an earlier, non-atomic version of this migration: it
// may have been interrupted before its backfill. If no diagnosis is missing from it there is
// nothing to decide and it is marked. Otherwise the data cannot tell an interrupted backfill
// from later manual re-analyses (also absent from the ledger, on purpose), so nothing is
// guessed: AIBudgetMigration reports it and ResolveAIBudgetMigration settles it explicitly.
func ensureAIBudget(sqldb *sql.DB) error {
	tx, err := sqldb.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var ledger, mark int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'ai_budget'),
		(SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'ai_budget_init')`).Scan(&ledger, &mark); err != nil {
		return err
	}
	if mark > 0 {
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ai_budget_init`).Scan(&mark); err != nil {
			return err
		}
	}
	if ledger > 0 && mark > 0 {
		return nil // ya inicializado
	}
	if _, err := tx.Exec(aiBudgetSchema + aiBudgetInitSchema); err != nil {
		return err
	}
	how := "migration"
	if ledger > 0 { // registro sin marca: versión anterior de esta migración
		var missing int
		if err := tx.QueryRow(missingFromLedger).Scan(&missing); err != nil {
			return err
		}
		if missing > 0 {
			return tx.Commit() // ambiguo: queda sin marca hasta resolverlo (AIBudgetMigration)
		}
		how = "verified"
	}
	if err := aiBudgetHook("created"); err != nil {
		return err
	}
	res, err := tx.Exec(backfillLedger)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if err := aiBudgetHook("backfilled"); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO ai_budget_init(id, done_at, backfilled, how) VALUES(1, ?, ?, ?)`, NowMs(), n, how); err != nil {
		return err
	}
	return tx.Commit()
}

// AIBudgetMigration reports a ledger left unfinished by an earlier version of its migration:
// pending is true while it is not settled, missing is how many diagnoses (not SKIPPED) have no
// ledger row: spent before the upgrade, or manual re-analyses done after it.
func (s *Store) AIBudgetMigration() (pending bool, missing int, err error) {
	var mark int
	if err = s.db.QueryRow(`SELECT COUNT(*) FROM ai_budget_init`).Scan(&mark); err != nil || mark > 0 {
		return false, 0, err
	}
	err = s.db.QueryRow(missingFromLedger).Scan(&missing)
	return true, missing, err
}

// ResolveAIBudgetMigration settles an unfinished ledger (see AIBudgetMigration) as the operator
// decides: backfill counts every diagnosis without a ledger row as already spent (what the
// migration would have done; manual re-analyses done meanwhile count too), otherwise the ledger
// is kept as it is. Either way it is marked and returns the rows added. Without a pending
// migration it does nothing.
func (s *Store) ResolveAIBudgetMigration(backfill bool) (int64, error) {
	var added int64
	err := s.Atomic(func(tx *Store) (*Idem, error) {
		var mark int
		if err := tx.db.QueryRow(`SELECT COUNT(*) FROM ai_budget_init`).Scan(&mark); err != nil {
			return nil, err
		}
		if mark > 0 {
			return nil, errNothingToResolve
		}
		how := "recovered-keep"
		if backfill {
			res, err := tx.db.Exec(backfillLedger)
			if err != nil {
				return nil, err
			}
			added, _ = res.RowsAffected()
			how = "recovered-backfill"
		}
		_, err := tx.db.Exec(`INSERT INTO ai_budget_init(id, done_at, backfilled, how) VALUES(1, ?, ?, ?)`, NowMs(), added, how)
		return nil, err
	})
	if errors.Is(err, errNothingToResolve) {
		return 0, nil
	}
	return added, err
}

// aiBudgetHook lets tests interrupt the migration at its boundaries.
var aiBudgetHook = func(stage string) error { return nil }

var errNothingToResolve = errors.New("ai budget migration already settled")
