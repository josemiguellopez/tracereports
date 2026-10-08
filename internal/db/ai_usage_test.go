package db

import (
	"testing"
	"time"
)

// Los totales de hoy, 7 y 30 días cuentan fechas locales con hoy incluido; se guardan 90 días.
func TestAIUsagePeriodsAndRetention(t *testing.T) {
	s := openTestStore(t)
	now := time.Date(2026, 10, 8, 15, 0, 0, 0, time.Local)
	add := func(daysAgo int, kind string, in, out int64, failed, known bool) {
		t.Helper()
		if err := s.AddAIUsage(AIUsageCall{At: now.AddDate(0, 0, -daysAgo), Provider: "gemini", Model: "g-1", Kind: kind,
			Failed: failed, InputTokens: in, OutputTokens: out, TokensKnown: known}); err != nil {
			t.Fatal(err)
		}
	}
	add(0, "triage", 100, 10, false, true)
	add(0, "triage", 50, 5, true, true)
	add(6, "run_summary", 30, 3, false, true) // borde de los 7 días
	add(7, "escalation", 20, 2, false, true)  // fuera de 7, dentro de 30
	add(29, "test", 0, 0, true, false)        // borde de los 30 días, sin tokens
	add(30, "triage", 999, 99, false, true)   // fuera de 30
	u, err := s.AIUsage(now)
	if err != nil {
		t.Fatal(err)
	}
	if u.Today != (AIUsageTotals{Calls: 2, Errors: 1, InputTokens: 150, OutputTokens: 15}) {
		t.Fatalf("today: %+v", u.Today)
	}
	if u.Last7 != (AIUsageTotals{Calls: 3, Errors: 1, InputTokens: 180, OutputTokens: 18}) {
		t.Fatalf("7 days: %+v", u.Last7)
	}
	if u.Last30 != (AIUsageTotals{Calls: 5, Errors: 2, InputTokens: 200, OutputTokens: 20, Untracked: 1}) {
		t.Fatalf("30 days: %+v", u.Last30)
	}
	if len(u.Daily) != 4 || u.Daily[0].Day != "2026-10-08" || u.Daily[0].Calls != 2 || u.Daily[3].Day != "2026-09-09" {
		t.Fatalf("daily, newest first: %+v", u.Daily)
	}
	if len(u.ByKind) != 4 || u.ByKind[0].Kind != "triage" || u.ByKind[0].Calls != 2 {
		t.Fatalf("by kind: %+v", u.ByKind)
	}
	// lo de hace más de 90 días se olvida al escribir
	add(91, "triage", 1, 1, false, true)
	add(0, "triage", 1, 1, false, true)
	var old int
	s.db.QueryRow(`SELECT COUNT(*) FROM ai_usage_daily WHERE day < '2026-07-10'`).Scan(&old)
	if old != 0 {
		t.Fatalf("rows older than 90 days kept: %d", old)
	}
}

// Varios modelos y proveedores: un total por modelo, ordenado por llamadas.
func TestAIUsageByModel(t *testing.T) {
	s := openTestStore(t)
	now := time.Now()
	for i, m := range []struct{ provider, model string }{{"openai", "gpt-a"}, {"gemini", "g-1"}, {"gemini", "g-1"}} {
		s.AddAIUsage(AIUsageCall{At: now, Provider: m.provider, Model: m.model, Kind: "triage", InputTokens: int64(i + 1), TokensKnown: true})
	}
	u, _ := s.AIUsage(now)
	if len(u.ByModel) != 2 || u.ByModel[0].Model != "g-1" || u.ByModel[0].Calls != 2 || u.ByModel[1].Model != "gpt-a" {
		t.Fatalf("by model: %+v", u.ByModel)
	}
}
