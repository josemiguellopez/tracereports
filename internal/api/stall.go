package api

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// El vigilante de bloqueos. El registro de pedidos lentos (slowlog.go) anota un pedido cuando
// termina: si el servidor queda trabado (una tarea tiene tomada la única conexión a la base), nada
// termina y nada se anota. El vigilante mira los pedidos EN CURSO: cuando varios llevan más de
// stallAfter sin terminar, abre un episodio, lo avisa en el log y guarda una vez un volcado de lo que
// está haciendo cada tarea del servidor (goroutines) junto al estado de la conexión a la base. Ese
// volcado dice qué tenía tomada la base. Los episodios se ven en Mantenimiento > Conexiones.

var (
	stallAfter = 10 * time.Second // un pedido trabado: más que esto sin terminar
	stallMin   = 2                // cuántos trabados a la vez son un episodio (uno solo puede ser una subida grande)
	stallEvery = 5 * time.Second  // cada cuánto mira
)

const (
	stallKeepEpisodes = 20 // en memoria, para Mantenimiento
	stallKeepDumps    = 10 // archivos de volcado en DATA_DIR/diagnostics
	stallDumpMax      = 8 << 20
)

// stallDumpName: el único formato de archivo que se sirve (nada de rutas armadas con lo que llega).
var stallDumpName = regexp.MustCompile(`^stall-\d{8}-\d{6}\.txt$`)

type inflightReq struct {
	start  time.Time
	method string
	path   string
}

// stallEpisode is a period in which several requests were stuck at once.
type stallEpisode struct {
	Start    int64    `json:"start"`          // cuando empezó el pedido trabado más antiguo
	End      int64    `json:"end,omitempty"`  // 0: sigue trabado
	MaxStuck int      `json:"max_stuck"`      // el máximo de pedidos trabados a la vez
	Paths    []string `json:"paths"`          // qué pedidos (método y ruta), los más repetidos primero
	DBWaitMs int64    `json:"db_wait_ms"`     // cuánto esperaron por la conexión a la base en el episodio
	Dump     string   `json:"dump,omitempty"` // archivo del volcado (solo para quien administra)
}

type stallWatch struct {
	mu       sync.Mutex
	inflight map[*inflightReq]struct{}
	current  *stallEpisode
	episodes []stallEpisode
	dbWait0  time.Duration
}

var stalls = &stallWatch{inflight: map[*inflightReq]struct{}{}}

// track anota un pedido en curso; la función devuelta lo quita.
func (w *stallWatch) track(method, path string, start time.Time) func() {
	r := &inflightReq{start: start, method: method, path: path}
	w.mu.Lock()
	w.inflight[r] = struct{}{}
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		delete(w.inflight, r)
		w.mu.Unlock()
	}
}

// stuck: los pedidos en curso hace más de stallAfter, el más antiguo primero.
func (w *stallWatch) stuck(now time.Time) []inflightReq {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []inflightReq
	for r := range w.inflight {
		if now.Sub(r.start) >= stallAfter {
			out = append(out, *r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].start.Before(out[j].start) })
	return out
}

// check is one look of the watchdog: it opens, updates or closes the current episode. dump writes
// the goroutine dump of a new episode and returns its file name ("" if it could not).
func (s *Server) checkStalls(now time.Time, dump func(header string) string) {
	w := stalls
	st := s.Store.PoolStats()
	stuck := w.stuck(now)
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(stuck) < stallMin {
		if w.current != nil && len(stuck) == 0 {
			ep := *w.current
			ep.End = now.UnixMilli()
			ep.DBWaitMs = (st.WaitDuration - w.dbWait0).Milliseconds()
			w.episodes = append(w.episodes, ep)
			if len(w.episodes) > stallKeepEpisodes {
				w.episodes = w.episodes[len(w.episodes)-stallKeepEpisodes:]
			}
			w.current = nil
			slog.Info("server stall ended", "seconds", (ep.End-ep.Start)/1000, "max_stuck", ep.MaxStuck, "db_wait_ms", ep.DBWaitMs)
		}
		return
	}
	paths := map[string]int{}
	for _, r := range stuck {
		paths[r.method+" "+r.path]++
	}
	if w.current == nil {
		w.dbWait0 = st.WaitDuration
		w.current = &stallEpisode{Start: stuck[0].start.UnixMilli()}
		var b strings.Builder
		fmt.Fprintf(&b, "TraceReports stall at %s\n%d requests running for more than %s:\n", now.Format(time.RFC3339), len(stuck), stallAfter)
		for _, r := range stuck {
			fmt.Fprintf(&b, "  %6.1fs  %s %s\n", now.Sub(r.start).Seconds(), r.method, r.path)
		}
		fmt.Fprintf(&b, "database connection: open=%d in_use=%d idle=%d wait_count=%d wait_duration=%s\n",
			st.OpenConnections, st.InUse, st.Idle, st.WaitCount, st.WaitDuration)
		// las lecturas largas (En vivo, Almacenamiento) van por estas: si están todas tomadas, esperan ahí
		ro := s.Store.ReaderStats()
		fmt.Fprintf(&b, "read-only connections: open=%d/%d in_use=%d idle=%d wait_count=%d wait_duration=%s\n\n",
			ro.OpenConnections, ro.MaxOpenConnections, ro.InUse, ro.Idle, ro.WaitCount, ro.WaitDuration)
		w.mu.Unlock()
		name := dump(b.String())
		w.mu.Lock()
		if w.current != nil {
			w.current.Dump = name
		}
		slog.Warn("server stalled: requests are waiting", "stuck", len(stuck), "oldest_s", int(now.Sub(stuck[0].start).Seconds()),
			// wait_count son las esperas acumuladas desde el arranque, no las de ahora
			"db_in_use", st.InUse, "db_waits_total", st.WaitCount, "readers_in_use", ro.InUse, "readers_waits_total", ro.WaitCount, "dump", name)
	}
	if w.current == nil {
		return
	}
	w.current.MaxStuck = max(w.current.MaxStuck, len(stuck))
	w.current.Paths = topPaths(paths, w.current.Paths)
}

// topPaths junta los pedidos de esta mirada con los ya vistos en el episodio (como mucho 5).
func topPaths(now map[string]int, seen []string) []string {
	for _, p := range seen {
		if _, ok := now[p]; !ok {
			now[p] = 0
		}
	}
	out := make([]string, 0, len(now))
	for p := range now {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return now[out[i]] > now[out[j]] || (now[out[i]] == now[out[j]] && out[i] < out[j])
	})
	return out[:min(len(out), 5)]
}

// recentStalls: los episodios, el más reciente primero (el que sigue abierto, arriba).
func recentStalls(withDump bool) []stallEpisode {
	w := stalls
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]stallEpisode, 0, len(w.episodes)+1)
	if w.current != nil {
		out = append(out, *w.current)
	}
	for i := len(w.episodes) - 1; i >= 0; i-- {
		out = append(out, w.episodes[i])
	}
	if !withDump {
		for i := range out {
			out[i].Dump = ""
		}
	}
	return out
}

// diagnosticsDir: DATA_DIR/diagnostics (junto a screenshots/).
func (s *Server) diagnosticsDir() string {
	if s.ScreenshotsDir == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(s.ScreenshotsDir), "diagnostics")
}

// writeStallDump guarda el volcado de todas las goroutines con su encabezado y deja los últimos
// stallKeepDumps. Devuelve el nombre del archivo.
func (s *Server) writeStallDump(header string) string {
	dir := s.diagnosticsDir()
	if dir == "" || os.MkdirAll(dir, 0o700) != nil {
		return ""
	}
	buf := make([]byte, stallDumpMax)
	n := runtime.Stack(buf, true)
	name := "stall-" + time.Now().Format("20060102-150405") + ".txt"
	if err := os.WriteFile(filepath.Join(dir, name), append([]byte(header), buf[:n]...), 0o600); err != nil {
		slog.Error("stall: write dump", "err", err)
		return ""
	}
	if files, err := filepath.Glob(filepath.Join(dir, "stall-*.txt")); err == nil && len(files) > stallKeepDumps {
		sort.Strings(files) // el nombre lleva la fecha: el orden alfabético es el cronológico
		for _, f := range files[:len(files)-stallKeepDumps] {
			os.Remove(f)
		}
	}
	return name
}

// StartStallWatch mira los pedidos en curso cada stallEvery hasta que ctx termina.
func (s *Server) StartStallWatch(ctx context.Context) {
	go func() {
		t := time.NewTicker(stallEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.checkStalls(now, s.writeStallDump)
			}
		}
	}()
}
