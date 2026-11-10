package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// slowRequestAfter: a request that takes longer is logged. A client that gave up waiting (its
// timeout) with no such line in the log never reached the server: the connection stalled before
// it (Docker's port forwarding, a proxy, the network), not in TraceReports.
var slowRequestAfter = 2 * time.Second

// slowRequests logs every request slower than slowRequestAfter with its method, path, status and
// duration (never the query, which may carry names or keys). The event stream stays open by
// design and is not measured.
func slowRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/stream") {
			next.ServeHTTP(w, r)
			return
		}
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if d := time.Since(start); d >= slowRequestAfter {
			slog.Warn("slow request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", d.Milliseconds())
		}
	})
}
