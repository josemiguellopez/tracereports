package api

import (
	"net/http"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// addConsole stores the browser console of a test: {entries: [{level, text, location, timestamp}]}
// with level error, warning, pageerror, info, log or debug. Text is masked like any evidence; at
// most db.MaxConsolePerTest entries are kept per test.
func (s *Server) addConsole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	var in struct {
		Entries []db.ConsoleEntry `json:"entries"`
	}
	if !decode(w, r, &in) {
		return
	}
	if _, err := s.Store.GetTest(id); respondErr(w, err, "test") {
		return
	}
	red := s.redactor()
	fallback := eventTime(r)
	entries := make([]db.ConsoleEntry, 0, len(in.Entries))
	for _, e := range in.Entries {
		e.Level = strings.ToLower(strings.TrimSpace(e.Level))
		if e.Level == "warn" {
			e.Level = "warning"
		}
		if !db.ConsoleLevels[e.Level] {
			writeError(w, http.StatusBadRequest, "level must be error, warning, pageerror, info, log or debug")
			return
		}
		e.Text = redactThenCut(red, e.Text, 4000)
		e.Location = redactThenCut(red, e.Location, 500)
		if e.Timestamp <= 0 {
			e.Timestamp = fallback
		}
		entries = append(entries, e)
	}
	var n int
	err := s.commit(w, r, func(tx *db.Store) (int, any, error) {
		var err error
		n, err = tx.AddConsole(id, entries)
		return http.StatusCreated, map[string]int{"stored": n, "dropped": len(entries) - n}, err
	})
	if err != nil {
		serverError(w, err)
		return
	}
	s.publishTest("console", id, map[string]int{"stored": n})
}
