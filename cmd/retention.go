package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
)

// startRetention deletes, at startup and then every 6 hours, the runs older than
// TRACEREPORTS_RETENTION_DAYS days and their screenshots. Without the variable nothing is deleted.
func startRetention(ctx context.Context, store *db.Store, shotsDir string) {
	days, err := strconv.Atoi(env.Get("RETENTION_DAYS"))
	if err != nil || days <= 0 {
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
	}
}
