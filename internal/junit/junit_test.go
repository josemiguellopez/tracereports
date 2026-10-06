package junit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parseFile(t *testing.T, name string) []Suite {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	suites, err := Parse(f)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return suites
}

func cases(suites []Suite) []Case {
	var out []Case
	for _, s := range suites {
		out = append(out, s.Cases...)
	}
	return out
}

func TestPytest(t *testing.T) {
	suites := parseFile(t, "pytest.xml")
	if len(suites) != 1 || suites[0].Name != "pytest" {
		t.Fatalf("suites: %+v", suites)
	}
	want := time.Date(2026, 10, 6, 10, 0, 0, 123456000, time.UTC)
	if !suites[0].Timestamp.Equal(want) || suites[0].Duration != 3512*time.Millisecond {
		t.Fatalf("suite time: %v %v", suites[0].Timestamp, suites[0].Duration)
	}
	c := suites[0].Cases
	if len(c) != 4 {
		t.Fatalf("cases: %d", len(c))
	}
	if c[0].Status != Pass || c[0].Key() != "tests.test_login#test_login_ok" || c[0].Duration != 1200*time.Millisecond {
		t.Fatalf("pass case: %+v", c[0])
	}
	fail := c[1]
	if fail.Status != Fail || fail.Message != "AssertionError: assert 500 == 200" {
		t.Fatalf("failure: %+v", fail)
	}
	if !strings.Contains(fail.Trace, ">       assert resp.status == 200") || !strings.HasSuffix(fail.Trace, "AssertionError") {
		t.Fatalf("trace: %q", fail.Trace)
	}
	if fail.SystemOut != "abriendo login\nenviando formulario" {
		t.Fatalf("system-out: %q", fail.SystemOut)
	}
	if c[2].Status != Skip || c[2].Message != "Sin datos del mes" {
		t.Fatalf("skip: %+v", c[2])
	}
	// <error> (fallo en setup) también es un fallo
	if c[3].Status != Fail || c[3].Message != `failed on setup with "ConnectionError: db down"` || c[3].Trace != "ConnectionError: db down" {
		t.Fatalf("error: %+v", c[3])
	}
}

func TestSurefire(t *testing.T) {
	suites := parseFile(t, "surefire.xml")
	c := cases(suites)
	if len(suites) != 1 || suites[0].Name != "com.acme.LoginTest" || len(c) != 3 {
		t.Fatalf("suites: %+v", suites)
	}
	if !suites[0].Timestamp.IsZero() {
		t.Fatalf("no timestamp in report: %v", suites[0].Timestamp)
	}
	flaky := c[1]
	if flaky.Status != Pass || flaky.Attempts != 2 || flaky.Duration != 1000250*time.Millisecond {
		t.Fatalf("flaky passed on retry: %+v", flaky)
	}
	broken := c[2]
	if broken.Status != Fail || broken.Attempts != 3 {
		t.Fatalf("failed after reruns: %+v", broken)
	}
	if broken.Message != "org.opentest4j.AssertionFailedError: expected: <200> but was: <500>" {
		t.Fatalf("message with type: %q", broken.Message)
	}
	if !strings.Contains(broken.Trace, "LoginTest.java:30") {
		t.Fatalf("trace: %q", broken.Trace)
	}
	if c[0].Key() != "com.acme.LoginTest#loginOk" {
		t.Fatalf("key: %q", c[0].Key())
	}
}

func TestPlaywright(t *testing.T) {
	suites := parseFile(t, "playwright.xml")
	c := cases(suites)
	if len(c) != 2 || suites[0].Name != "login.spec.ts" {
		t.Fatalf("suites: %+v", suites)
	}
	if c[0].Status != Fail || !strings.Contains(c[0].Trace, "Timed out 5000ms") {
		t.Fatalf("failure: %+v", c[0])
	}
	// type="FAILURE" es genérico, pero se conserva tal cual (no se inventa nada)
	if c[0].Message != "FAILURE: login.spec.ts:10:5 admin sees the dashboard" {
		t.Fatalf("message: %q", c[0].Message)
	}
	if c[1].Status != Pass {
		t.Fatalf("pass: %+v", c[1])
	}
}

func TestNestedSuitesAndSkippedWithoutReason(t *testing.T) {
	suites := parseFile(t, "jest.xml")
	if len(suites) != 2 || suites[0].Name != "Cart" || suites[1].Name != "Cart / Checkout" {
		t.Fatalf("suites: %+v", suites)
	}
	if suites[0].Cases[1].Status != Skip || suites[0].Cases[1].Message != "" {
		t.Fatalf("skip without reason: %+v", suites[0].Cases[1])
	}
	// sin zona horaria: UTC
	if !suites[0].Timestamp.Equal(time.Date(2026, 10, 6, 10, 10, 0, 0, time.UTC)) {
		t.Fatalf("timestamp: %v", suites[0].Timestamp)
	}
	// suite sin time: la suma de sus casos
	if suites[1].Duration != 200*time.Millisecond {
		t.Fatalf("duration: %v", suites[1].Duration)
	}
}

func TestLatin1AndHTMLEntities(t *testing.T) {
	c := cases(parseFile(t, "latin1.xml"))
	if len(c) != 1 || c[0].Name != "validación  ok" {
		t.Fatalf("latin1: %+v", c)
	}
}

func TestRejectsWhatIsNotJUnit(t *testing.T) {
	for name, doc := range map[string]string{
		"empty":           "",
		"html":            "<html><body>hi</body></html>",
		"json":            `{"tests":[]}`,
		"truncated":       `<testsuite name="x"><testcase name="a">`,
		"unknown charset": `<?xml version="1.0" encoding="x-nope"?><testsuite/>`,
	} {
		if _, err := Parse(strings.NewReader(doc)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestExternalEntitiesAreNotResolved(t *testing.T) {
	doc := `<?xml version="1.0"?><!DOCTYPE x [<!ENTITY xxe SYSTEM "file:///etc/passwd">]><testsuite name="s"><testcase name="&xxe;"/></testsuite>`
	if _, err := Parse(strings.NewReader(doc)); err == nil {
		t.Fatal("an undeclared external entity must be rejected, never resolved")
	}
}

func TestEdgeValues(t *testing.T) {
	doc := `<testsuites><testcase name="  " time="abc"/><testcase classname="C" name="n" time="-1"/></testsuites>`
	c := cases(mustParse(t, doc))
	if len(c) != 2 || c[0].Name != "(unnamed test)" || c[0].Duration != 0 || c[1].Duration != 0 || c[0].Key() != "(unnamed test)" {
		t.Fatalf("edge values: %+v", c)
	}
	if c[0].Attempts != 1 {
		t.Fatalf("attempts default: %d", c[0].Attempts)
	}
}

func TestFailureWithoutMessageUsesFirstTraceLine(t *testing.T) {
	c := cases(mustParse(t, `<testsuite><testcase name="t"><failure>
		boom happened
		at line 2</failure></testcase></testsuite>`))
	if c[0].Message != "boom happened" {
		t.Fatalf("message: %q", c[0].Message)
	}
}

func mustParse(t *testing.T, doc string) []Suite {
	t.Helper()
	s, err := Parse(strings.NewReader(doc))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
