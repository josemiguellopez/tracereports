package api

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Mantenimiento > Conexiones de los clientes: todo lo que dice si la evidencia llega bien al
// servidor, sin entrar a la máquina que lo corre. Tres fuentes:
//   - los runs cuyo cliente informó un problema al cerrarlos (creados tarde, reintentos, perdidos);
//   - los pedidos lentos que vio el servidor (slowlog.go), desde que arrancó;
//   - las direcciones con credenciales rechazadas (throttle.go), solo para quien administra.

// connectionsDays: período de los runs con problemas de entrega.
const connectionsDays = 7

func (s *Server) connections(w http.ResponseWriter, r *http.Request) {
	since := time.Now().Add(-connectionsDays * 24 * time.Hour).UnixMilli()
	runs, err := s.Store.DeliveryProblems(since, 100)
	if err != nil {
		serverError(w, err)
		return
	}
	out := map[string]any{"days": connectionsDays, "runs": runs, "slow": recentSlow(), "slow_after_ms": slowRequestAfter.Milliseconds(),
		"started_at": slowSince.UnixMilli()}
	// las direcciones que fallaron credenciales y los volcados de los bloqueos: solo para quien administra
	editable, _ := s.settingsAccess(r)
	if editable && s.authFails != nil {
		out["auth"] = s.authFails.snapshot()
	}
	out["stalls"] = recentStalls(editable)
	out["stall_after_ms"] = stallAfter.Milliseconds()
	writeJSON(w, http.StatusOK, out)
}

// cleanDelivery bounds what a client reports (texts cut, counts not negative); nil when there is
// nothing to report, so a run that arrived fine stores nothing.
func cleanDelivery(in *db.RunDelivery) *db.RunDelivery {
	if in == nil {
		return nil
	}
	n := func(v int) int { return min(max(v, 0), 1_000_000_000) }
	d := db.RunDelivery{Late: in.Late, LateAt: max(in.LateAt, 0), Reason: clean(in.Reason, 200), Detail: clean(in.Detail, 300),
		Retried: n(in.Retried), Rejected: n(in.Rejected), Dropped: n(in.Dropped), Lost: n(in.Lost), Spooled: n(in.Spooled),
		Client: clean(in.Client, 60), Host: clean(in.Host, 120), ReportedAt: time.Now().UnixMilli()}
	if !d.Notable() {
		return nil
	}
	return &d
}

// authAddress is an address that sent wrong credentials: how many times in the window and for how
// long it is blocked (0: not blocked).
type authAddress struct {
	IP        string `json:"ip"`
	Failures  int    `json:"failures"`
	BlockedMs int64  `json:"blocked_ms"`
}

// snapshot: the addresses with failures in the current window, most failures first.
func (a *authFailures) snapshot() []authAddress {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	out := []authAddress{}
	for ip, f := range a.byIP {
		left := authWindow - now.Sub(f.first)
		if left <= 0 {
			continue
		}
		x := authAddress{IP: ip.String(), Failures: f.n}
		if f.n >= a.limit {
			x.BlockedMs = left.Milliseconds()
		}
		out = append(out, x)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Failures > out[j].Failures || (out[i].Failures == out[j].Failures && out[i].IP < out[j].IP)
	})
	return out
}

// stallDump sirve un volcado de bloqueo (solo para quien administra; el nombre se valida contra el
// formato que escribe el vigilante, nunca se arma una ruta con lo que llega).
func (s *Server) stallDump(w http.ResponseWriter, r *http.Request) {
	if ok, reason := s.settingsAccess(r); !ok {
		writeError(w, http.StatusForbidden, "only an administrator can download diagnostics ("+reason+")")
		return
	}
	name := chi.URLParam(r, "name")
	dir := s.diagnosticsDir()
	if !stallDumpName.MatchString(name) || dir == "" {
		writeError(w, http.StatusNotFound, "dump not found")
		return
	}
	raw, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		writeError(w, http.StatusNotFound, "dump not found")
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Write(raw)
}
