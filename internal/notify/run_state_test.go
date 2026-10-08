package notify

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// runInState creates a run in one of the states a notification can see.
func runInState(t *testing.T, s *db.Store, state string) int64 {
	t.Helper()
	run, _ := s.CreateRun("Suite "+state, "qa")
	add := func(status string) {
		id, _ := s.CreateTest(run, "t "+status, "", "")
		if status != "RUNNING" {
			s.FinishTest(id, status, "", "")
		}
	}
	switch state {
	case "open":
		add("PASS")
		add("RUNNING")
		return run // sin cerrar
	case "open-finished":
		add("PASS")
		return run // todos sus tests terminaron, pero la ejecución sigue abierta
	case "incomplete":
		add("PASS")
		s.FinishRunWith(run, true)
		return run
	case "warning":
		add("PASS")
		add("WARNING")
	case "skip":
		add("SKIP")
		add("SKIP")
	case "pass":
		add("PASS")
		add("SKIP")
	case "fail":
		add("PASS")
		add("FAIL")
	}
	s.FinishRun(run)
	return run
}

// La ausencia de fallos no hace verde una notificación: en curso, incompleta, con advertencias o
// todo omitido es una advertencia, con su motivo en el título y los contadores reales.
func TestNotificationFollowsTheRunState(t *testing.T) {
	s, err := db.Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n := &Notifier{store: s}
	for _, c := range []struct {
		state, icon, reason, stats string
		passed, warning            bool
	}{
		{"open", "⚠️", "en curso", "1 sin terminar", false, true},
		{"open-finished", "⚠️", "en curso", "1 OK", false, true},
		{"incomplete", "⚠️", "ejecución incompleta", "1 OK", false, true},
		{"warning", "⚠️", "con advertencias", "1 con advertencia", false, true},
		{"skip", "⚠️", "todos los tests omitidos", "2 omitidos", false, true},
		{"pass", "✅", "", "1 OK · 0 fallidos · 1 omitidos", true, false},
		{"fail", "❌", "", "1 fallidos", false, false},
	} {
		m, err := n.build(runInState(t, s, c.state))
		if err != nil || m == nil {
			t.Fatalf("%s: %v", c.state, err)
		}
		if m.Passed != c.passed || !strings.HasPrefix(m.Title, c.icon+" ") ||
			!strings.Contains(m.Title, c.reason) || !strings.Contains(m.Stats, c.stats) {
			t.Errorf("%s: passed=%v title=%q stats=%q", c.state, m.Passed, m.Title, m.Stats)
		}
		card := TeamsPayload(m)["attachments"].([]map[string]any)[0]["content"].(map[string]any)
		color := card["body"].([]map[string]any)[0]["color"]
		if want := map[bool]string{true: "Warning", false: map[bool]string{true: "Good", false: "Attention"}[c.passed]}[c.warning]; color != want {
			t.Errorf("%s: Teams color %v, want %s", c.state, color, want)
		}
	}
	// NOTIFY_ON=failures sigue avisando solo de fallos
	n.onlyFails = true
	if m, _ := n.build(runInState(t, s, "incomplete")); m != nil {
		t.Errorf("only failures: an incomplete run without failures is not sent: %+v", m)
	}
	if m, _ := n.build(runInState(t, s, "fail")); m == nil {
		t.Error("only failures: a failed run is sent")
	}
}
