package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
)

// startStaleRuns closes, every 15 minutes, the runs left RUNNING without receiving anything for
// TRACEREPORTS_STALE_RUN_HOURS hours (default 24; 0 = never), as incomplete: they no longer
// show as "in progress" forever and retention can delete them like any other.
func startStaleRuns(ctx context.Context, srv interface {
	CloseStaleRuns(time.Duration) (int, error)
}) {
	hours := 24.0
	if v := strings.TrimSpace(env.Get("STALE_RUN_HOURS")); v != "" {
		h, err := strconv.ParseFloat(v, 64)
		if err != nil || h < 0 {
			slog.Warn("TRACEREPORTS_STALE_RUN_HOURS is not a number of hours: using 24", "value", v)
		} else {
			hours = h
		}
	}
	if hours == 0 {
		slog.Info("closing of abandoned runs disabled (TRACEREPORTS_STALE_RUN_HOURS=0)")
		return
	}
	idle := time.Duration(hours * float64(time.Hour))
	every := min(15*time.Minute, max(idle/4, time.Minute))
	go func() {
		for {
			if _, err := srv.CloseStaleRuns(idle); err != nil {
				slog.Error("stale runs", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(every):
			}
		}
	}()
}

// startRetention deletes, at startup and then every 6 hours, the runs older than
// TRACEREPORTS_RETENTION_DAYS days and their screenshots. Without the variable nothing is deleted
// (and it says so: evidence piles up forever).
func startRetention(ctx context.Context, store *db.Store, shotsDir string) {
	raw := strings.TrimSpace(env.Get("RETENTION_DAYS"))
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 {
		if raw == "" {
			slog.Warn("TRACEREPORTS_RETENTION_DAYS not set: runs, screenshots and network captures are kept forever (e.g. TRACEREPORTS_RETENTION_DAYS=90)")
		} else {
			slog.Warn("TRACEREPORTS_RETENTION_DAYS is not a positive number of days: nothing is deleted", "value", raw)
		}
		return
	}
	slog.Info("retention enabled", "days", days)
	go func() {
		for {
			purgeOld(store, shotsDir, days)
			select {
			case <-ctx.Done():
				return
			case <-time.After(6 * time.Hour):
			}
		}
	}()
}

func purgeOld(store *db.Store, shotsDir string, days int) {
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
	n, shots, err := store.PurgeRunsBefore(cutoff)
	if err != nil {
		slog.Error("retention: purge runs", "err", err)
		return
	}
	removed := 0
	for _, url := range shots {
		name := strings.TrimPrefix(url, "/screenshots/")
		if name == url || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			continue
		}
		if os.Remove(filepath.Join(shotsDir, name)) == nil {
			removed++
		}
	}
	if n > 0 {
		slog.Info("retention: old runs deleted", "runs", n, "screenshots", removed, "older_than_days", days)
		// lo borrado tampoco queda en el archivo de la base ni en la WAL
		if err := store.Compact(); err != nil {
			slog.Error("retention: compact the database", "err", err)
		}
	}
}

// restrictDir leaves dir readable only by the server's user (0700). A folder created by an older
// version (0755) is tightened; a failure only warns (a volume mounted read-only for others, Windows).
func restrictDir(dir string) {
	info, err := os.Stat(dir)
	if err != nil || runtime.GOOS == "windows" || info.Mode().Perm()&0o077 == 0 {
		return
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		slog.Warn("could not restrict the permissions of the data folder (0700)", "dir", dir, "err", err)
	}
}
