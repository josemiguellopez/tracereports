package db

import (
	"strings"
	"time"
)

// Uso del proveedor de IA (Ajustes → Uso de la IA): cuántas llamadas se hicieron y cuántos tokens
// informó el proveedor, por día, proveedor, modelo y tipo de análisis. Una fila por combinación
// y día, así que la tabla es chica; se guardan aiUsageDays días. No hay costos: los precios
// cambian y la factura es la del proveedor.
const aiUsageSchema = `
CREATE TABLE IF NOT EXISTS ai_usage_daily (
	day           TEXT    NOT NULL,            -- fecha local 2006-01-02
	provider      TEXT    NOT NULL,
	model         TEXT    NOT NULL,
	kind          TEXT    NOT NULL,            -- triage | run_summary | escalation | test
	calls         INTEGER NOT NULL DEFAULT 0,
	errors        INTEGER NOT NULL DEFAULT 0,
	input_tokens  INTEGER NOT NULL DEFAULT 0,
	output_tokens INTEGER NOT NULL DEFAULT 0,
	untracked     INTEGER NOT NULL DEFAULT 0,  -- llamadas cuya respuesta no informó tokens
	duration_ms   INTEGER NOT NULL DEFAULT 0,  -- tiempo total esperando al proveedor
	timed         INTEGER NOT NULL DEFAULT 0,  -- llamadas con duración medida (las anteriores no la tienen)
	err_rate_limit   INTEGER NOT NULL DEFAULT 0,
	err_server       INTEGER NOT NULL DEFAULT 0,
	err_auth         INTEGER NOT NULL DEFAULT 0,
	err_timeout      INTEGER NOT NULL DEFAULT 0,
	err_network      INTEGER NOT NULL DEFAULT 0,
	err_other        INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (day, provider, model, kind)
);
`

// aiUsageDays is how many days of AI usage are kept.
const aiUsageDays = 90

// AIUsageCall is one call to the AI provider.
type AIUsageCall struct {
	At           time.Time
	Provider     string
	Model        string
	Kind         string
	Failed       bool
	InputTokens  int64
	OutputTokens int64
	TokensKnown  bool          // la respuesta informó tokens (un error de conexión o algunas APIs compatibles no)
	Duration     time.Duration // cuánto tardó el proveedor en responder (o en fallar)
	Reason       string        // por qué falló (ai.FailureReason): rate_limit | server | auth | timeout | network | other
}

// aiErrorReasons are the kinds of failed calls counted apart (columns err_<reason>).
var aiErrorReasons = []string{"rate_limit", "server", "auth", "timeout", "network", "other"}

// AddAIUsage counts one call to the AI provider and forgets the days past aiUsageDays.
func (s *Store) AddAIUsage(c AIUsageCall) error {
	day := c.At.In(time.Local).Format("2006-01-02")
	failed, untracked := 0, 0
	if c.Failed {
		failed = 1
	}
	byReason := map[string]int{}
	if c.Failed {
		reason := c.Reason
		if reason == "" || !contains(aiErrorReasons, reason) {
			reason = "other"
		}
		byReason[reason] = 1
	}
	if !c.TokensKnown {
		untracked = 1
	}
	if _, err := s.db.Exec(`INSERT INTO ai_usage_daily(day, provider, model, kind, calls, errors, input_tokens, output_tokens, untracked, duration_ms, timed,
			err_rate_limit, err_server, err_auth, err_timeout, err_network, err_other)
		VALUES(?, ?, ?, ?, 1, ?, ?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(day, provider, model, kind) DO UPDATE SET calls = calls + 1, errors = errors + excluded.errors,
			input_tokens = input_tokens + excluded.input_tokens, output_tokens = output_tokens + excluded.output_tokens,
			untracked = untracked + excluded.untracked, duration_ms = duration_ms + excluded.duration_ms, timed = timed + 1,
			err_rate_limit = err_rate_limit + excluded.err_rate_limit, err_server = err_server + excluded.err_server, err_auth = err_auth + excluded.err_auth, err_timeout = err_timeout + excluded.err_timeout, err_network = err_network + excluded.err_network, err_other = err_other + excluded.err_other`,
		day, c.Provider, c.Model, c.Kind, failed, c.InputTokens, c.OutputTokens, untracked, c.Duration.Milliseconds(),
		byReason["rate_limit"], byReason["server"], byReason["auth"], byReason["timeout"], byReason["network"], byReason["other"]); err != nil {
		return err
	}
	cutoff := c.At.In(time.Local).AddDate(0, 0, -aiUsageDays).Format("2006-01-02")
	_, err := s.db.Exec(`DELETE FROM ai_usage_daily WHERE day < ?`, cutoff)
	return err
}

// AIUsageTotals adds up the calls of a period.
type AIUsageTotals struct {
	Calls        int64 `json:"calls"`
	Errors       int64 `json:"errors"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Untracked    int64 `json:"untracked"`   // llamadas sin datos de tokens
	DurationMs   int64 `json:"duration_ms"` // tiempo total esperando al proveedor
	Timed        int64 `json:"timed"`       // llamadas con duración medida
	AvgMs        int64 `json:"avg_ms"`      // respuesta media por llamada medida (0 si ninguna lo fue)
	// ErrorsBy counts failed calls by reason: rate_limit (429), server (5xx), auth (401/403),
	// timeout, network, other.
	ErrorsBy map[string]int64 `json:"errors_by"`
}

// average is the mean response over the calls whose duration was measured: calls recorded by
// an older version have none and must not drag it towards zero.
func (t *AIUsageTotals) average() {
	if t.Timed > 0 {
		t.AvgMs = t.DurationMs / t.Timed
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

// scanErrors appends the destinations of the err_<reason> columns, in aiErrorReasons order.
func (t *AIUsageTotals) scanErrors() []any {
	vals := make([]any, len(aiErrorReasons))
	t.ErrorsBy = map[string]int64{}
	for i := range aiErrorReasons {
		vals[i] = new(int64)
	}
	return vals
}

func (t *AIUsageTotals) setErrors(vals []any) {
	for i, r := range aiErrorReasons {
		if n := *vals[i].(*int64); n > 0 {
			t.ErrorsBy[r] = n
		}
	}
}

// AIUsageRow is a group of calls (by model, by kind or by day and model).
type AIUsageRow struct {
	Day      string `json:"day,omitempty"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Kind     string `json:"kind,omitempty"`
	AIUsageTotals
}

// AIUsageReport is what the Settings screen shows.
type AIUsageReport struct {
	Today   AIUsageTotals `json:"today"`
	Last7   AIUsageTotals `json:"last_7"`
	Last30  AIUsageTotals `json:"last_30"`
	ByModel []AIUsageRow  `json:"by_model"` // últimos 30 días
	ByKind  []AIUsageRow  `json:"by_kind"`  // últimos 30 días
	Daily   []AIUsageRow  `json:"daily"`    // últimos 30 días, por día y modelo, del más reciente al más antiguo
	// tests que quedaron sin diagnóstico automático por el límite por ejecución (últimos 30 días)
	SkippedByBudget int64 `json:"skipped_by_budget"`
}

// AIUsage reports the AI usage of today, the last 7 and the last 30 days (local dates, today
// included) as of now.
func (s *Store) AIUsage(now time.Time) (*AIUsageReport, error) {
	now = now.In(time.Local)
	today := now.Format("2006-01-02")
	from7 := now.AddDate(0, 0, -6).Format("2006-01-02")
	from30 := now.AddDate(0, 0, -29).Format("2006-01-02")
	r := &AIUsageReport{ByModel: []AIUsageRow{}, ByKind: []AIUsageRow{}, Daily: []AIUsageRow{}}
	totals := func(from string, t *AIUsageTotals) error {
		errs := t.scanErrors()
		err := s.db.QueryRow(`SELECT COALESCE(SUM(calls), 0), COALESCE(SUM(errors), 0), COALESCE(SUM(input_tokens), 0),
			COALESCE(SUM(output_tokens), 0), COALESCE(SUM(untracked), 0), COALESCE(SUM(duration_ms), 0), COALESCE(SUM(timed), 0),
			COALESCE(SUM(err_rate_limit), 0), COALESCE(SUM(err_server), 0), COALESCE(SUM(err_auth), 0), COALESCE(SUM(err_timeout), 0), COALESCE(SUM(err_network), 0), COALESCE(SUM(err_other), 0)
			FROM ai_usage_daily WHERE day >= ? AND day <= ?`, from, today).
			Scan(append([]any{&t.Calls, &t.Errors, &t.InputTokens, &t.OutputTokens, &t.Untracked, &t.DurationMs, &t.Timed}, errs...)...)
		t.setErrors(errs)
		t.average()
		return err
	}
	for _, p := range []struct {
		from string
		t    *AIUsageTotals
	}{{today, &r.Today}, {from7, &r.Last7}, {from30, &r.Last30}} {
		if err := totals(p.from, p.t); err != nil {
			return nil, err
		}
	}
	rows := func(cols, group, order string, out *[]AIUsageRow) error {
		q, err := s.db.Query(`SELECT `+cols+`, SUM(calls), SUM(errors), SUM(input_tokens), SUM(output_tokens), SUM(untracked), SUM(duration_ms),
			SUM(timed), SUM(err_rate_limit), SUM(err_server), SUM(err_auth), SUM(err_timeout), SUM(err_network), SUM(err_other)
			FROM ai_usage_daily WHERE day >= ? AND day <= ? GROUP BY `+group+` ORDER BY `+order, from30, today)
		if err != nil {
			return err
		}
		defer q.Close()
		for q.Next() {
			var row AIUsageRow
			dest := map[string]any{"day": &row.Day, "provider": &row.Provider, "model": &row.Model, "kind": &row.Kind}
			var ptrs []any
			for _, c := range splitCols(cols) {
				ptrs = append(ptrs, dest[c])
			}
			errs := row.scanErrors()
			ptrs = append(ptrs, &row.Calls, &row.Errors, &row.InputTokens, &row.OutputTokens, &row.Untracked, &row.DurationMs, &row.Timed)
			if err := q.Scan(append(ptrs, errs...)...); err != nil {
				return err
			}
			row.setErrors(errs)
			row.average()
			*out = append(*out, row)
		}
		return q.Err()
	}
	if err := rows("provider, model", "provider, model", "SUM(calls) DESC, provider, model", &r.ByModel); err != nil {
		return nil, err
	}
	if err := rows("kind", "kind", "SUM(calls) DESC, kind", &r.ByKind); err != nil {
		return nil, err
	}
	if err := rows("day, provider, model", "day, provider, model", "day DESC, SUM(calls) DESC, provider, model", &r.Daily); err != nil {
		return nil, err
	}
	start30 := time.Date(now.Year(), now.Month(), now.Day()-29, 0, 0, 0, 0, time.Local).UnixMilli()
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM ai_triage WHERE state = 'SKIPPED' AND updated_at >= ?`, start30).
		Scan(&r.SkippedByBudget); err != nil {
		return nil, err
	}
	return r, nil
}

// splitCols lists the columns of a "a, b, c" list, in order.
func splitCols(cols string) []string {
	parts := strings.Split(cols, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}
