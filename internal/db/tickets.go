package db

import (
	"database/sql"
	"errors"
)

// Tickets created in an issue tracker (GitHub, Jira, Azure DevOps) from a failure. Without a
// foreign key: when retention deletes an old run, its ticket is still known, so the next failure
// of the same test points to it instead of opening a duplicate.
const ticketsSchema = `
CREATE TABLE IF NOT EXISTS tickets (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id     INTEGER NOT NULL,
	test_id    INTEGER NOT NULL DEFAULT 0,  -- 0 = la ejecución completa
	test_key   TEXT    NOT NULL DEFAULT '', -- identidad del test: une sus ejecuciones
	provider   TEXT    NOT NULL,            -- github | jira | azure
	ticket_key TEXT    NOT NULL,            -- "#12", "SHOP-34", "Bug 56"
	url        TEXT    NOT NULL,
	created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tickets_run ON tickets(run_id);
CREATE INDEX IF NOT EXISTS idx_tickets_key ON tickets(test_key, provider);
`

// Ticket is an issue created from a run or test.
type Ticket struct {
	ID        int64  `json:"id"`
	RunID     int64  `json:"run_id"`
	TestID    int64  `json:"test_id"`
	TestKey   string `json:"test_key,omitempty"`
	Provider  string `json:"provider"`
	Key       string `json:"key"`
	URL       string `json:"url"`
	CreatedAt int64  `json:"created_at"`
}

// SaveTicket records a created ticket.
func (s *Store) SaveTicket(t *Ticket) error {
	if t.CreatedAt == 0 {
		t.CreatedAt = NowMs()
	}
	res, err := s.db.Exec(`INSERT INTO tickets(run_id, test_id, test_key, provider, ticket_key, url, created_at) VALUES(?,?,?,?,?,?,?)`,
		t.RunID, t.TestID, t.TestKey, t.Provider, t.Key, t.URL, t.CreatedAt)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// ExistingTicket returns the latest ticket of the same failure in a provider: the same test (by
// its identity, in any run) or, for a whole run, that run. nil when there is none.
func (s *Store) ExistingTicket(runID, testID int64, testKey, provider string) (*Ticket, error) {
	q, args := `WHERE run_id=? AND test_id=0 AND provider=?`, []any{runID, provider}
	if testID != 0 {
		q, args = `WHERE test_key=? AND test_key<>'' AND provider=?`, []any{testKey, provider}
	}
	t, err := scanTicket(s.db.QueryRow(`SELECT id, run_id, test_id, test_key, provider, ticket_key, url, created_at FROM tickets `+q+
		` ORDER BY created_at DESC, id DESC LIMIT 1`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// TicketsOfRun lists the tickets of a run's failures: created from it, or from earlier runs of
// the same tests (the ticket that is probably still open).
func (s *Store) TicketsOfRun(runID int64) ([]Ticket, error) {
	rows, err := s.db.Query(`SELECT k.id, k.run_id, k.test_id, k.test_key, k.provider, k.ticket_key, k.url, k.created_at
		FROM tickets k
		WHERE k.run_id = ? OR (k.test_key <> '' AND k.test_key IN (SELECT test_key FROM tests WHERE run_id = ?))
		ORDER BY k.created_at, k.id`, runID, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, rows.Err()
}

func scanTicket(r scanner) (*Ticket, error) {
	var t Ticket
	if err := r.Scan(&t.ID, &t.RunID, &t.TestID, &t.TestKey, &t.Provider, &t.Key, &t.URL, &t.CreatedAt); err != nil {
		return nil, err
	}
	return &t, nil
}
