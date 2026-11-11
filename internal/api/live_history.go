package api

import (
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Live lists what runs now and what ended in the last hours (liveWindow). The history is everything
// before that, to pick an execution and see it in Live without knowing its execution_id:
//
//   - searched in the database (name, project, environment, branch, execution_id, a file of it) over
//     the whole retention, not only over what was loaded;
//   - by days (from/to) and in pages (before = the start of the last one shown), with the total;
//   - light: names, dates and counts per execution, counted in the database (no tests).
const (
	liveHistoryMaxDays = 90  // hasta dónde se busca
	liveHistoryPage    = 30  // por página
	liveHistoryMaxPage = 100 // ?limit= como mucho
	liveHistoryMaxRuns = 50000
)

// liveHistoryItem is a past execution: what the picker of Live needs to show and open it.
type liveHistoryItem struct {
	Key         string         `json:"key"`
	Title       string         `json:"title"`
	ExecutionID string         `json:"execution_id,omitempty"`
	Project     string         `json:"project,omitempty"`
	Environment string         `json:"environment,omitempty"`
	Branch      string         `json:"branch,omitempty"`
	Running     bool           `json:"running"`
	StartedAt   int64          `json:"started_at"`
	EndedAt     int64          `json:"ended_at,omitempty"`
	Planned     int            `json:"planned"`
	Total       int            `json:"total"`
	Done        int            `json:"done"`
	Runs        int            `json:"runs"`
	Counts      map[string]int `json:"counts"`
	Files       []string       `json:"files,omitempty"` // los nombres de sus runs (se ven al buscar por archivo)
}

// getLiveHistory lists past executions, newest first
// (GET /api/v1/live/history?q=&from=&to=&before=&limit=30): {now, total, next, executions}. next is the
// value of before for the next page (0: there are no more).
func (s *Server) getLiveHistory(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	now := db.NowMs()
	oldest := now - int64(liveHistoryMaxDays)*int64(24*time.Hour/time.Millisecond)
	from := max(queryMs(q.Get("from"), oldest), oldest)
	to := queryMs(q.Get("to"), now+1)
	before := queryMs(q.Get("before"), 0)
	limit := clampInt(q.Get("limit"), liveHistoryPage, 1, liveHistoryMaxPage)
	text := strings.TrimSpace(q.Get("q"))

	runs, err := s.Store.HistoryRuns(from, to, text, liveHistoryMaxRuns)
	if err != nil {
		serverError(w, err)
		return
	}
	// una búsqueda que encontró un archivo de una ejecución: la ejecución entera (sus contadores reales)
	if text != "" {
		var ids []string
		seen := map[string]bool{}
		for _, run := range runs {
			if run.ExecutionID != "" && !seen[run.ExecutionID] {
				seen[run.ExecutionID] = true
				ids = append(ids, run.ExecutionID)
			}
		}
		if len(ids) > 0 {
			all, err := s.Store.RunsOfExecutions(ids)
			if err != nil {
				serverError(w, err)
				return
			}
			runs = mergeRuns(runs, all)
		}
	}
	groups := db.GroupExecutions(runs, now)
	slices.SortStableFunc(groups, func(a, b db.Execution) int { // la más nueva primero
		if a.Runs[0].StartedAt != b.Runs[0].StartedAt {
			return int(b.Runs[0].StartedAt - a.Runs[0].StartedAt)
		}
		return strings.Compare(b.Key, a.Key)
	})
	total := len(groups)
	if before > 0 {
		i, _ := slices.BinarySearchFunc(groups, before, func(g db.Execution, t int64) int { // la primera que empezó antes
			if g.Runs[0].StartedAt >= t {
				return -1
			}
			return 1
		})
		groups = groups[i:]
	}
	page := groups[:min(limit, len(groups))]
	next := int64(0)
	if len(groups) > len(page) {
		next = page[len(page)-1].Runs[0].StartedAt
	}
	var ids []int64
	for _, g := range page {
		for _, run := range g.Runs {
			ids = append(ids, run.ID)
		}
	}
	counts, err := s.Store.RunCountsOf(ids)
	if err != nil {
		serverError(w, err)
		return
	}
	out := make([]liveHistoryItem, 0, len(page))
	for _, g := range page {
		out = append(out, historyItem(g, counts))
	}
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "total": total, "next": next, "executions": out})
}

// mergeRuns joins two lists of runs (oldest first) without repeating one.
func mergeRuns(a, b []db.LiveRun) []db.LiveRun {
	seen := make(map[int64]bool, len(a)+len(b))
	out := make([]db.LiveRun, 0, len(a)+len(b))
	for _, list := range [][]db.LiveRun{a, b} {
		for _, run := range list {
			if !seen[run.ID] {
				seen[run.ID] = true
				out = append(out, run)
			}
		}
	}
	slices.SortStableFunc(out, func(x, y db.LiveRun) int {
		if x.StartedAt != y.StartedAt {
			return int(x.StartedAt - y.StartedAt)
		}
		return int(x.ID - y.ID)
	})
	return out
}

func historyItem(g db.Execution, counts map[int64]db.RunCounts) liveHistoryItem {
	first := g.Runs[0]
	it := liveHistoryItem{Key: g.Key, ExecutionID: first.ExecutionID, Project: first.Project, Environment: first.Environment,
		Branch: first.Branch, StartedAt: first.StartedAt, Runs: len(g.Runs), Counts: map[string]int{}}
	seen := map[string]bool{}
	for _, r := range g.Runs {
		c := counts[r.ID]
		it.Planned += r.Planned
		it.Total += c.Total
		it.Done += c.Total - c.Running
		if c.Passed > 0 {
			it.Counts["PASS"] += c.Passed
		}
		if c.Failed > 0 {
			it.Counts["FAIL"] += c.Failed
		}
		if r.Status == "RUNNING" {
			it.Running = true
		}
		if r.EndedAt != nil {
			it.EndedAt = max(it.EndedAt, *r.EndedAt)
		}
		if name := strings.TrimSpace(r.Name); name != "" && !seen[name] && len(it.Files) < 50 {
			seen[name] = true
			it.Files = append(it.Files, name)
		}
	}
	if it.Running {
		it.EndedAt = 0
	}
	it.Title = execTitle(first.ExecutionID, first.Project, first.Branch, first.Commit, first.Name, len(g.Runs))
	return it
}

// queryMs reads a time (Unix ms) of a query, def when missing or invalid.
func queryMs(v string, def int64) int64 {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// clampInt reads a whole number of a query, def when missing or invalid, kept within [lo, hi].
func clampInt(v string, def, lo, hi int) int {
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return min(max(n, lo), hi)
}
