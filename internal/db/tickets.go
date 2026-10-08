package db

import (
	"database/sql"
	"errors"
)

// Tickets created in an issue tracker (GitHub, Jira, Azure DevOps) from a failure. Without a
// foreign key: when retention deletes an old run, its ticket is still known, so the next failure
// of the same test points to it instead of opening a duplicate.
//
// A test key is only unique inside a project, so a ticket is reused only by the same project
// and the same tracker destination (repository / project of the tracker). Tickets saved before
// these columns existed get their project from their run when it still exists; otherwise the
// project stays NULL (unknown) and the ticket is only listed with the run that created it,
// never reused for another one. Their destination is unknown (target ”): they are reused within
// the same project, as before, and the UI still offers to create another one.
const ticketsSchema = `
CREATE TABLE IF NOT EXISTS tickets (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	run_id     INTEGER NOT NULL,
	test_id    INTEGER NOT NULL DEFAULT 0,  -- 0 = la ejecución completa
	test_key   TEXT    NOT NULL DEFAULT '', -- identidad del test: une sus ejecuciones
	provider   TEXT    NOT NULL,            -- github | jira | azure
	ticket_key TEXT    NOT NULL,            -- "#12", "SHOP-34", "Bug 56"
	url        TEXT    NOT NULL,
	created_at INTEGER NOT NULL,
	project    TEXT,                        -- proyecto de la ejecución; NULL = desconocido (ticket antiguo)
	target     TEXT    NOT NULL DEFAULT ''  -- destino en el tracker (repo, proyecto); '' = desconocido
);
CREATE INDEX IF NOT EXISTS idx_tickets_run ON tickets(run_id);
`

// ticketMigrations run on every start after the columns exist (idempotent).
const ticketMigrations = `
UPDATE tickets SET project = (SELECT r.project FROM runs r WHERE r.id = tickets.run_id)
	WHERE project IS NULL AND EXISTS (SELECT 1 FROM runs r WHERE r.id = tickets.run_id);
DROP INDEX IF EXISTS idx_tickets_key;
CREATE INDEX IF NOT EXISTS idx_tickets_identity ON tickets(project, test_key, provider);
`

// Ticket is an issue created from a run or test.
type Ticket struct {
	ID        int64  `json:"id"`
	RunID     int64  `json:"run_id"`
	TestID    int64  `json:"test_id"`
	TestKey   string `json:"test_key,omitempty"`
	Project   string `json:"project,omitempty"`
	Target    string `json:"-"` // destino en el tracker: interno, no se muestra
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
	res, err := s.db.Exec(`INSERT INTO tickets(run_id, test_id, test_key, provider, ticket_key, url, created_at, project, target)
		VALUES(?,?,?,?,?,?,?,?,?)`, t.RunID, t.TestID, t.TestKey, t.Provider, t.Key, t.URL, t.CreatedAt, t.Project, t.Target)
	if err != nil {
		return err
	}
	t.ID, err = res.LastInsertId()
	return err
}

// ExistingTicket returns the latest ticket of the same failure in a provider and destination:
// the same test of the same project (by its identity, in any run) or, for a whole run, that run.
// A ticket whose destination is unknown (saved before it was recorded) also counts. nil when
// there is none.
func (s *Store) ExistingTicket(runID, testID int64, testKey, project, provider, target string) (*Ticket, error) {
	q, args := `WHERE run_id=? AND test_id=0 AND provider=?`, []any{runID, provider}
	if testID != 0 {
		q, args = `WHERE test_key=? AND test_key<>'' AND project IS NOT NULL AND project=? AND provider=?`, []any{testKey, project, provider}
	}
	q += ` AND (target=? OR target='')`
	args = append(args, target)
	t, err := scanTicket(s.db.QueryRow(`SELECT `+ticketCols+` FROM tickets `+q+
		` ORDER BY created_at DESC, id DESC LIMIT 1`, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// TicketsOfRun lists the tickets of a run's failures: created from it, or from earlier runs of
// the same tests of the same project (the ticket that is probably still open).
func (s *Store) TicketsOfRun(runID int64) ([]Ticket, error) {
	rows, err := s.db.Query(`SELECT `+ticketCols+`
		FROM tickets
		WHERE run_id = ? OR (test_key <> '' AND project IS NOT NULL
			AND project = (SELECT project FROM runs WHERE id = ?)
			AND test_key IN (SELECT test_key FROM tests WHERE run_id = ?))
		ORDER BY created_at, id`, runID, runID, runID)
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

const ticketCols = `id, run_id, test_id, test_key, provider, ticket_key, url, created_at, project, target`

func scanTicket(r scanner) (*Ticket, error) {
	var t Ticket
	var project sql.NullString
	if err := r.Scan(&t.ID, &t.RunID, &t.TestID, &t.TestKey, &t.Provider, &t.Key, &t.URL, &t.CreatedAt, &project, &t.Target); err != nil {
		return nil, err
	}
	t.Project = project.String
	return &t, nil
}
