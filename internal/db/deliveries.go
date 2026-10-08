package db

import (
	"database/sql"
	"errors"
)

// Notification deliveries to chat webhooks (Teams, Slack): an outbox in SQLite so a send that
// fails for a transient reason is retried, also after a restart. One row per message and channel:
// a channel that already received a message is never sent it again because another one failed.
// The webhook URL is not stored (it is a credential): the channel name is resolved when sending.
const deliveriesSchema = `
CREATE TABLE IF NOT EXISTS deliveries (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	dedup_key  TEXT    NOT NULL,            -- "run:12:<hash>", "weekly:2026-10-05"...
	channel    TEXT    NOT NULL,            -- teams | slack
	kind       TEXT    NOT NULL,            -- run | weekly | escalation
	payload    BLOB    NOT NULL,            -- el JSON que se envía
	state      TEXT    NOT NULL DEFAULT 'pending', -- pending | sent | failed
	attempts   INTEGER NOT NULL DEFAULT 0,
	next_at    INTEGER NOT NULL,            -- cuándo intentar (también el plazo de un intento en curso)
	last_error TEXT    NOT NULL DEFAULT '',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_deliveries_due ON deliveries(state, next_at);
CREATE INDEX IF NOT EXISTS idx_deliveries_key ON deliveries(dedup_key, channel);
`

// Delivery states.
const (
	DeliveryPending = "pending"
	DeliverySent    = "sent"
	DeliveryFailed  = "failed"
)

// Delivery is one message to one channel.
type Delivery struct {
	ID        int64  `json:"id"`
	DedupKey  string `json:"dedup_key"`
	Channel   string `json:"channel"`
	Kind      string `json:"kind"`
	Payload   []byte `json:"-"`
	State     string `json:"state"`
	Attempts  int    `json:"attempts"`
	NextAt    int64  `json:"next_at"`
	LastError string `json:"last_error,omitempty"`
	CreatedAt int64  `json:"created_at"`
}

// EnqueueDelivery adds a delivery unless one with the same key and channel already exists: any
// state when onlyPending is false (automatic messages: never twice), only a pending one when
// true (a message sent on request may be sent again; a pending one is reused). It returns the
// delivery (new or existing) and whether it was created.
func (s *Store) EnqueueDelivery(d *Delivery, onlyPending bool) (*Delivery, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	q := `SELECT ` + deliveryCols + ` FROM deliveries WHERE dedup_key = ? AND channel = ?`
	if onlyPending {
		q += ` AND state = 'pending'`
	}
	existing, err := scanDelivery(tx.QueryRow(q+` ORDER BY id DESC LIMIT 1`, d.DedupKey, d.Channel))
	if err == nil {
		return existing, false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, false, err
	}
	now := NowMs()
	res, err := tx.Exec(`INSERT INTO deliveries(dedup_key, channel, kind, payload, state, next_at, created_at, updated_at)
		VALUES(?,?,?,?, 'pending', ?, ?, ?)`, d.DedupKey, d.Channel, d.Kind, d.Payload, now, now, now)
	if err != nil {
		return nil, false, err
	}
	out := *d
	out.ID, _ = res.LastInsertId()
	out.State, out.NextAt, out.CreatedAt = DeliveryPending, now, now
	return &out, true, tx.Commit()
}

// ClaimDelivery takes a pending, due delivery for one attempt: it moves next_at to until (the
// lease), so nobody else attempts it meanwhile; if the process dies, it is retried after until.
func (s *Store) ClaimDelivery(id, until int64) (*Delivery, error) {
	now := NowMs()
	res, err := s.db.Exec(`UPDATE deliveries SET next_at = ?, attempts = attempts + 1, updated_at = ?
		WHERE id = ? AND state = 'pending' AND next_at <= ?`, until, now, id, now)
	if err != nil {
		return nil, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	return scanDelivery(s.db.QueryRow(`SELECT `+deliveryCols+` FROM deliveries WHERE id = ?`, id))
}

// DueDeliveries lists pending deliveries whose time has come (oldest first).
func (s *Store) DueDeliveries(limit int) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM deliveries WHERE state = 'pending' AND next_at <= ? ORDER BY next_at, id LIMIT ?`, NowMs(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// FinishDelivery records the result of an attempt: sent, retry at nextAt (pending), or failed.
func (s *Store) FinishDelivery(id int64, state string, nextAt int64, lastError string) error {
	_, err := s.db.Exec(`UPDATE deliveries SET state = ?, next_at = ?, last_error = ?, updated_at = ? WHERE id = ?`,
		state, nextAt, lastError, NowMs(), id)
	return err
}

// GetDelivery returns one delivery.
func (s *Store) GetDelivery(id int64) (*Delivery, error) {
	d, err := scanDelivery(s.db.QueryRow(`SELECT `+deliveryCols+` FROM deliveries WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return d, err
}

// PruneDeliveries deletes finished deliveries (sent or failed) last updated before cutoff.
func (s *Store) PruneDeliveries(cutoff int64) error {
	_, err := s.db.Exec(`DELETE FROM deliveries WHERE state != 'pending' AND updated_at < ?`, cutoff)
	return err
}

const deliveryCols = `id, dedup_key, channel, kind, payload, state, attempts, next_at, last_error, created_at`

func scanDelivery(r scanner) (*Delivery, error) {
	var d Delivery
	if err := r.Scan(&d.ID, &d.DedupKey, &d.Channel, &d.Kind, &d.Payload, &d.State, &d.Attempts, &d.NextAt, &d.LastError, &d.CreatedAt); err != nil {
		return nil, err
	}
	return &d, nil
}
