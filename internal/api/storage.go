package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/josemiguellopez/tracereports/internal/diskusage"
	"github.com/josemiguellopez/tracereports/internal/env"
)

// Almacenamiento (Mantenimiento): cuánto ocupa la evidencia, cuánto queda en el disco, a qué ritmo
// crece, y la retención (borrar las ejecuciones más antiguas que N días) elegida en la interfaz o
// con TRACEREPORTS_RETENTION_DAYS, que manda.

const (
	// settingRetentionDays stores the retention chosen in the interface (0 = keep everything).
	settingRetentionDays = "retention_days"
	// settingRetentionLast stores the last cleanup (JSON), to show it after a restart.
	settingRetentionLast = "retention_last"
	maxRetentionDays     = 3650
	// RetentionEvery is how often the automatic cleanup runs.
	RetentionEvery = 6 * time.Hour
	// DiskWarnPercent: from this share of the disk in use, Maintenance and the log warn.
	DiskWarnPercent = 80
	// storageTopProjects: the projects listed by size.
	storageTopProjects = 10
)

// RetentionRun is one cleanup: when, what it deleted and what it freed.
type RetentionRun struct {
	At         int64  `json:"at"`
	Days       int    `json:"days"`
	Runs       int    `json:"runs"`
	Files      int    `json:"files"`
	FreedBytes int64  `json:"freed_bytes"`
	Trigger    string `json:"trigger"` // auto | manual
	By         string `json:"by,omitempty"`
	// Error: la limpieza se cortó con un error; lo de Runs y FreedBytes sí se borró
	Error string `json:"error,omitempty"`
}

// retentionState is the cleanup in progress and the next automatic one.
type retentionState struct {
	mu      sync.Mutex
	running bool
	next    int64 // ms; 0 = no automatic cleanup scheduled
}

// RetentionDays is how many days of runs are kept (0 = all) and where it comes from: "env"
// (TRACEREPORTS_RETENTION_DAYS, which wins; a value that is not a positive number keeps
// everything), "ui" (Maintenance) or "default" (keep everything).
func (s *Server) RetentionDays() (int, string) {
	if v := strings.TrimSpace(env.Get("RETENTION_DAYS")); v != "" {
		if d, err := strconv.Atoi(v); err == nil && d > 0 {
			return d, "env"
		}
		return 0, "env"
	}
	if saved, err := s.Store.Settings(); err == nil {
		if d, err := strconv.Atoi(saved[settingRetentionDays]); err == nil && d >= 0 {
			return d, "ui"
		}
	}
	return 0, "default"
}

func (s *Server) lastRetention() *RetentionRun {
	saved, err := s.Store.Settings()
	if err != nil || saved[settingRetentionLast] == "" {
		return nil
	}
	var r RetentionRun
	if json.Unmarshal([]byte(saved[settingRetentionLast]), &r) != nil {
		return nil
	}
	return &r
}

// errCleanupRunning: a cleanup is already in progress.
var errCleanupRunning = fmt.Errorf("a cleanup is already running")

// PurgeOld deletes the finished runs started more than `days` days ago with their screenshots and
// attachments, compacts the database and records what it freed.
func (s *Server) PurgeOld(days int, trigger, by string) (RetentionRun, error) {
	s.retention.mu.Lock()
	if s.retention.running {
		s.retention.mu.Unlock()
		return RetentionRun{}, errCleanupRunning
	}
	s.retention.running = true
	s.retention.mu.Unlock()
	defer func() {
		s.retention.mu.Lock()
		s.retention.running = false
		s.retention.mu.Unlock()
	}()

	run := RetentionRun{At: time.Now().UnixMilli(), Days: days, Trigger: trigger, By: by}
	cutoff := time.Now().AddDate(0, 0, -days).UnixMilli()
	// si una tanda falla, las anteriores ya se borraron: sus archivos se borran igual y después se informa
	n, urls, purgeErr := s.Store.PurgeRunsBefore(cutoff)
	run.Runs = n
	for _, url := range urls {
		path := s.screenshotPath(url)
		if path == "" {
			continue
		}
		info, err := os.Stat(path)
		if err == nil && os.Remove(path) == nil {
			run.Files++
			run.FreedBytes += info.Size()
		}
	}
	if n > 0 {
		// el espacio de la base se devuelve al compactarla, aparte (compact.go): reescribe toda la base
		s.markCompactPending()
		slog.Info("retention: old runs deleted", "runs", n, "files", run.Files, "freed_bytes", run.FreedBytes,
			"older_than_days", days, "trigger", oneLine(trigger))
	}
	if purgeErr != nil {
		run.Error = purgeErr.Error()
	}
	// también una limpieza cortada a medias: lo que borró se ve en "Última limpieza" (decía 0)
	if purgeErr == nil || n > 0 {
		if raw, err := json.Marshal(run); err == nil {
			if err := s.Store.SaveSettings(map[string]string{settingRetentionLast: string(raw)}); err != nil {
				slog.Error("retention: save the last cleanup", "err", err)
			}
		}
	}
	if purgeErr != nil {
		if n > 0 {
			s.publish("run", 0, 0, map[string]string{"action": "deleted"})
		}
		return run, purgeErr
	}
	s.publish("run", 0, 0, map[string]string{"action": "deleted"}) // las listas abiertas se ponen al día
	return run, nil
}

// screenshotPath is the file of a /screenshots/ URL inside ScreenshotsDir ("" if it is not one).
func (s *Server) screenshotPath(url string) string {
	name := strings.TrimPrefix(url, "/screenshots/")
	if s.ScreenshotsDir == "" || name == url || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return ""
	}
	return filepath.Join(s.ScreenshotsDir, name)
}

// StartRetention runs the automatic cleanup now and every RetentionEvery with the days in force at
// each pass (a change in Maintenance applies without a restart), and warns in the log when the
// disk is almost full.
func (s *Server) StartRetention(ctx context.Context) {
	if days, source := s.RetentionDays(); days == 0 {
		if source == "env" {
			slog.Warn("TRACEREPORTS_RETENTION_DAYS is not a positive number of days: nothing is deleted",
				"value", env.Get("RETENTION_DAYS"))
		} else {
			slog.Warn("TRACEREPORTS_RETENTION_DAYS not set and no retention chosen in Maintenance: runs, " +
				"screenshots and network captures are kept forever (e.g. 90 days)")
		}
	} else {
		slog.Info("retention enabled", "days", days, "source", source)
	}
	go func() {
		for {
			if days, _ := s.RetentionDays(); days > 0 {
				if _, err := s.PurgeOld(days, "auto", ""); err != nil && err != errCleanupRunning {
					slog.Error("retention: purge runs", "err", err)
				}
			}
			if u, err := diskusage.Of(s.ScreenshotsDir); err == nil && u.UsedPercent() >= DiskWarnPercent {
				slog.Warn("the data disk is almost full: choose a retention in Maintenance or free space",
					"used_percent", int(u.UsedPercent()), "free_bytes", u.Free)
			}
			s.retention.mu.Lock()
			s.retention.next = time.Now().Add(RetentionEvery).UnixMilli()
			s.retention.mu.Unlock()
			select {
			case <-ctx.Done():
				return
			case <-time.After(RetentionEvery):
			}
		}
	}()
}

// fileSizes measures the files of ScreenshotsDir (name -> bytes).
func (s *Server) fileSizes() (map[string]int64, error) {
	out := map[string]int64{}
	if s.ScreenshotsDir == "" {
		return out, nil
	}
	err := filepath.WalkDir(s.ScreenshotsDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		if info, err := d.Info(); err == nil {
			rel, _ := filepath.Rel(s.ScreenshotsDir, path)
			out[filepath.ToSlash(rel)] = info.Size()
		}
		return nil
	})
	return out, err
}

func sumURLs(sizes map[string]int64, urls []string) (bytes int64, n int) {
	for _, u := range urls {
		if name := strings.TrimPrefix(u, "/screenshots/"); name != u {
			if b, ok := sizes[name]; ok {
				bytes += b
				n++
			}
		}
	}
	return bytes, n
}

// storageTopDOMRuns: the heaviest runs by Inspector pages listed in Storage.
const storageTopDOMRuns = 10

type projectUsage struct {
	Project string `json:"project"`
	Runs    int    `json:"runs"`
	Tests   int    `json:"tests"`
	Bytes   int64  `json:"bytes"` // archivos + su parte de la base (aproximada, por tests)
}

// storage reports what the server's data takes (GET /api/v1/maintenance/storage).
func (s *Server) storage(w http.ResponseWriter, r *http.Request) {
	counts, err := s.Store.StorageCounts()
	if err != nil {
		serverError(w, err)
		return
	}
	sizes, err := s.fileSizes()
	if err != nil {
		serverError(w, err)
		return
	}
	var filesBytes int64
	for _, b := range sizes {
		filesBytes += b
	}
	byProject, err := s.Store.FilesByProject()
	if err != nil {
		serverError(w, err)
		return
	}
	projects := make([]projectUsage, 0, len(byProject))
	var referenced int64
	for _, p := range byProject {
		b, _ := sumURLs(sizes, p.URLs)
		referenced += b
		if counts.Tests > 0 {
			b += counts.DBBytes * int64(p.Tests) / int64(counts.Tests)
		}
		projects = append(projects, projectUsage{Project: p.Project, Runs: p.Runs, Tests: p.Tests, Bytes: b})
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].Bytes > projects[j].Bytes })
	more := 0
	if len(projects) > storageTopProjects {
		more = len(projects) - storageTopProjects
		projects = projects[:storageTopProjects]
	}
	total := counts.DBBytes + filesBytes
	out := map[string]any{
		"db_bytes": counts.DBBytes, "files_bytes": filesBytes, "files": len(sizes), "total_bytes": total,
		// archivos que ninguna ejecución usa (de borrados interrumpidos o versiones viejas)
		"orphan_bytes": max(0, filesBytes-referenced),
		"runs":         counts.Runs, "tests": counts.Tests, "oldest_run": counts.OldestRun, "newest_run": counts.NewestRun,
		"projects": projects, "more_projects": more, "warn_percent": DiskWarnPercent,
	}
	// ritmo: lo que ocupa hoy repartido entre los días que cubre (una estimación, no una medición)
	if counts.OldestRun > 0 {
		days := max(1, float64(time.Now().UnixMilli()-counts.OldestRun)/float64(24*time.Hour/time.Millisecond))
		perDay := float64(total) / days
		out["bytes_per_day"] = int64(perDay)
		if u, err := diskusage.Of(s.ScreenshotsDir); err == nil && perDay > 0 {
			out["days_until_full"] = int(float64(u.Free) / perDay)
		}
	}
	if u, err := diskusage.Of(s.ScreenshotsDir); err == nil && s.ScreenshotsDir != "" {
		out["disk"] = map[string]any{"total": u.Total, "free": u.Free, "used_percent": u.UsedPercent()}
		out["warn"] = u.UsedPercent() >= DiskWarnPercent
	}
	days, source := s.RetentionDays()
	editable, _ := s.settingsAccess(r)
	canRun, _ := s.uiAccess(r)
	s.retention.mu.Lock()
	ret := map[string]any{"days": days, "source": source, "editable": editable && source != "env", "can_run": canRun,
		"every_hours": int(RetentionEvery / time.Hour), "running": s.retention.running, "last": s.lastRetention()}
	if days > 0 && s.retention.next > 0 {
		ret["next_at"] = s.retention.next
	}
	s.retention.mu.Unlock()
	out["retention"] = ret
	pending, last := s.compactInfo()
	s.compact.mu.Lock()
	cmp := map[string]any{"pending": pending, "running": s.compact.running, "last": last, "can_run": canRun,
		"quiet_minutes": int(compactQuietFor / time.Minute)}
	s.compact.mu.Unlock()
	if rb, err := s.Store.ReclaimableBytes(); err == nil {
		cmp["reclaimable_bytes"] = rb
	}
	if need, free, err := s.compactNeed(); err == nil {
		cmp["need_bytes"] = need
		if free >= 0 {
			cmp["free_bytes"] = free
		}
	}
	if n, err := s.Store.RunningRuns(); err == nil {
		cmp["running_runs"] = n
	}
	out["compact"] = cmp
	// las páginas del Inspector: lo que más crece con TRACEREPORTS_DOM_STEPS
	if total, glyphs, pages, runs, err := s.Store.DOMSizes(storageTopDOMRuns); err == nil {
		out["dom"] = map[string]any{"bytes": total, "glyph_bytes": glyphs, "pages": pages, "runs": runs}
	}
	writeJSON(w, http.StatusOK, out)
}

// storagePreview says what keeping only the last N days would delete and free
// (GET /api/v1/maintenance/storage/preview?days=N).
func (s *Server) storagePreview(w http.ResponseWriter, r *http.Request) {
	days, err := strconv.Atoi(r.URL.Query().Get("days"))
	if err != nil || days < 1 || days > maxRetentionDays {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("days must be between 1 and %d", maxRetentionDays))
		return
	}
	p, err := s.Store.PreviewPurgeBefore(time.Now().AddDate(0, 0, -days).UnixMilli())
	if err != nil {
		serverError(w, err)
		return
	}
	counts, err := s.Store.StorageCounts()
	if err != nil {
		serverError(w, err)
		return
	}
	sizes, err := s.fileSizes()
	if err != nil {
		serverError(w, err)
		return
	}
	files, n := sumURLs(sizes, p.URLs)
	var dbShare int64
	if counts.Tests > 0 {
		dbShare = counts.DBBytes * int64(p.Tests) / int64(counts.Tests)
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "runs": p.Runs, "tests": p.Tests, "files": n,
		"files_bytes": files, "db_bytes_approx": dbShare, "freed_bytes_approx": files + dbShare})
}

// setRetention changes the retention from the interface (0 = keep everything).
func (s *Server) setRetention(w http.ResponseWriter, r *http.Request) {
	if !s.guardSettingsWrite(w, r) {
		return
	}
	var in struct {
		Days *int `json:"days"`
	}
	if !decode(w, r, &in) {
		return
	}
	if in.Days == nil || *in.Days < 0 || *in.Days > maxRetentionDays {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("days must be between 0 (keep everything) and %d", maxRetentionDays))
		return
	}
	if _, source := s.RetentionDays(); source == "env" {
		writeError(w, http.StatusConflict, "set by TRACEREPORTS_RETENTION_DAYS: change it there")
		return
	}
	if err := s.Store.SaveSettings(map[string]string{settingRetentionDays: strconv.Itoa(*in.Days)}); err != nil {
		serverError(w, err)
		return
	}
	slog.Info("maintenance: retention changed", "days", *in.Days, "by", oneLine(s.uiUser(r)))
	days, source := s.RetentionDays()
	writeJSON(w, http.StatusOK, map[string]any{"days": days, "source": source, "editable": true})
}

// runRetention cleans up now (POST /ui/maintenance/retention/run): with the days given (the ones
// previewed) or the retention in force.
func (s *Server) runRetention(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		Days int `json:"days"`
	}
	if !decode(w, r, &in) {
		return
	}
	days := in.Days
	if days == 0 {
		days, _ = s.RetentionDays()
	}
	if days < 1 || days > maxRetentionDays {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("choose how many days to keep (1 to %d)", maxRetentionDays))
		return
	}
	run, err := s.PurgeOld(days, "manual", s.uiUser(r))
	if err == errCleanupRunning {
		writeError(w, http.StatusConflict, "a cleanup is already running")
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}
