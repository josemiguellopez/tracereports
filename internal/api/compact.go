package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/josemiguellopez/tracereports/internal/diskusage"
)

// Compactar la base (VACUUM) devuelve al disco el espacio de lo borrado, pero reescribe el archivo
// entero: en una base de varios GB tarda minutos, tiene tomada la única conexión que escribe (los
// clientes esperan para guardar resultados) y necesita libre tanto espacio como ocupa la base. Por
// eso ya no se hace al terminar cada limpieza o borrado en lote: lo borrado deja la compactación
// pendiente, y se hace sola cuando el servidor está tranquilo (sin ejecuciones en curso ni escrituras
// de los clientes en compactQuietFor, una vez cada compactMinGap como mucho) o a mano desde
// Mantenimiento > Almacenamiento. Lo borrado igual queda sobrescrito con ceros (secure_delete).

const (
	settingCompactLast    = "compact_last"
	settingCompactPending = "compact_pending"
)

var (
	compactQuietFor = 15 * time.Minute
	compactMinGap   = 20 * time.Hour
	compactEvery    = 10 * time.Minute
)

// compactSpaceMargin: libre que pide además del tamaño de la base (la copia temporal y la WAL).
var compactSpaceMargin = 1.1

var errCompactRunning = errors.New("the database is already being compacted")

// errCompactSpace: no hay lugar en el disco para la copia que hace VACUUM.
type errCompactSpace struct{ need, free int64 }

func (e errCompactSpace) Error() string {
	return fmt.Sprintf("not enough free disk to compact: it needs about %s free and there are %s", humanBytes(e.need), humanBytes(e.free))
}

type compactState struct {
	mu      sync.Mutex
	running bool
	// lastWrite: hora (Unix ms) de la última escritura de un cliente; la compactación automática espera silencio
	lastWrite atomic.Int64
}

// CompactRun is one compaction, as Maintenance shows it.
type CompactRun struct {
	At         int64  `json:"at"`
	TookMs     int64  `json:"took_ms"`
	FreedBytes int64  `json:"freed_bytes"`
	Trigger    string `json:"trigger"` // "auto" o "manual"
	By         string `json:"by,omitempty"`
}

// markCompactPending remembers (across restarts) that something was deleted and the file can shrink.
func (s *Server) markCompactPending() {
	if err := s.Store.SaveSettings(map[string]string{settingCompactPending: "1"}); err != nil {
		slog.Warn("compact: remember it is pending", "err", err)
	}
}

func (s *Server) compactInfo() (pending bool, last *CompactRun) {
	saved, err := s.Store.Settings()
	if err != nil {
		return false, nil
	}
	if raw := saved[settingCompactLast]; raw != "" {
		var r CompactRun
		if json.Unmarshal([]byte(raw), &r) == nil {
			last = &r
		}
	}
	return saved[settingCompactPending] == "1", last
}

// compactNeed is the free disk a compaction needs (about the size of the database).
func (s *Server) compactNeed() (need, free int64, err error) {
	counts, err := s.Store.StorageCounts()
	if err != nil {
		return 0, 0, err
	}
	need = int64(float64(counts.DBBytes) * compactSpaceMargin)
	u, err := diskusage.Of(s.ScreenshotsDir)
	if err != nil {
		return need, -1, nil // sin medida del disco: se intenta igual
	}
	return need, int64(u.Free), nil
}

// CompactNow compacts the database (one at a time) if there is room for it, and records it.
func (s *Server) CompactNow(trigger, by string) (CompactRun, error) {
	s.compact.mu.Lock()
	if s.compact.running {
		s.compact.mu.Unlock()
		return CompactRun{}, errCompactRunning
	}
	s.compact.running = true
	s.compact.mu.Unlock()
	defer func() {
		s.compact.mu.Lock()
		s.compact.running = false
		s.compact.mu.Unlock()
	}()
	need, free, err := s.compactNeed()
	if err != nil {
		return CompactRun{}, err
	}
	if free >= 0 && free < need {
		return CompactRun{}, errCompactSpace{need: need, free: free}
	}
	before, err := s.Store.StorageCounts()
	if err != nil {
		return CompactRun{}, err
	}
	t0 := time.Now()
	if err := s.Store.Compact(); err != nil {
		return CompactRun{}, err
	}
	run := CompactRun{At: time.Now().UnixMilli(), TookMs: time.Since(t0).Milliseconds(), Trigger: trigger, By: by}
	if after, err := s.Store.StorageCounts(); err == nil && after.DBBytes < before.DBBytes {
		run.FreedBytes = before.DBBytes - after.DBBytes
	}
	raw, _ := json.Marshal(run)
	if err := s.Store.SaveSettings(map[string]string{settingCompactLast: string(raw), settingCompactPending: ""}); err != nil {
		slog.Warn("compact: save the last one", "err", err)
	}
	slog.Info("compact: database compacted", "trigger", trigger, "freed_bytes", run.FreedBytes, "took_ms", run.TookMs)
	return run, nil
}

// compactDue says whether the automatic compaction can run now: something to give back, no run in
// progress, no client write for compactQuietFor and none in the last compactMinGap.
func (s *Server) compactDue(now time.Time) bool {
	pending, last := s.compactInfo()
	if !pending || (last != nil && now.Sub(time.UnixMilli(last.At)) < compactMinGap) {
		return false
	}
	if lw := s.compact.lastWrite.Load(); lw > 0 && now.Sub(time.UnixMilli(lw)) < compactQuietFor {
		return false
	}
	n, err := s.Store.RunningRuns()
	return err == nil && n == 0
}

// StartCompaction checks every compactEvery whether the automatic compaction is due.
func (s *Server) StartCompaction(ctx context.Context) {
	go func() {
		t := time.NewTicker(compactEvery)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				if !s.compactDue(now) {
					continue
				}
				if _, err := s.CompactNow("auto", ""); err != nil && err != errCompactRunning {
					slog.Warn("compact: automatic compaction", "err", err)
				}
			}
		}
	}()
}

// compactNowHandler is "Compact now" in Maintenance > Storage.
func (s *Server) compactNowHandler(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	run, err := s.CompactNow("manual", s.uiUser(r))
	var space errCompactSpace
	switch {
	case errors.Is(err, errCompactRunning):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &space):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "kind": "disk_space", "need": space.need, "free": space.free})
	case err != nil:
		serverError(w, err)
	default:
		writeJSON(w, http.StatusOK, run)
	}
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
