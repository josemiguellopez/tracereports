package owners

import (
	"os"
	"path/filepath"
	"testing"
)

const rules = `
# dueños por defecto
*                          @qa-team
tests/checkout/*           Equipo pagos
*::test_login*             @auth
tag:smoke                  @qa-smoke
suite:"Billing / *"        Facturación
`

func TestOwner(t *testing.T) {
	r, err := Parse(rules)
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 5 {
		t.Fatalf("rules: %d", r.Len())
	}
	for _, c := range []struct {
		test Test
		want string
	}{
		{Test{Key: "tests/other/test_x.py::test_y"}, "@qa-team"},
		{Test{Key: "tests/checkout/test_pay.py::test_card"}, "Equipo pagos"},
		{Test{Key: "TESTS/CHECKOUT/test_pay.py::test_card"}, "Equipo pagos"}, // sin distinguir mayúsculas
		{Test{Key: "tests/checkout/test_auth.py::test_login_ok"}, "@auth"},   // la última que aplica gana
		{Test{Key: "tests/checkout/a.py::b", Tags: []string{"api", " smoke "}}, "@qa-smoke"},
		{Test{Key: "x", Suite: "Billing / Invoices"}, "Facturación"},
		{Test{Key: "x", Suite: "Billingx"}, "@qa-team"},
		{Test{}, ""}, // sin identidad ni tags: solo "*" sobre la key, que está vacía
	} {
		if got := r.Owner(c.test); got != c.want {
			t.Errorf("Owner(%+v) = %q, want %q", c.test, got, c.want)
		}
	}
}

func TestParseErrorsAndInline(t *testing.T) {
	for _, bad := range []string{"tests/*", "tag: @x", "*   ", `suite:"open @x`, `"" @x`} {
		if _, err := Parse(bad); err == nil {
			t.Errorf("%q: expected an error", bad)
		}
	}
	r, err := Parse("* @a; tag:e2e @b") // en una variable de entorno, separadas por ;
	if err != nil || r.Owner(Test{Key: "k", Tags: []string{"e2e"}}) != "@b" {
		t.Fatalf("inline: %v", err)
	}
	var none *Rules
	if none.Owner(Test{Key: "k"}) != "" || none.Len() != 0 {
		t.Fatal("nil rules own nothing")
	}
	// los caracteres de regex del patrón son literales
	r, _ = Parse("a.b(c)+* @lit")
	if r.Owner(Test{Key: "a.b(c)+zzz"}) != "@lit" || r.Owner(Test{Key: "aXb(c)+"}) != "" {
		t.Fatal("pattern metacharacters must be literal")
	}
}

func TestFromEnv(t *testing.T) {
	t.Setenv("TRACEREPORTS_OWNERS_FILE", "")
	t.Setenv("TRACEREPORTS_OWNERS", "")
	if r, err := FromEnv(); r != nil || err != nil {
		t.Fatalf("nothing set: %v %v", r, err)
	}
	file := filepath.Join(t.TempDir(), "OWNERS")
	os.WriteFile(file, []byte("* @file\n"), 0o644)
	t.Setenv("TRACEREPORTS_OWNERS_FILE", file)
	t.Setenv("TRACEREPORTS_OWNERS", "tag:x @env")
	r, err := FromEnv()
	if err != nil || r.Owner(Test{Key: "k"}) != "@file" || r.Owner(Test{Key: "k", Tags: []string{"x"}}) != "@env" {
		t.Fatalf("file + env: %v", err)
	}
	t.Setenv("TRACEREPORTS_OWNERS_FILE", filepath.Join(t.TempDir(), "missing"))
	if _, err := FromEnv(); err == nil {
		t.Fatal("a missing file is an error at startup")
	}
	if got := Tags(" smoke, ,checkout "); len(got) != 2 || got[1] != "checkout" {
		t.Fatalf("tags: %v", got)
	}
}
