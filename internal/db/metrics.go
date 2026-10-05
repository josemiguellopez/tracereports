package db

import (
	"sort"
	"strings"
	"time"
)

// Metrics is the quality trend across runs over a period (the "Métricas" screen).
type Metrics struct {
	Days   int      `json:"days"`
	Suite  string   `json:"suite"`
	Env    string   `json:"env"`
	Tag    string   `json:"tag"`
	Custom bool     `json:"custom"` // rango elegido por fechas (no "últimos N días")
	From   int64    `json:"from"`
	To     int64    `json:"to"`
	Suites []string `json:"suites"` // opciones de los filtros (todo el período, sin filtrar)
	Envs   []string `json:"envs"`
	TagSet []string `json:"tag_set"`

	Current  KPIs `json:"current"`
	Previous KPIs `json:"previous"` // mismo largo, justo antes: para mostrar la variación

	Daily      []DayStat   `json:"daily"`
	Runs       []RunPoint  `json:"runs"`        // últimas ejecuciones, de la más antigua a la más reciente
	TopFailing []TestStat  `json:"top_failing"` // tests que más fallan (y cuánto tiempo se fue en ellos)
	Flaky      []TestStat  `json:"flaky"`
	Slowest    []TestStat  `json:"slowest"`
	Causes     []CauseStat `json:"causes"` // causas según la IA de los tests fallidos
	Tags       []TagStat   `json:"tags"`
}

// KPIs summarizes a period.
type KPIs struct {
	Runs       int     `json:"runs"`
	Tests      int     `json:"tests"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	PassRate   float64 `json:"pass_rate"` // % sobre los tests no omitidos
	AvgRunMs   int64   `json:"avg_run_ms"`
	FailTimeMs int64   `json:"fail_time_ms"` // tiempo de ejecución consumido por tests que fallaron
	FlakyTests int     `json:"flaky_tests"`
}

// DayStat is one day of the period.
type DayStat struct {
	Day      string  `json:"day"` // 2006-01-02 (hora del servidor)
	Runs     int     `json:"runs"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	PassRate float64 `json:"pass_rate"`
}

// RunPoint is one run in the trend.
type RunPoint struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	StartedAt  int64   `json:"started_at"`
	Total      int     `json:"total"`
	Passed     int     `json:"passed"`
	Failed     int     `json:"failed"`
	PassRate   float64 `json:"pass_rate"`
	DurationMs int64   `json:"duration_ms"`
}

// TestStat aggregates one test (same identity and environment) over the period.
type TestStat struct {
	Name    string `json:"name"`
	Key     string `json:"key"`
	Env     string `json:"env"`
	Project string `json:"project,omitempty"`
	Branch  string `json:"branch,omitempty"`
	// ctx is the full identity (project, environment, branch, key): rows and the flaky list use it,
	// so a homonymous test of another project or branch never borrows another's history.
	ctx        string
	Runs       int      `json:"runs"`
	Fails      int      `json:"fails"`
	FailRate   float64  `json:"fail_rate"`
	Flips      int      `json:"flips"`
	AvgMs      int64    `json:"avg_ms"`
	MaxMs      int64    `json:"max_ms"`
	TrendPct   float64  `json:"trend_pct"` // duración: últimas 3 ejecuciones vs las anteriores
	FailTimeMs int64    `json:"fail_time_ms"`
	Recent     []string `json:"recent"` // últimos estados, del más antiguo al más reciente
	LastError  string   `json:"last_error,omitempty"`
	LastRunID  int64    `json:"last_run_id"`
	LastTestID int64    `json:"last_test_id"`
	LastFailAt int64    `json:"last_fail_at,omitempty"`
}

// CauseStat counts failures per AI category ("" = sin diagnóstico).
type CauseStat struct {
	Category string `json:"category"`
	Count    int    `json:"count"`
}

// TagStat is the stability of one tag/category.
type TagStat struct {
	Tag      string  `json:"tag"`
	Tests    int     `json:"tests"`
	Passed   int     `json:"passed"`
	Failed   int     `json:"failed"`
	PassRate float64 `json:"pass_rate"`
}

type metricRow struct {
	testID, runID      int64
	name, tags, status string
	key, env, project  string
	branch             string
	attempts           int
	started            int64
	ended              *int64
	errMsg, cause      string
}

func pct(a, b int) float64 {
	if b == 0 {
		return 0
	}
	return float64(int(float64(a)*1000/float64(b)+0.5)) / 10
}

// MetricsQuery selects the period and the slice of data the metrics cover.
type MetricsQuery struct {
	Days     int   // últimos N días (si From/To no vienen)
	From, To int64 // rango propio en ms (opcional)
	Suite    string
	Env      string
	Tag      string
}

// runFilter returns the SQL condition for the optional suite (run name) and environment filters.
func runFilter(q MetricsQuery) (string, []any) {
	cond, args := "", []any{}
	if q.Suite != "" {
		cond += " AND r.name = ?"
		args = append(args, q.Suite)
	}
	if q.Env != "" {
		cond += " AND r.environment = ?"
		args = append(args, q.Env)
	}
	return cond, args
}

func hasTag(tags, tag string) bool {
	for _, t := range strings.Split(tags, ",") {
		if strings.EqualFold(strings.TrimSpace(t), tag) {
			return true
		}
	}
	return false
}

func (s *Store) periodRuns(from, to int64, q MetricsQuery) ([]RunPoint, error) {
	cond, args := runFilter(q)
	rows, err := s.db.Query(`SELECT r.id, r.name, r.started_at, COALESCE(r.ended_at, r.started_at), r.total, r.passed, r.failed, r.skipped
		FROM runs r WHERE r.status != 'RUNNING' AND r.started_at >= ? AND r.started_at < ?`+cond+` ORDER BY r.started_at`,
		append([]any{from, to}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RunPoint
	for rows.Next() {
		var p RunPoint
		var ended int64
		var skipped int
		if err := rows.Scan(&p.ID, &p.Name, &p.StartedAt, &ended, &p.Total, &p.Passed, &p.Failed, &skipped); err != nil {
			return nil, err
		}
		p.DurationMs = ended - p.StartedAt
		p.PassRate = pct(p.Passed, p.Total-skipped)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) periodTests(from, to int64, q MetricsQuery) ([]metricRow, error) {
	cond, args := runFilter(q)
	rows, err := s.db.Query(`SELECT t.id, t.run_id, t.name, t.category, t.status, t.started_at, t.ended_at, t.error_message,
			COALESCE((SELECT a.category FROM ai_triage a WHERE a.test_id = t.id AND a.state = 'DONE'), ''),
			t.test_key, r.environment, r.project, t.attempts, r.branch
		FROM tests t JOIN runs r ON r.id = t.run_id
		WHERE r.status != 'RUNNING' AND t.status != 'RUNNING' AND r.started_at >= ? AND r.started_at < ?`+cond+`
		ORDER BY r.started_at, t.id LIMIT 100000`, append([]any{from, to}, args...)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []metricRow
	for rows.Next() {
		var m metricRow
		if err := rows.Scan(&m.testID, &m.runID, &m.name, &m.tags, &m.status, &m.started, &m.ended, &m.errMsg, &m.cause,
			&m.key, &m.env, &m.project, &m.attempts, &m.branch); err != nil {
			return nil, err
		}
		if q.Tag != "" && !hasTag(m.tags, q.Tag) {
			continue
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// identity groups the executions of the same test: identity plus its context (project,
// environment and branch, like the history), so staging never mixes with production nor a
// feature branch with main.
func (m metricRow) identity() string {
	return m.project + "\x1f" + m.env + "\x1f" + m.branch + "\x1f" + m.key
}

func (m metricRow) duration() int64 {
	if m.ended == nil {
		return 0
	}
	return *m.ended - m.started
}

func kpis(runs []RunPoint, tests []metricRow) KPIs {
	k := KPIs{Runs: len(runs), Tests: len(tests)}
	counted := 0
	var runTime int64
	for _, r := range runs {
		runTime += r.DurationMs
	}
	if len(runs) > 0 {
		k.AvgRunMs = runTime / int64(len(runs))
	}
	statuses := map[string][]string{}
	retried := map[string]int{}
	for _, t := range tests {
		switch t.status {
		case "PASS":
			k.Passed++
			counted++
		case "FAIL":
			k.Failed++
			counted++
			k.FailTimeMs += t.duration()
		case "WARNING":
			counted++
		}
		statuses[t.identity()] = append(statuses[t.identity()], t.status)
		if t.status != "FAIL" && t.attempts > 1 {
			retried[t.identity()]++
		}
	}
	k.PassRate = pct(k.Passed, counted)
	for id, st := range statuses {
		if kind, _, _ := Classify(reversed(st), retried[id]); kind == StabilityFlaky {
			k.FlakyTests++
		}
	}
	return k
}

// distinct returns the most frequent values of a runs column in the period (filter options).
func (s *Store) distinct(column string, from, to int64) ([]string, error) {
	rows, err := s.db.Query(`SELECT `+column+` FROM runs WHERE started_at >= ? AND started_at < ? AND `+column+` != ''
		GROUP BY `+column+` ORDER BY COUNT(*) DESC, `+column+` LIMIT 100`, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// withTestCounts recomputes each run's counters from the (filtered) tests, so a tag filter
// changes the trend too, and drops the runs left without tests.
func withTestCounts(runs []RunPoint, tests []metricRow, keepEmpty bool) []RunPoint {
	type cnt struct{ total, passed, failed, skipped int }
	byRun := map[int64]*cnt{}
	for _, t := range tests {
		c := byRun[t.runID]
		if c == nil {
			c = &cnt{}
			byRun[t.runID] = c
		}
		c.total++
		switch t.status {
		case "PASS":
			c.passed++
		case "FAIL":
			c.failed++
		case "SKIP":
			c.skipped++
		}
	}
	out := []RunPoint{}
	for _, r := range runs {
		c := byRun[r.ID]
		if c == nil {
			if keepEmpty {
				out = append(out, r)
			}
			continue
		}
		r.Total, r.Passed, r.Failed = c.total, c.passed, c.failed
		r.PassRate = pct(c.passed, c.total-c.skipped)
		out = append(out, r)
	}
	return out
}

// Metrics computes the quality metrics of a period: the last q.Days days or the q.From–q.To
// range, optionally narrowed to one suite (run name), environment or tag. Runs still in
// progress are left out.
func (s *Store) Metrics(q MetricsQuery) (*Metrics, error) {
	now := time.Now()
	var from, to int64
	var end time.Time
	days := q.Days
	custom := q.From > 0 && q.To > q.From
	if custom {
		from, to, end = q.From, q.To, time.UnixMilli(q.To-1)
		days = int((to - from + 86400000 - 1) / 86400000)
		if days > 366 {
			days = 366
		}
	} else {
		if days <= 0 {
			days = 30
		}
		to, end = now.UnixMilli(), now
		from = now.AddDate(0, 0, -days).UnixMilli()
	}
	prevFrom := from - (to - from)

	m := &Metrics{Days: days, Suite: q.Suite, Env: q.Env, Tag: q.Tag, Custom: custom, From: from, To: to,
		Daily: []DayStat{}, Runs: []RunPoint{}, TopFailing: []TestStat{}, Flaky: []TestStat{}, Slowest: []TestStat{},
		Causes: []CauseStat{}, Tags: []TagStat{}}
	var err error
	if m.Suites, err = s.distinct("name", from, to); err != nil {
		return nil, err
	}
	if m.Envs, err = s.distinct("environment", from, to); err != nil {
		return nil, err
	}

	runs, err := s.periodRuns(from, to, q)
	if err != nil {
		return nil, err
	}
	tests, err := s.periodTests(from, to, q)
	if err != nil {
		return nil, err
	}
	prevRuns, err := s.periodRuns(prevFrom, from, q)
	if err != nil {
		return nil, err
	}
	prevTests, err := s.periodTests(prevFrom, from, q)
	if err != nil {
		return nil, err
	}
	// opciones de tag: las de todo el período (sin el filtro de tag)
	tagCount := map[string]int{}
	allTests := tests
	if q.Tag != "" {
		if allTests, err = s.periodTests(from, to, MetricsQuery{Suite: q.Suite, Env: q.Env}); err != nil {
			return nil, err
		}
	}
	for _, t := range allTests {
		for _, tag := range strings.Split(t.tags, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				tagCount[tag]++
			}
		}
	}
	m.TagSet = []string{}
	for tag := range tagCount {
		m.TagSet = append(m.TagSet, tag)
	}
	sort.Slice(m.TagSet, func(i, j int) bool {
		if tagCount[m.TagSet[i]] != tagCount[m.TagSet[j]] {
			return tagCount[m.TagSet[i]] > tagCount[m.TagSet[j]]
		}
		return m.TagSet[i] < m.TagSet[j]
	})
	if len(m.TagSet) > 60 {
		m.TagSet = m.TagSet[:60]
	}

	runs = withTestCounts(runs, tests, q.Tag == "")
	prevRuns = withTestCounts(prevRuns, prevTests, q.Tag == "")
	m.Current, m.Previous = kpis(runs, tests), kpis(prevRuns, prevTests)

	// últimas 60 ejecuciones para la tendencia
	if len(runs) > 60 {
		m.Runs = runs[len(runs)-60:]
	} else if runs != nil {
		m.Runs = runs
	}

	// por día (todos los días del período, aunque no haya ejecuciones)
	byDay := map[string]*DayStat{}
	dayKeys := make([]string, days)
	for d := range dayKeys {
		dayKeys[d] = end.AddDate(0, 0, -days+1+d).Format("2006-01-02")
		byDay[dayKeys[d]] = &DayStat{Day: dayKeys[d]}
	}
	runDay := map[int64]string{}
	for _, r := range runs {
		day := time.UnixMilli(r.StartedAt).Format("2006-01-02")
		runDay[r.ID] = day
		if ds := byDay[day]; ds != nil {
			ds.Runs++
		}
	}
	for _, t := range tests {
		if ds := byDay[runDay[t.runID]]; ds != nil {
			switch t.status {
			case "PASS":
				ds.Passed++
			case "FAIL":
				ds.Failed++
			}
		}
	}
	for _, key := range dayKeys {
		ds := byDay[key]
		ds.PassRate = pct(ds.Passed, ds.Passed+ds.Failed)
		m.Daily = append(m.Daily, *ds)
	}

	// por test
	type acc struct {
		stat      TestStat
		statuses  []string
		retried   int
		durations []int64
	}
	byName := map[string]*acc{}
	var order []string
	causes := map[string]int{}
	tags := map[string]*TagStat{}
	var tagOrder []string
	for _, t := range tests {
		a := byName[t.identity()]
		if a == nil {
			a = &acc{stat: TestStat{Name: t.name, Key: t.key, Env: t.env, Project: t.project, Branch: t.branch, ctx: t.identity()}}
			byName[t.identity()] = a
			order = append(order, t.identity())
		}
		a.stat.Name = t.name // el nombre visible más reciente
		a.stat.Runs++
		a.statuses = append(a.statuses, t.status)
		if t.status != "FAIL" && t.attempts > 1 {
			a.retried++
		}
		if d := t.duration(); d > 0 {
			a.durations = append(a.durations, d)
		}
		a.stat.LastRunID, a.stat.LastTestID = t.runID, t.testID
		if t.status == "FAIL" {
			a.stat.Fails++
			a.stat.FailTimeMs += t.duration()
			a.stat.LastError = firstLine(t.errMsg)
			a.stat.LastFailAt = t.started
			causes[t.cause]++
		}
		for _, tag := range strings.Split(t.tags, ",") {
			tag = strings.TrimSpace(tag)
			if tag == "" {
				continue
			}
			ts := tags[tag]
			if ts == nil {
				ts = &TagStat{Tag: tag}
				tags[tag] = ts
				tagOrder = append(tagOrder, tag)
			}
			ts.Tests++
			switch t.status {
			case "PASS":
				ts.Passed++
			case "FAIL":
				ts.Failed++
			}
		}
	}
	var all []TestStat
	flakyIDs := map[string]bool{}
	for _, id := range order {
		a := byName[id]
		if kind, _, _ := Classify(reversed(a.statuses), a.retried); kind == StabilityFlaky {
			flakyIDs[a.stat.ctx] = true
		}
		st := a.stat
		st.FailRate = pct(st.Fails, st.Runs)
		st.Flips = flips(a.statuses)
		st.Recent = a.statuses
		if len(st.Recent) > 12 {
			st.Recent = st.Recent[len(st.Recent)-12:]
		}
		if n := len(a.durations); n > 0 {
			var sum int64
			for _, d := range a.durations {
				sum += d
				if d > st.MaxMs {
					st.MaxMs = d
				}
			}
			st.AvgMs = sum / int64(n)
			if n >= 6 {
				recent, before := avg(a.durations[n-3:]), avg(a.durations[:n-3])
				if before > 0 {
					st.TrendPct = float64(int((float64(recent-before)/float64(before))*1000)) / 10
				}
			}
		}
		all = append(all, st)
	}

	pick := func(keep func(TestStat) bool, less func(a, b TestStat) bool, n int) []TestStat {
		out := []TestStat{}
		for _, st := range all {
			if keep(st) {
				out = append(out, st)
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) })
		if len(out) > n {
			out = out[:n]
		}
		return out
	}
	m.TopFailing = pick(func(t TestStat) bool { return t.Fails > 0 },
		func(a, b TestStat) bool {
			if a.Fails != b.Fails {
				return a.Fails > b.Fails
			}
			return a.FailTimeMs > b.FailTimeMs
		}, 10)
	m.Flaky = pick(func(t TestStat) bool { return flakyIDs[t.ctx] },
		func(a, b TestStat) bool { return a.Flips > b.Flips || (a.Flips == b.Flips && a.FailRate > b.FailRate) }, 10)
	m.Slowest = pick(func(t TestStat) bool { return t.AvgMs > 0 },
		func(a, b TestStat) bool { return a.AvgMs > b.AvgMs }, 10)

	for c, n := range causes {
		m.Causes = append(m.Causes, CauseStat{Category: c, Count: n})
	}
	sort.Slice(m.Causes, func(i, j int) bool {
		if m.Causes[i].Count != m.Causes[j].Count {
			return m.Causes[i].Count > m.Causes[j].Count
		}
		return m.Causes[i].Category < m.Causes[j].Category
	})
	for _, tag := range tagOrder {
		ts := tags[tag]
		ts.PassRate = pct(ts.Passed, ts.Passed+ts.Failed)
		m.Tags = append(m.Tags, *ts)
	}
	sort.SliceStable(m.Tags, func(i, j int) bool { return m.Tags[i].PassRate < m.Tags[j].PassRate })
	if len(m.Tags) > 15 {
		m.Tags = m.Tags[:15]
	}
	return m, nil
}

// flips counts PASS<->FAIL changes, ignoring skipped/other states.
func flips(statuses []string) int {
	n, prev := 0, ""
	for _, st := range statuses {
		if st != "PASS" && st != "FAIL" {
			continue
		}
		if prev != "" && st != prev {
			n++
		}
		prev = st
	}
	return n
}

func avg(ds []int64) int64 {
	if len(ds) == 0 {
		return 0
	}
	var s int64
	for _, d := range ds {
		s += d
	}
	return s / int64(len(ds))
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// reversed returns a copy of statuses in the opposite order (Classify reads newest first).
func reversed(statuses []string) []string {
	out := make([]string, len(statuses))
	for i, st := range statuses {
		out[len(statuses)-1-i] = st
	}
	return out
}
