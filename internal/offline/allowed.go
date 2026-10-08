package offline

import (
	"net/http"
	"regexp"
)

// A recording is evidence, not instructions: an events file may only create runs and tests,
// attach their evidence and close them — exactly the calls the clients record. Any other method
// or path (Settings, UI actions, imports, deletes, a path with %-escapes, "..", a query...) makes
// the recording invalid, so neither report nor push ever sends it anywhere. The /api/v1/ prefix
// alone is not an authorization.
var ingestCalls = []struct {
	method string
	path   *regexp.Regexp
}{
	{http.MethodPost, regexp.MustCompile(`^/api/v1/runs$`)},
	{http.MethodPost, regexp.MustCompile(`^/api/v1/runs/-?[0-9]{1,19}/tests$`)},
	{http.MethodPatch, regexp.MustCompile(`^/api/v1/runs/-?[0-9]{1,19}/finish$`)},
	{http.MethodPost, regexp.MustCompile(`^/api/v1/tests/-?[0-9]{1,19}/(logs|screenshot|network|dom|console|artifact)$`)},
	{http.MethodPatch, regexp.MustCompile(`^/api/v1/tests/-?[0-9]{1,19}/finish$`)},
}

// Ingest reports whether method and path are one of the calls a client records (see
// ingestCalls). Ids may be local (negative) or real (a worker joining an existing run).
func Ingest(method, path string) bool {
	for _, c := range ingestCalls {
		if c.method == method && c.path.MatchString(path) {
			return true
		}
	}
	return false
}
