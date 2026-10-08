package release

import (
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// reopen deja la ejecución como recién creada / en curso: sin fin y con estado RUNNING.
func reopen(d *db.RunDetail) *db.RunDetail {
	d.Status, d.EndedAt = "RUNNING", nil
	return d
}

func TestOpenRunsNeverGetGo(t *testing.T) {
	cases := map[string]*db.RunDetail{
		// recién creada, sin tests
		"open and empty": reopen(run()),
		// todos los resultados que llegaron son PASS y no hay ninguno corriendo
		"open, all received PASS": reopen(run(test("a", "PASS", "smoke"), test("b", "PASS", "pim"))),
	}
	for name, d := range cases {
		got := Evaluate(Default(), d, nil)
		if got.Decision == Go || check(got, "complete").OK || check(got, "complete").Detail != "open" {
			t.Fatalf("%s: %s %+v", name, got.Decision, check(got, "complete"))
		}
		if got.Decision != NoGo {
			t.Fatalf("%s: an open run blocks: %s", name, got.Decision)
		}
	}
	// con tests corriendo
	d := run(test("a", "PASS", ""), test("b", "RUNNING", ""))
	d.Running = 1
	if got := Evaluate(Default(), reopen(d), nil); got.Decision != NoGo {
		t.Fatalf("running tests: %s", got.Decision)
	}
}

func TestClosedRuns(t *testing.T) {
	// cerrada y completa: cumple el gate
	if got := Evaluate(Default(), run(test("a", "PASS", "smoke"), test("b", "PASS", "pim")), nil); got.Decision != Go {
		t.Fatalf("closed and complete: %s %+v", got.Decision, got.Checks)
	}
	// cerrada pero incompleta (interrumpida): sigue bloqueando
	d := run(test("a", "PASS", ""))
	d.Incomplete = true
	if got := Evaluate(Default(), d, nil); got.Decision != NoGo || check(got, "complete").Detail != "incomplete" {
		t.Fatalf("incomplete: %s %+v", got.Decision, check(got, "complete"))
	}
}

// Política: sin ningún test ejecutado (sin tests o todos SKIP) no hay evidencia para salir.
func TestNoEvidencePolicy(t *testing.T) {
	for name, d := range map[string]*db.RunDetail{
		"closed, no tests": run(),
		"closed, all SKIP": run(test("a", "SKIP", ""), test("b", "SKIP", "")),
	} {
		got := Evaluate(Default(), d, nil)
		if got.Decision != NoGo || check(got, "evidence").OK || check(got, "evidence").Detail != "0" {
			t.Fatalf("%s: %s %+v", name, got.Decision, check(got, "evidence"))
		}
		if !check(got, "complete").OK || !check(got, "pass_rate").OK {
			t.Fatalf("%s: only the evidence check blocks: %+v", name, got.Checks)
		}
	}
	// un SKIP junto a tests ejecutados no cambia nada
	if got := Evaluate(Default(), run(test("a", "PASS", ""), test("b", "SKIP", "")), nil); got.Decision != Go || !check(got, "evidence").OK {
		t.Fatalf("pass + skip: %s", got.Decision)
	}
}
