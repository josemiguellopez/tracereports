package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// Cerrar a mano una ejecución colgada: los tests sin terminar quedan INTERRUPTED con el motivo y
// quién la cerró; no cuentan como fallo y la ejecución queda incompleta, en WARNING.
func TestCloseRunByHand(t *testing.T) {
	s := openTestStore(t)
	rid, _ := s.CreateRun("colgada", "qa")
	ok, _ := s.CreateTest(rid, "login", "", "")
	s.FinishTest(ok, "PASS", "", "")
	cut, _ := s.CreateTest(rid, "busca cliente", "", "")

	if r := mustRun(t, s, rid); r.Status != "RUNNING" || r.IdleMs < 0 {
		t.Fatalf("open run: %+v", r)
	}
	r, err := s.CloseRunByHand(rid, "admin", "  Se cayó la máquina  ")
	if err != nil {
		t.Fatal(err)
	}
	if r.Status != "WARNING" || !r.Incomplete || r.Interrupted != 1 || r.Failed != 0 || r.Passed != 1 {
		t.Fatalf("closed run: %+v", r)
	}
	if r.ClosedBy != "admin" || r.CloseReason != "Se cayó la máquina" || r.IdleMs != 0 {
		t.Fatalf("who and why: %+v", r)
	}
	tt, _ := s.GetTest(cut)
	if tt.Status != "INTERRUPTED" || !strings.Contains(tt.ErrorMessage, "manualmente por admin") || !strings.Contains(tt.ErrorMessage, "Se cayó la máquina") {
		t.Fatalf("cut test: %s %q", tt.Status, tt.ErrorMessage)
	}
	if done, _ := s.GetTest(ok); done.Status != "PASS" {
		t.Fatalf("a finished test keeps its result: %s", done.Status)
	}
	if _, err := s.CloseRunByHand(rid, "admin", "otra vez"); err != ErrRunClosed {
		t.Fatalf("closing twice: %v", err)
	}
	if _, err := s.CloseRunByHand(rid+99, "admin", ""); err != ErrNotFound {
		t.Fatalf("unknown run: %v", err)
	}
	// un resultado que llega tarde se acepta, y la ejecución sigue marcada como incompleta
	s.FinishTest(cut, "PASS", "", "")
	if r := mustRun(t, s, rid); r.Interrupted != 0 || r.Passed != 2 || !r.Incomplete || r.Status != "WARNING" {
		t.Fatalf("late result: %+v", r)
	}
}

func TestManualMessageWithoutUserOrReason(t *testing.T) {
	if got := ManualMessage("", ""); got != "Interrumpido: ejecución cerrada manualmente desde la interfaz." {
		t.Fatalf("%q", got)
	}
}

// Los tests que versiones anteriores cerraron como FAIL "Interrumpido…" pasan a INTERRUPTED al
// abrir la base, y su ejecución deja de contarlos como fallo.
func TestOldInterruptedFailuresAreMigrated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	rid, _ := s.CreateRun("antigua", "qa")
	real, _ := s.CreateTest(rid, "falla de verdad", "", "")
	s.FinishTest(real, "FAIL", "AssertionError", "")
	cut, _ := s.CreateTest(rid, "cortado", "", "")
	s.FinishRun(rid)
	// como lo dejaba la versión anterior
	s.db.Exec(`UPDATE tests SET status='FAIL', error_message=? WHERE id=?`, InterruptedMessage, cut)
	s.db.Exec(`UPDATE runs SET failed=2, interrupted=0, status='FAIL' WHERE id=?`, rid)
	s.Close()

	s, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if tt, _ := s.GetTest(cut); tt.Status != "INTERRUPTED" {
		t.Fatalf("old interrupted failure: %s", tt.Status)
	}
	if tt, _ := s.GetTest(real); tt.Status != "FAIL" {
		t.Fatalf("a real failure stays FAIL: %s", tt.Status)
	}
	var failed, interrupted int
	s.db.QueryRow(`SELECT failed, interrupted FROM runs WHERE id=?`, rid).Scan(&failed, &interrupted)
	if failed != 1 || interrupted != 1 {
		t.Fatalf("stored counters recounted: failed=%d interrupted=%d", failed, interrupted)
	}
}

// En las métricas un test cortado no cuenta: ni pasó ni falló.
func TestPassRateIgnoresInterrupted(t *testing.T) {
	var r rate
	for _, st := range []string{"PASS", "FAIL", "INTERRUPTED", "SKIP"} {
		r.add(st)
	}
	if r.counted != 2 || r.pct() != 50 {
		t.Fatalf("rate: %+v %.1f", r, r.pct())
	}
}
