package db

import (
	"database/sql"
	"encoding/json"

	"github.com/josemiguellopez/tracereports/internal/correlate"
)

const networkSchema = `
CREATE TABLE IF NOT EXISTS network (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	test_id           INTEGER NOT NULL REFERENCES tests(id) ON DELETE CASCADE,
	seq               INTEGER NOT NULL,
	method            TEXT    NOT NULL DEFAULT '',
	url               TEXT    NOT NULL DEFAULT '',
	status            INTEGER NOT NULL DEFAULT 0,
	status_text       TEXT    NOT NULL DEFAULT '',
	mime_type         TEXT    NOT NULL DEFAULT '',
	resource_type     TEXT    NOT NULL DEFAULT '',
	failed            INTEGER NOT NULL DEFAULT 0,
	error_text        TEXT    NOT NULL DEFAULT '',
	started_at        INTEGER NOT NULL DEFAULT 0,
	duration_ms       INTEGER,
	request_headers   TEXT    NOT NULL DEFAULT '{}',
	post_data         TEXT    NOT NULL DEFAULT '',
	post_data_via_cdp INTEGER NOT NULL DEFAULT 0,
	response_headers  TEXT    NOT NULL DEFAULT '{}',
	response_body     TEXT    NOT NULL DEFAULT '',
	body_size         INTEGER NOT NULL DEFAULT 0,
	body_truncated    INTEGER NOT NULL DEFAULT 0,
	evidence_file     TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_network_test ON network(test_id, seq);
`

// networkErrorCond matches connections that did not end OK (HTTP >= 400 or no response) and
// that the test did not declare as expected (e.g. a 401 checked on purpose).
const networkErrorCond = `(failed = 1 OR status >= 400) AND expected = 0`

// NetConn is one HTTP request/response captured by the browser during a test.
type NetConn struct {
	ID              int64             `json:"id"`
	TestID          int64             `json:"test_id"`
	Seq             int               `json:"seq"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Status          int               `json:"status"`
	StatusText      string            `json:"status_text"`
	MimeType        string            `json:"mime_type"`
	ResourceType    string            `json:"resource_type"`
	Failed          bool              `json:"failed"`
	ErrorText       string            `json:"error_text"`
	StartedAt       int64             `json:"started_at"`
	DurationMs      *int64            `json:"duration_ms"`
	RequestHeaders  map[string]string `json:"request_headers"`
	PostData        string            `json:"post_data"`
	PostDataViaCDP  bool              `json:"post_data_via_cdp"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    string            `json:"response_body"`
	BodySize        int64             `json:"body_size"`
	BodyTruncated   bool              `json:"body_truncated"`
	// EvidenceFile is the path, on the machine that ran the test, of the local JSON with the
	// complete (unmasked by size) capture — set when the client saves it with guardar_en=.
	EvidenceFile string `json:"evidence_file"`
	// BodyFile is only set in exported ZIPs: relative path of the file with the stored body.
	BodyFile string `json:"body_file,omitempty"`
	// Expected: the test declared this response as expected (a negative case checked on
	// purpose); it is never counted as an error nor proposed as the cause of a failure.
	Expected bool `json:"expected"`
	// TraceID and RequestID link the call to the backend logs (traceparent, X-Request-Id...),
	// read from its headers. LogsURL and TraceURL open them (TRACEREPORTS_LOGS_URL and
	// TRACEREPORTS_TRACE_URL); the API fills them, the store does not know the templates.
	TraceID   string `json:"trace_id,omitempty"`
	RequestID string `json:"request_id,omitempty"`
	LogsURL   string `json:"logs_url,omitempty"`
	TraceURL  string `json:"trace_url,omitempty"`
}

// AddNetwork appends connections to a test (seq continues after the existing ones).
func (s *Store) AddNetwork(testID int64, conns []NetConn) error {
	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM tests WHERE id=?`, testID).Scan(&exists); err != nil {
		return err
	}
	if exists == 0 {
		return ErrNotFound
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var seq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM network WHERE test_id=?`, testID).Scan(&seq); err != nil {
		return err
	}
	stmt, err := tx.Prepare(`INSERT INTO network(test_id, seq, method, url, status, status_text, mime_type, resource_type,
		failed, error_text, started_at, duration_ms, request_headers, post_data, post_data_via_cdp,
		response_headers, response_body, body_size, body_truncated, evidence_file, expected)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, c := range conns {
		seq++
		reqH, _ := json.Marshal(nonNil(c.RequestHeaders))
		resH, _ := json.Marshal(nonNil(c.ResponseHeaders))
		if _, err := stmt.Exec(testID, seq, c.Method, c.URL, c.Status, c.StatusText, c.MimeType, c.ResourceType,
			c.Failed, c.ErrorText, c.StartedAt, c.DurationMs, string(reqH), c.PostData, c.PostDataViaCDP,
			string(resH), c.ResponseBody, c.BodySize, c.BodyTruncated, c.EvidenceFile, c.Expected); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetNetConn returns one connection by id.
func (s *Store) GetNetConn(id int64) (*NetConn, error) {
	conns, err := s.queryNetwork(`WHERE id=?`, id)
	if err != nil {
		return nil, err
	}
	if len(conns) == 0 {
		return nil, ErrNotFound
	}
	return &conns[0], nil
}

// ListNetwork returns all connections of a test in capture order.
func (s *Store) ListNetwork(testID int64) ([]NetConn, error) {
	return s.queryNetwork(`WHERE test_id=? ORDER BY seq`, testID)
}

// ListNetworkErrors returns up to limit failed connections (HTTP >= 400 or no response).
func (s *Store) ListNetworkErrors(testID int64, limit int) ([]NetConn, error) {
	return s.queryNetwork(`WHERE test_id=? AND `+networkErrorCond+` ORDER BY seq LIMIT ?`, testID, limit)
}

// NetworkTimeline returns every connection of a test in capture order without headers or bodies
// (enough to correlate errors with the moment the test failed).
func (s *Store) NetworkTimeline(testID int64) ([]NetConn, error) {
	rows, err := s.db.Query(`SELECT id, seq, method, url, status, status_text, failed, error_text, started_at, duration_ms, expected
		FROM network WHERE test_id = ? ORDER BY seq`, testID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetConn{}
	for rows.Next() {
		c := NetConn{TestID: testID}
		var dur sql.NullInt64
		if err := rows.Scan(&c.ID, &c.Seq, &c.Method, &c.URL, &c.Status, &c.StatusText, &c.Failed, &c.ErrorText, &c.StartedAt, &dur, &c.Expected); err != nil {
			return nil, err
		}
		if dur.Valid {
			c.DurationMs = &dur.Int64
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) queryNetwork(where string, args ...any) ([]NetConn, error) {
	rows, err := s.db.Query(`SELECT id, test_id, seq, method, url, status, status_text, mime_type, resource_type,
		failed, error_text, started_at, duration_ms, request_headers, post_data, post_data_via_cdp,
		response_headers, response_body, body_size, body_truncated, evidence_file, expected FROM network `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []NetConn{}
	for rows.Next() {
		var c NetConn
		var reqH, resH string
		var dur sql.NullInt64
		if err := rows.Scan(&c.ID, &c.TestID, &c.Seq, &c.Method, &c.URL, &c.Status, &c.StatusText, &c.MimeType,
			&c.ResourceType, &c.Failed, &c.ErrorText, &c.StartedAt, &dur, &reqH, &c.PostData, &c.PostDataViaCDP,
			&resH, &c.ResponseBody, &c.BodySize, &c.BodyTruncated, &c.EvidenceFile, &c.Expected); err != nil {
			return nil, err
		}
		if dur.Valid {
			c.DurationMs = &dur.Int64
		}
		_ = json.Unmarshal([]byte(reqH), &c.RequestHeaders)
		_ = json.Unmarshal([]byte(resH), &c.ResponseHeaders)
		ids := correlate.Extract(c.RequestHeaders, c.ResponseHeaders)
		c.TraceID, c.RequestID = ids.TraceID, ids.RequestID
		out = append(out, c)
	}
	return out, rows.Err()
}

func nonNil(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	return m
}
