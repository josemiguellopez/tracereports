package db

import (
	"fmt"
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

// eachPeriodTest calls fn for every finished test of the finished runs of the period (suite and
// environment filters applied in SQL), oldest first. Nothing is capped and nothing is kept: the
// caller aggregates as rows go by, so the metrics cover every result whatever the volume. fn must
// not query the store (the single connection is busy with the rows).
func (s *Store) eachPeriodTest(from, to int64, q MetricsQuery, fn func(*metricRow)) error {
	cond, args := runFilter(q)
	rows, err := s.db.Query(`SELECT t.id, t.run_id, t.name, t.category, t.status, t.started_at, t.ended_at, t.error_message,
			COALESCE((SELECT a.category FROM ai_triage a WHERE a.test_id = t.id AND a.state = 'DONE'), ''),
			t.test_key, r.environment, r.project, t.attempts, r.branch
		FROM tests t JOIN runs r ON r.id = t.run_id
		WHERE r.status != 'RUNNING' AND t.status != 'RUNNING' AND r.started_at >= ? AND r.started_at < ?`+cond+`
		ORDER BY r.started_at, t.id`, append([]any{from, to}, args...)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var m metricRow
		if err := rows.Scan(&m.testID, &m.runID, &m.name, &m.tags, &m.status, &m.started, &m.ended, &m.errMsg, &m.cause,
			&m.key, &m.env, &m.project, &m.attempts, &m.branch); err != nil {
			return err
		}
		fn(&m)
	}
	return rows.Err()
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

// Pass rate, the same in every comparable indicator (KPI, days, runs, tags, release gate):
// PASS over the tests that ran, that is everything but SKIP. A WARNING ran and did not pass.
type rate struct{ passed, failed, counted int }

func (r *rate) add(status string) {
	switch status {
	case "SKIP":
		return
	case "PASS":
		r.passed++
	case "FAIL":
		r.failed++
	}
	r.counted++
}

func (r rate) pct() float64 { return pct(r.passed, r.counted) }

// testAcc accumulates one test identity with bounded memory: counters, the last statuses the
// lists and the stability classification need, and duration sums.
type testAcc struct {
	stat      TestStat
	recent    []string // últimos 12 estados (todos)
	window    []string // últimos historyWindow estados PASS/WARNING/FAIL (Classify)
	lastPF    string   // último PASS o FAIL (cambios)
	retried   int
	durN      int
	durSum    int64
	durLast   []int64 // últimas 3 duraciones
	flipCount int
}

func (a *testAcc) add(t *metricRow) {
	st := t.status
	a.stat.Runs++
	a.recent = appendLast(a.recent, st, 12)
	if st == "PASS" || st == "WARNING" || st == "FAIL" {
		a.window = appendLast(a.window, st, max(historyWindow, persistentStreak))
	}
	if st == "PASS" || st == "FAIL" {
		if a.lastPF != "" && a.lastPF != st {
			a.flipCount++
		}
		a.lastPF = st
	}
	if st != "FAIL" && t.attempts > 1 {
		a.retried++
	}
	if d := t.duration(); d > 0 {
		a.durN++
		a.durSum += d
		if d > a.stat.MaxMs {
			a.stat.MaxMs = d
		}
		a.durLast = appendLast(a.durLast, d, 3)
	}
}

func (a *testAcc) flaky() bool {
	kind, _, _ := Classify(reversed(a.window), a.retried)
	return kind == StabilityFlaky
}

func appendLast[T any](s []T, v T, n int) []T {
	if len(s) == n {
		copy(s, s[1:])
		s[n-1] = v
		return s
	}
	return append(s, v)
}

type runCount struct {
	total, skipped int
	rate           rate
}

// periodAcc aggregates one period: KPIs, per-run counters and per-test identities.
type periodAcc struct {
	k      KPIs
	all    rate
	byRun  map[int64]*runCount
	tests  map[string]*testAcc
	order  []string
	detail bool // lista por test (solo el período actual)
}

func newPeriodAcc(detail bool) *periodAcc {
	return &periodAcc{byRun: map[int64]*runCount{}, tests: map[string]*testAcc{}, detail: detail}
}

func (p *periodAcc) add(t *metricRow) *testAcc {
	p.k.Tests++
	p.all.add(t.status)
	if t.status == "FAIL" {
		p.k.FailTimeMs += t.duration()
	}
	c := p.byRun[t.runID]
	if c == nil {
		c = &runCount{}
		p.byRun[t.runID] = c
	}
	c.total++
	if t.status == "SKIP" {
		c.skipped++
	}
	c.rate.add(t.status)
	id := t.identity()
	a := p.tests[id]
	if a == nil {
		a = &testAcc{stat: TestStat{Key: t.key, Env: t.env, Project: t.project, Branch: t.branch, ctx: id}}
		p.tests[id] = a
		p.order = append(p.order, id)
	}
	a.add(t)
	if p.detail {
		a.stat.Name = t.name // el nombre visible más reciente
		a.stat.LastRunID, a.stat.LastTestID = t.runID, t.testID
		if t.status == "FAIL" {
			a.stat.Fails++
			a.stat.FailTimeMs += t.duration()
			a.stat.LastError = firstLine(t.errMsg)
			a.stat.LastFailAt = t.started
		}
	}
	return a
}

// kpis closes the period: runs recounted from the (filtered) tests, so a tag filter changes the
// trend too; the runs left without tests are dropped when filtering by tag.
func (p *periodAcc) kpis(runs []RunPoint, keepEmpty bool) ([]RunPoint, KPIs) {
	out := []RunPoint{}
	for _, r := range runs {
		c := p.byRun[r.ID]
		if c == nil {
			if keepEmpty {
				out = append(out, r)
			}
			continue
		}
		r.Total, r.Passed, r.Failed = c.total, c.rate.passed, c.rate.failed
		r.PassRate = c.rate.pct()
		out = append(out, r)
	}
	k := p.k
	k.Runs = len(out)
	var runTime int64
	for _, r := range out {
		runTime += r.DurationMs
	}
	if len(out) > 0 {
		k.AvgRunMs = runTime / int64(len(out))
	}
	k.Passed, k.Failed, k.PassRate = p.all.passed, p.all.failed, p.all.pct()
	for _, id := range p.order {
		if p.tests[id].flaky() {
			k.FlakyTests++
		}
	}
	return out, k
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

// datesBetween returns the local calendar dates (2006-01-02) from a to b, both included. It
// steps by calendar day at noon: midnight does not exist on some daylight saving changes
// (Santiago jumps from 23:59 to 01:00), noon always does, so no date is skipped or repeated.
func datesBetween(a, b time.Time) []string {
	var out []string
	a, b = a.In(time.Local), b.In(time.Local)
	last := b.Format("2006-01-02")
	y, mo, d := a.Date()
	for day := time.Date(y, mo, d, 12, 0, 0, 0, time.Local); ; day = time.Date(y, mo, d+1, 12, 0, 0, 0, time.Local) {
		key := day.Format("2006-01-02")
		if key > last {
			return out
		}
		out = append(out, key)
		y, mo, d = day.Date()
	}
}

// metricsNow is the clock of the rolling period (tests fix it).
var metricsNow = time.Now

// Metrics computes the quality metrics of a period: the last q.Days days or the q.From–q.To
// range, optionally narrowed to one suite (run name), environment or tag. Runs still in
// progress are left out. Every result of the period counts (streamed, not loaded): only the
// lists (top failing, flaky, slowest, tags, last 60 runs) are cut.
func (s *Store) Metrics(q MetricsQuery) (*Metrics, error) {
	now := metricsNow()
	var from, to int64
	var end time.Time
	days := q.Days
	custom := q.From > 0 && q.To > q.From
	if custom {
		from, to, end = q.From, q.To, time.UnixMilli(q.To-1)
		// presupuesto antes de crear nada (metrics_budget.go)
		if days = dateCount(time.UnixMilli(from), end); days > MaxMetricsDays {
			return nil, fmt.Errorf("%w: %d days, at most %d", ErrMetricsRangeTooLong, days, MaxMetricsDays)
		}
	} else {
		if days <= 0 {
			days = 30
		}
		if days > MaxMetricsDays {
			return nil, fmt.Errorf("%w: %d days, at most %d", ErrMetricsRangeTooLong, days, MaxMetricsDays)
		}
		// +1: el límite es exclusivo y debe incluir lo creado en este mismo milisegundo
		to, end = now.UnixMilli()+1, now
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
	prevRuns, err := s.periodRuns(prevFrom, from, q)
	if err != nil {
		return nil, err
	}

	// por día (todos los días del período, aunque no haya ejecuciones)
	type dayAcc struct {
		runs int
		rate rate
	}
	// cada fecha que toca el período: uno móvil ("últimos 7 días" desde esta hora) empieza a
	// mitad de un día y toca N+1 fechas; el gráfico las muestra todas, así suma lo mismo que los KPIs
	byDay := map[string]*dayAcc{}
	dayKeys := datesBetween(time.UnixMilli(from), end)
	for _, key := range dayKeys {
		byDay[key] = &dayAcc{}
	}
	runDay := map[int64]string{}
	for _, r := range runs {
		runDay[r.ID] = time.UnixMilli(r.StartedAt).Format("2006-01-02")
	}

	cur := newPeriodAcc(true)
	tagCount := map[string]int{} // opciones de tag: las de todo el período (sin el filtro de tag)
	type tagAcc struct {
		tests int
		rate  rate
	}
	tags := map[string]*tagAcc{}
	var tagOrder []string
	causes := map[string]int{}
	err = s.eachPeriodTest(from, to, q, func(t *metricRow) {
		for _, tag := range strings.Split(t.tags, ",") {
			if tag = strings.TrimSpace(tag); tag != "" {
				tagCount[tag]++
			}
		}
		if q.Tag != "" && !hasTag(t.tags, q.Tag) {
			return
		}
		cur.add(t)
		if ds := byDay[runDay[t.runID]]; ds != nil {
			ds.rate.add(t.status)
		}
		if t.status == "FAIL" {
			causes[t.cause]++
		}
		for _, tag := range strings.Split(t.tags, ",") {
			if tag = strings.TrimSpace(tag); tag == "" {
				continue
			}
			ts := tags[tag]
			if ts == nil {
				ts = &tagAcc{}
				tags[tag] = ts
				tagOrder = append(tagOrder, tag)
			}
			ts.tests++
			ts.rate.add(t.status)
		}
	})
	if err != nil {
		return nil, err
	}
	prev := newPeriodAcc(false)
	err = s.eachPeriodTest(prevFrom, from, q, func(t *metricRow) {
		if q.Tag == "" || hasTag(t.tags, q.Tag) {
			prev.add(t)
		}
	})
	if err != nil {
		return nil, err
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

	runs, m.Current = cur.kpis(runs, q.Tag == "")
	_, m.Previous = prev.kpis(prevRuns, q.Tag == "")

	// últimas 60 ejecuciones para la tendencia
	if len(runs) > 60 {
		m.Runs = runs[len(runs)-60:]
	} else {
		m.Runs = runs
	}
	for _, r := range runs {
		if ds := byDay[runDay[r.ID]]; ds != nil {
			ds.runs++
		}
	}
	for _, key := range dayKeys {
		ds := byDay[key]
		m.Daily = append(m.Daily, DayStat{Day: key, Runs: ds.runs, Passed: ds.rate.passed, Failed: ds.rate.failed, PassRate: ds.rate.pct()})
	}

	// por test
	var all []TestStat
	flakyIDs := map[string]bool{}
	for _, id := range cur.order {
		a := cur.tests[id]
		if a.flaky() {
			flakyIDs[a.stat.ctx] = true
		}
		st := a.stat
		st.FailRate = pct(st.Fails, st.Runs)
		st.Flips = a.flipCount
		st.Recent = a.recent
		if a.durN > 0 {
			st.AvgMs = a.durSum / int64(a.durN)
			if a.durN >= 6 {
				var last int64
				for _, d := range a.durLast {
					last += d
				}
				recent, before := last/3, (a.durSum-last)/int64(a.durN-3)
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
		m.Tags = append(m.Tags, TagStat{Tag: tag, Tests: ts.tests, Passed: ts.rate.passed, Failed: ts.rate.failed, PassRate: ts.rate.pct()})
	}
	sort.SliceStable(m.Tags, func(i, j int) bool { return m.Tags[i].PassRate < m.Tags[j].PassRate })
	if len(m.Tags) > 15 {
		m.Tags = m.Tags[:15]
	}
	return m, nil
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
