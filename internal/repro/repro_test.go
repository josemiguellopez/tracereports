package repro

import (
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

func TestCurlMasksSecrets(t *testing.T) {
	c := &db.NetConn{Method: "POST", URL: "https://api.example.com/auth/login?lang=es&token=abc",
		RequestHeaders: map[string]string{"Authorization": "<masked>", "Content-Type": "application/json", ":authority": "x", "X-Api-Key": "k1"},
		PostData:       `{"user":"ana","password":"<masked>","rut":"12.345.678-9"}`}
	got := Curl(c, redact.Default())
	for _, want := range []string{
		"curl -X POST 'https://api.example.com/auth/login?lang=es&token=***'",
		`-H "Authorization: $AUTHORIZATION"`,
		`-H 'Content-Type: application/json'`,
		`-H "X-Api-Key: $X_API_KEY"`,
		`"password":"***"`,
		`"rut":"***"`,
		`"user":"ana"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("falta %q en:\n%s", want, got)
		}
	}
	for _, bad := range []string{"<masked>", "abc", "k1", ":authority", "12.345.678-9"} {
		if strings.Contains(got, bad) {
			t.Errorf("no debe contener %q:\n%s", bad, got)
		}
	}
}

func TestCurlKeepsBodyAsSent(t *testing.T) {
	c := &db.NetConn{Method: "PUT", URL: "https://api.example.com/x", PostData: `{"z":1,"a":"it's"}`}
	got := Curl(c, redact.Default())
	if !strings.Contains(got, `--data-raw '{"z":1,"a":"it'\''s"}'`) {
		t.Errorf("body cambiado: %s", got)
	}
	if !strings.HasPrefix(Curl(&db.NetConn{URL: "/rel"}, nil), "curl -X GET '/rel'") {
		t.Error("sin método debe ser GET")
	}
}

func TestCommands(t *testing.T) {
	cases := []struct {
		fw, key string
		want    []string
	}{
		// este test y, después, todo su archivo o clase; sin git checkout (el commit va aparte: Script)
		{"pytest", "tests/test_login.py::test_admin", []string{"pytest 'tests/test_login.py::test_admin'", "pytest 'tests/test_login.py'"}},
		{"playwright", "tests/login.spec.js > Login > admin entra [chromium]", []string{"npx playwright test 'tests/login.spec.js' -g '^admin entra$' --project='chromium'", "npx playwright test 'tests/login.spec.js' --project='chromium'"}},
		{"junit5", "com.acme.LoginTest#loginOk", []string{"mvn test -Dtest='LoginTest#loginOk'", "./gradlew test --tests 'com.acme.LoginTest.loginOk'", "mvn test -Dtest='LoginTest'", "./gradlew test --tests 'com.acme.LoginTest'"}},
		{"go", "shop/checkout/TestPay/visa", []string{"go test ./shop/checkout/... -run '^TestPay$/^visa$'"}},
		// claves de Allure (importadas): allure-pytest, allure-playwright y allure-junit5
		{"pytest", "test.allure_attachments_test#test_attach", []string{"pytest 'test/allure_attachments_test.py::test_attach'", "pytest 'test/allure_attachments_test.py'"}},
		{"pytest", "tests.shop.test_cart.TestCart#test_add [qty=2]", []string{"pytest 'tests/shop/test_cart.py::TestCart::test_add'", "pytest 'tests/shop/test_cart.py'"}},
		{"playwright", "tests/sample.test.js:4:1 [Project=chromium]", []string{"npx playwright test 'tests/sample.test.js:4' --project='chromium'", "npx playwright test 'tests/sample.test.js' --project='chromium'"}},
		{"junit-platform", "com.example.junit5.StepTest.lambdaStepTest", []string{"mvn test -Dtest='StepTest#lambdaStepTest'", "./gradlew test --tests 'com.example.junit5.StepTest.lambdaStepTest'", "mvn test -Dtest='StepTest'", "./gradlew test --tests 'com.example.junit5.StepTest'"}},
		{"pytest", "name:algo", nil},
		{"cypress", "x", nil},
	}
	for _, c := range cases {
		got := Commands(c.fw, c.key)
		if len(got) != len(c.want) {
			t.Fatalf("%s %s: %v", c.fw, c.key, got)
		}
		for i := range got {
			if got[i].Cmd != c.want[i] {
				t.Errorf("%s: %q, quería %q", c.fw, got[i].Cmd, c.want[i])
			}
		}
	}
}

// Un caso real: test_002 depende de que test_001 haya creado el pedido; corrido solo falla por
// otra razón ("número de pedido no puede ser vacío"). Ahí el archivo completo va primero.
func TestDependentTestRunsTheWholeFileFirst(t *testing.T) {
	key := "tests/shop/checkout/test_pay_with_saved_card.py::TestCheckout::test_002_pay_with_saved_card"
	cmds := Commands("pytest", key)
	if !DependsOnEarlier("Exception: Error número de pedido no puede ser vacio") ||
		!DependsOnEarlier("Failed: Test 'test_002' omitido — paso anterior falló: 'test_001'") ||
		DependsOnEarlier("AssertionError: assert 10 == 12") || DependsOnEarlier("TimeoutError: waiting for locator('#pay')") {
		t.Fatal("DependsOnEarlier")
	}
	got := Ordered(cmds, true)
	if got[0].Scope != ScopeFile || got[0].Cmd != "pytest 'tests/shop/checkout/test_pay_with_saved_card.py'" || got[1].Scope != ScopeTest {
		t.Fatalf("file first: %+v", got)
	}
	if same := Ordered(cmds, false); same[0].Scope != ScopeTest {
		t.Fatalf("test first: %+v", same)
	}
	s := Script(got, "fd051753d33f9a", "es")
	want := "# Todo el archivo (si el test depende de otros anteriores del mismo archivo) · pytest\n" +
		"pytest 'tests/shop/checkout/test_pay_with_saved_card.py'\n\n" +
		"# Este test · pytest\npytest '" + key + "'\n\n" +
		"# La ejecución usó el commit fd051753d33f; si tu copia está en otra versión: git checkout fd051753d33f"
	if s != want {
		t.Fatalf("script:\n%s\nwant:\n%s", s, want)
	}
}
