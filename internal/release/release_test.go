package release

import (
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func test(name, status, tags string) db.Test {
	return db.Test{Name: name, Status: status, Category: tags}
}

// run is a closed run (what the gate decides on); open() reopens it for the open-run cases.
func run(tests ...db.Test) *db.RunDetail {
	d := &db.RunDetail{Tests: tests}
	d.ID = 9
	ended := int64(2000)
	d.Status, d.EndedAt = "PASS", &ended
	return d
}

func check(d *Decision, id string) Check {
	for _, c := range d.Checks {
		if c.ID == id {
			return c
		}
	}
	return Check{ID: "missing"}
}

func TestGoWhenEverythingPasses(t *testing.T) {
	g, _ := Parse("critical=smoke")
	d := Evaluate(g, run(test("login", "PASS", "smoke"), test("buscar", "PASS", "pim"), test("x", "SKIP", "")), []string{})
	if d.Decision != Go || d.PassRate != 100 {
		t.Fatalf("all green: %+v", d)
	}
	for _, c := range d.Checks {
		if !c.OK {
			t.Fatalf("check %s should pass: %+v", c.ID, c)
		}
	}
}

func TestCriticalFailureBlocks(t *testing.T) {
	g, _ := Parse("critical=Smoke, checkout; min_pass_rate=50")
	d := Evaluate(g, run(test("login", "FAIL", "smoke,login"), test("buscar", "PASS", "pim"), test("pagar", "PASS", "checkout")), nil)
	c := check(d, "critical")
	if d.Decision != NoGo || c.OK || strings.Join(c.Tests, ",") != "login" {
		t.Fatalf("a critical tag failed: %+v", d)
	}
	if check(d, "new_failures").ID != "missing" {
		t.Fatal("without a previous run there is no new_failures check")
	}
	// funcionalidades: las críticas primero, y entre ellas las que fallan
	var names []string
	for _, f := range d.Features {
		names = append(names, f.Name+":"+f.Status)
	}
	if strings.Join(names, " ") != "smoke:fail checkout:ok login:fail pim:ok" {
		t.Fatalf("features: %v", names)
	}
}

func TestPassRateAndIncompleteBlock(t *testing.T) {
	d := Evaluate(Default(), run(test("a", "PASS", ""), test("b", "FAIL", ""), test("c", "PASS", ""), test("d", "PASS", "")), []string{})
	if d.Decision != NoGo || check(d, "pass_rate").OK || check(d, "pass_rate").Detail != "75.0/95.0" {
		t.Fatalf("75%% < 95%%: %+v", d)
	}
	inc := run(test("a", "PASS", ""))
	inc.Incomplete = true
	if Evaluate(Default(), inc, nil).Decision != NoGo {
		t.Fatal("an incomplete run never ships")
	}
}

func TestRisks(t *testing.T) {
	g, _ := Parse("min_pass_rate=50; max_flaky=0")
	q := test("cupon", "FAIL", "checkout")
	q.Quarantine = &db.Quarantine{Active: true}
	flaky := test("buscar", "PASS", "pim")
	flaky.Flaky = true
	d := Evaluate(g, run(test("login", "PASS", "login"), q, flaky), []string{"login"}) // login falló antes? aquí: nuevo
	if d.Decision != Risk {
		t.Fatalf("quarantine, flaky and a new failure are risks, not blockers: %+v", d)
	}
	if d.PassRate != 100 { // el fallo en cuarentena no cuenta
		t.Fatalf("pass rate without the quarantined failure: %v", d.PassRate)
	}
	for _, id := range []string{"quarantined", "flaky", "new_failures"} {
		if check(d, id).OK {
			t.Errorf("%s should fail", id)
		}
	}
	var checkout Feature
	for _, f := range d.Features {
		if f.Name == "checkout" {
			checkout = f
		}
	}
	if checkout.Status != "warn" || checkout.Quarantined != 1 {
		t.Fatalf("an area with only quarantined failures is a warning: %+v", checkout)
	}
}

func TestParse(t *testing.T) {
	g, err := Parse(" min_pass_rate = 90 ; critical = a , ,b ; max_new_failures=2;max_flaky=0 ")
	if err != nil || g.MinPassRate != 90 || strings.Join(g.Critical, ",") != "a,b" || g.MaxNewFailures != 2 || g.MaxFlaky != 0 {
		t.Fatalf("parse: %+v %v", g, err)
	}
	if g, _ := Parse(""); g.MinPassRate != 95 || g.MaxFlaky != 3 {
		t.Fatalf("defaults: %+v", g)
	}
	for _, bad := range []string{"min_pass_rate=120", "max_flaky=-1", "foo=1", "critical"} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
}
