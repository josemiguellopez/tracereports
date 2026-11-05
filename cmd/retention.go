package main

import (
	"context"
	"log/slog"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/env"
)

// startStaleRuns closes, every 15 minutes (or sooner with a short threshold), the runs left
// RUNNING without receiving anything for the configured hours, as incomplete: they no longer show
// as "in progress" forever and retention can delete them like any other. The threshold is
// TRACEREPORTS_STALE_RUN_HOURS, or the one chosen in Mantenimiento (default 24; 0 = never), read
// on every pass so a change in the interface applies without a restart.
func startStaleRuns(ctx context.Context, srv interface {
	CloseStaleRuns(time.Duration) (int, error)
	StaleRunHours() (float64, string)
}) {
	if v := strings.TrimSpace(env.Get("STALE_RUN_HOURS")); v != "" {
		if h, err := strconv.ParseFloat(v, 64); err != nil || h < 0 {
			slog.Warn("TRACEREPORTS_STALE_RUN_HOURS is not a number of hours: using the one of Mantenimiento or 24", "value", v)
		}
	}
	if hours, source := srv.StaleRunHours(); hours == 0 {
		slog.Info("closing of abandoned runs disabled", "source", source)
	}
	go func() {
		for {
			every := 15 * time.Minute
			if hours, _ := srv.StaleRunHours(); hours > 0 {
				idle := time.Duration(hours * float64(time.Hour))
				every = min(every, max(idle/4, time.Minute))
				if _, err := srv.CloseStaleRuns(idle); err != nil {
					slog.Error("stale runs", "err", err)
				}
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(every):
			}
		}
	}()
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
