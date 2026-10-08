package repro

import (
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

// Valores hostiles (marcadores inocuos: nada se ejecuta, solo se mira el texto generado).
var hostileCommits = []string{
	";echo MARK;#",
	"abc123 && echo MARK",
	"$(echo MARK)",
	"`echo MARK`",
	"abc'; echo MARK; '",
	"abc123\necho MARK",
	"abc|echo MARK",
	"abc123 #",
	"HEAD",
	"v1.2.3",
}

func TestCommitFormat(t *testing.T) {
	full := "0123456789abcdef0123456789abcdef01234567"
	sha256 := strings.Repeat("ab", 32)
	for _, c := range []string{full, "abc1234", "ABCDEF12", sha256, "abcd"} {
		if !ValidCommit(c) {
			t.Errorf("%q is a valid Git commit id", c)
		}
	}
	if NormalizeCommit("ABCDEF12") != "abcdef12" || NormalizeCommit(" abc1234 ") != "abc1234" {
		t.Error("normalized to lowercase, trimmed")
	}
	for _, c := range append(hostileCommits, "", "abc", strings.Repeat("a", 65), "xyz1234") {
		if ValidCommit(c) || NormalizeCommit(c) != "" {
			t.Errorf("%q must be rejected", c)
		}
	}
}

func TestCommandsNeverCarryAHostileCommit(t *testing.T) {
	for _, c := range hostileCommits {
		for _, fw := range []struct{ fw, key string }{
			{"pytest", "test_login.py::test_admin"},
			{"playwright", "tests/a.spec.js > A > b [chromium]"},
			{"junit5", "com.acme.LoginTest#ok"},
			{"go", "shop/TestPay"},
		} {
			for _, cmd := range Commands(fw.fw, fw.key, c) {
				if strings.Contains(cmd.Cmd, "MARK") || strings.Contains(cmd.Cmd, "git checkout") {
					t.Errorf("%s with commit %q: %s", fw.fw, c, cmd.Cmd)
				}
			}
		}
	}
	// válidos: completo (se acorta a 12), abreviado y vacío
	if got := Commands("pytest", "t.py::x", "0123456789ABCDEF0123")[0].Cmd; got != "git checkout 0123456789ab && pytest 't.py::x'" {
		t.Errorf("full: %s", got)
	}
	if got := Commands("pytest", "t.py::x", "abc1234")[0].Cmd; got != "git checkout abc1234 && pytest 't.py::x'" {
		t.Errorf("short: %s", got)
	}
	if got := Commands("pytest", "t.py::x", "")[0].Cmd; got != "pytest 't.py::x'" {
		t.Errorf("empty: %s", got)
	}
}

func TestGoPackageFromAHostileKeyIsQuoted(t *testing.T) {
	got := Commands("go", "shop;echo MARK;/TestPay", "")[0].Cmd
	if !strings.HasPrefix(got, "go test './shop;echo MARK;/...' -run ") {
		t.Fatalf("the package must be a literal argument: %s", got)
	}
	got = Commands("go", "shop/$(echo MARK)/TestPay", "")[0].Cmd
	if !strings.HasPrefix(got, "go test './shop/$(echo MARK)/...'") {
		t.Fatalf("no substitution: %s", got)
	}
	// el caso normal no cambia
	if got := Commands("go", "shop/checkout/TestPay/visa", "")[0].Cmd; got != "go test ./shop/checkout/... -run '^TestPay$/^visa$'" {
		t.Fatalf("normal: %s", got)
	}
	if got := Commands("go", "TestRoot", "")[0].Cmd; got != "go test ./... -run '^TestRoot$'" {
		t.Fatalf("root package: %s", got)
	}
}

func TestCurlQuotesHostileMethodAndHeaderNames(t *testing.T) {
	c := &db.NetConn{Method: "GET;echo MARK", URL: "https://x/y",
		RequestHeaders: map[string]string{`X-Auth$(echo MARK)`: "<masked>", `Token"; echo MARK; "`: "<masked>", "1-Token": "<masked>"}}
	got := Curl(c, redact.Default())
	for _, bad := range []string{`-X GET;echo`, `"X-Auth$(echo MARK)`, `"Token"; echo`} {
		if strings.Contains(got, bad) {
			t.Fatalf("interpreted by the shell (%q):\n%s", bad, got)
		}
	}
	for _, want := range []string{`curl -X 'GET;echo MARK'`, `-H 'X-Auth$(echo MARK): '"$X_AUTH_ECHO_MARK_"`, `-H "1-Token: $H_1_TOKEN"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q:\n%s", want, got)
		}
	}
	// los normales no cambian
	got = Curl(&db.NetConn{Method: "POST", URL: "https://x/y", RequestHeaders: map[string]string{"Authorization": "<masked>"}}, redact.Default())
	if !strings.HasPrefix(got, "curl -X POST 'https://x/y'") || !strings.Contains(got, `-H "Authorization: $AUTHORIZATION"`) {
		t.Fatalf("normal: %s", got)
	}
}
