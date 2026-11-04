package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// Tests cut before they finished (status INTERRUPTED): the run was closed while they were still
// RUNNING, by the client (Ctrl+C, a crashed worker), by inactivity or by a person from the
// interface. They are neither a failure nor a pass: they do not count in the pass rate, flakiness
// or the release gate, and they are not diagnosed by the AI.

// InterruptedMessage is the error of the tests a run was closed without (they never finished).
const InterruptedMessage = "Interrumpido: el test no terminó antes de cerrar la ejecución (worker caído, timeout o cancelación)."

// ClosedBy of a run closed because it stopped receiving anything, and of one closed by hand
// without a UI login (the server only accepts it from this machine).
const (
	ClosedByInactivity = "auto"
	ClosedByHand       = "manual"
)

// MaxCloseReason bounds the reason a person writes when closing a run by hand.
const MaxCloseReason = 500

// ErrRunClosed: the run was already closed (nothing to close by hand).
var ErrRunClosed = errors.New("run already closed")

// IdleMessage is the error of the tests of a run closed for inactivity.
func IdleMessage(idle string) string {
	return "Interrumpido: la ejecución se cerró sola tras " + idle + " sin recibir datos (el proceso del test se detuvo sin avisar)."
}

// ManualMessage is the error of the tests of a run closed by hand from the interface.
func ManualMessage(by, reason string) string {
	who := "desde la interfaz"
	if by != "" {
		who = "por " + by
	}
	msg := "Interrumpido: ejecución cerrada manualmente " + who
	if reason != "" {
		msg += ". Motivo: " + reason
	}
	return msg + "."
}

// CloseRunByHand closes a run still RUNNING from the interface: its RUNNING tests become
// INTERRUPTED with the reason, the run is marked incomplete and remembers who closed it and why.
// ErrRunClosed when it was already closed.
func (s *Store) CloseRunByHand(runID int64, by, reason string) (*Run, error) {
	reason = strings.TrimSpace(reason)
	if n := []rune(reason); len(n) > MaxCloseReason {
		reason = string(n[:MaxCloseReason])
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var status string
	err = tx.QueryRow(`SELECT status FROM runs WHERE id=?`, runID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if status != "RUNNING" {
		return nil, ErrRunClosed
	}
	if _, err := closeRunTx(tx, runID, true, ManualMessage(by, reason)); err != nil {
		return nil, err
	}
	if by == "" {
		by = ClosedByHand
	}
	if _, err := tx.Exec(`UPDATE runs SET closed_by=?, close_reason=? WHERE id=?`, by, reason, runID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetRun(runID)
}

// migrateInterrupted turns the tests that earlier versions closed as FAIL with InterruptedMessage
// into INTERRUPTED (once: afterwards none match) and recounts their closed runs.
func migrateInterrupted(sqldb *sql.DB) error {
	rows, err := sqldb.Query(`SELECT DISTINCT run_id FROM tests WHERE status='FAIL' AND error_message=?`, InterruptedMessage)
	if err != nil {
		return err
	}
	var runs []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		runs = append(runs, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(runs) == 0 {
		return err
	}
	tx, err := sqldb.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tests SET status='INTERRUPTED' WHERE status='FAIL' AND error_message=?`, InterruptedMessage); err != nil {
		return err
	}
	for _, id := range runs {
		if err := refreshClosedRun(tx, id); err != nil {
			return fmt.Errorf("run %d: %w", id, err)
		}
	}
	return tx.Commit()
}
