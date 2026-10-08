package ai

import (
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/repro"
)

// DevFacts is what a developer needs to reproduce and debug one failed test: where it ran, the
// full error, each failed backend call ready to replay with cURL, the browser console and the
// command to run the test locally.
type DevFacts struct {
	TestID     int64             `json:"test_id"`
	TestName   string            `json:"test_name"`
	TestKey    string            `json:"test_key,omitempty"`
	Suite      string            `json:"suite,omitempty"`
	Params     string            `json:"params,omitempty"`
	Worker     string            `json:"worker,omitempty"`
	Attempts   int               `json:"attempts,omitempty"`
	Framework  string            `json:"framework,omitempty"`
	Branch     string            `json:"branch,omitempty"`
	Commit     string            `json:"commit,omitempty"`
	Error      string            `json:"error,omitempty"`
	ErrorTrace string            `json:"error_trace,omitempty"`
	Repro      []repro.Command   `json:"repro,omitempty"`
	Calls      []DevCall         `json:"calls,omitempty"`
	Console    []db.ConsoleEntry `json:"console,omitempty"` // errores y excepciones de la página
	Artifacts  []db.Artifact     `json:"artifacts,omitempty"`
}

// DevCall is a failed backend call with its request, response and cURL.
type DevCall struct {
	ID              int64             `json:"id"`
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Status          int               `json:"status"`
	Outcome         string            `json:"outcome"`
	DurationMs      int64             `json:"duration_ms,omitempty"`
	StartedAt       int64             `json:"started_at,omitempty"`
	MimeType        string            `json:"mime_type,omitempty"`
	RequestHeaders  map[string]string `json:"request_headers,omitempty"`
	RequestBody     string            `json:"request_body,omitempty"`
	ResponseHeaders map[string]string `json:"response_headers,omitempty"`
	ResponseBody    string            `json:"response_body,omitempty"`
	BodyClipped     bool              `json:"body_clipped,omitempty"`
	Curl            string            `json:"curl"`
	TraceID         string            `json:"trace_id,omitempty"`
	RequestID       string            `json:"request_id,omitempty"`
	LogsURL         string            `json:"logs_url,omitempty"`
	TraceURL        string            `json:"trace_url,omitempty"`
	// Baseline: the same call the last time the test passed (nil if it never passed).
	Baseline *DevBaseline `json:"baseline,omitempty"`
}

// DevBaseline summarizes the same call in the last green run.
type DevBaseline struct {
	RunID       int64 `json:"run_id"`
	TestID      int64 `json:"test_id"`
	Status      int   `json:"status"`
	DurationMs  int64 `json:"duration_ms,omitempty"`
	SameContext bool  `json:"same_context"`
}
