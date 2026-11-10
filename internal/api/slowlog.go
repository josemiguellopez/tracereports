package api

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
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
		done := stalls.track(r.Method, r.URL.Path, start) // el vigilante (stall.go) ve los que no terminan
		defer done()
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		if d := time.Since(start); d >= slowRequestAfter {
			slog.Warn("slow request", "method", r.Method, "path", r.URL.Path, "status", rec.status, "ms", d.Milliseconds())
			rememberSlow(slowEntry{At: start.UnixMilli(), Method: r.Method, Path: r.URL.Path, Status: rec.status, Ms: d.Milliseconds()})
		}
	})
}

// Los últimos pedidos lentos, para verlos en Mantenimiento > Conexiones sin leer el log del
// contenedor. En memoria (se vacían al reiniciar el servidor): slowSince dice desde cuándo.
const slowKeep = 50

type slowEntry struct {
	At     int64  `json:"at"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	Ms     int64  `json:"ms"`
}

var (
	slowMu    sync.Mutex
	slowRing  []slowEntry
	slowSince = time.Now()
)

func rememberSlow(e slowEntry) {
	slowMu.Lock()
	defer slowMu.Unlock()
	slowRing = append(slowRing, e)
	if len(slowRing) > slowKeep {
		slowRing = slowRing[len(slowRing)-slowKeep:]
	}
}

// recentSlow: los pedidos lentos guardados, el más reciente primero.
func recentSlow() []slowEntry {
	slowMu.Lock()
	defer slowMu.Unlock()
	out := make([]slowEntry, len(slowRing))
	for i, e := range slowRing {
		out[len(slowRing)-1-i] = e
	}
	return out
}
