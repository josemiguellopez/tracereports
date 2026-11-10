package db

import (
	"database/sql"
	"encoding/json"
	"errors"
)

// RunDelivery is how the evidence of a run reached the server, as its client reports it when it
// closes the run: whether the run could not be created at the start (the client recorded locally
// and uploaded it later), why, and what had to be retried or was lost. Only stored when there is
// something to report.
type RunDelivery struct {
	Late       bool   `json:"late,omitempty"`        // created late: recorded locally and uploaded afterwards
	LateAt     int64  `json:"late_at,omitempty"`     // when creating it failed (client clock, Unix ms)
	Reason     string `json:"reason,omitempty"`      // "the server did not answer within 30 s"
	Detail     string `json:"detail,omitempty"`      // "connected to 10.0.0.25:8080 in 3 ms, no answer in 10 s"
	Retried    int    `json:"retried,omitempty"`     // requests sent again after a timeout or a 5xx
	Rejected   int    `json:"rejected,omitempty"`    // refused by the server (4xx)
	Dropped    int    `json:"dropped,omitempty"`     // the client queue was full
	Lost       int    `json:"lost,omitempty"`        // could not be sent nor saved
	Spooled    int    `json:"spooled,omitempty"`     // saved to resend later
	Client     string `json:"client,omitempty"`      // "python 0.5.2"
	Host       string `json:"host,omitempty"`        // the machine that ran the tests
	ReportedAt int64  `json:"reported_at,omitempty"` // server time it arrived
}

// Notable: something went wrong on the way (otherwise it is not stored).
func (d RunDelivery) Notable() bool {
	return d.Late || d.Retried > 0 || d.Rejected > 0 || d.Dropped > 0 || d.Lost > 0 || d.Spooled > 0
}

// SetRunDelivery stores (or replaces) how the run's evidence arrived.
func (s *Store) SetRunDelivery(runID int64, d RunDelivery) error {
	raw, err := json.Marshal(d)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE runs SET delivery = ? WHERE id = ?`, string(raw), runID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// RunDeliveryOf is the delivery report of a run, or nil when it arrived without problems.
func (s *Store) RunDeliveryOf(runID int64) (*RunDelivery, error) {
	var raw string
	err := s.db.QueryRow(`SELECT delivery FROM runs WHERE id = ?`, runID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil || raw == "" {
		return nil, err
	}
	var d RunDelivery
	if json.Unmarshal([]byte(raw), &d) != nil {
		return nil, nil
	}
	return &d, nil
}

// DeliveryRun is a run whose evidence had problems on the way, for Maintenance > Connections.
type DeliveryRun struct {
	ID          int64       `json:"id"`
	Name        string      `json:"name"`
	ExecutionID string      `json:"execution_id"`
	Project     string      `json:"project"`
	Environment string      `json:"environment"`
	StartedAt   int64       `json:"started_at"`
	Delivery    RunDelivery `json:"delivery"`
}

// DeliveryProblems lists the runs started since `since` (Unix ms) with a delivery report, newest
// first, at most `limit`.
func (s *Store) DeliveryProblems(since int64, limit int) ([]DeliveryRun, error) {
	rows, err := s.db.Query(`SELECT id, name, execution_id, project, environment, started_at, delivery FROM runs
		WHERE delivery != '' AND started_at >= ? ORDER BY started_at DESC, id DESC LIMIT ?`, since, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeliveryRun{}
	for rows.Next() {
		var r DeliveryRun
		var raw string
		if err := rows.Scan(&r.ID, &r.Name, &r.ExecutionID, &r.Project, &r.Environment, &r.StartedAt, &raw); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &r.Delivery) != nil {
			continue
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PoolStats is the state of the connection to the database: with a single connection, a long
// operation makes every other request wait (WaitCount / WaitDuration grow, InUse stays at 1).
func (s *Store) PoolStats() sql.DBStats {
	if d, ok := s.db.(dbHandle); ok {
		return d.DB.Stats()
	}
	return sql.DBStats{}
}
