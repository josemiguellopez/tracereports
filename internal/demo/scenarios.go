package demo

import "embed"

// Escenarios: cada uno es una grabación REAL de un cliente de TraceReports (lo que de verdad envía
// pytest, Playwright JS, Selenium Java o Go) corriendo contra la tienda, más lo que TraceReports debe
// hacer con ella. La misma lista alimenta la demo, el playground y `tracereports selftest`, que
// reproduce cada grabación y comprueba sus expectativas: si un cambio rompe el camino de los datos
// (lo que manda un cliente → lo que muestra el reporte), falla ahí. Se graban con
// `go run ./test/scenarios/record` (ver test/scenarios/README.md).

//go:embed scenarios
var scenarioFiles embed.FS

// Client kinds of the scenarios.
const (
	ClientREST       = "rest"
	ClientPytest     = "pytest"
	ClientPlaywright = "playwright-js"
	ClientSelenium   = "selenium-java"
	ClientGo         = "go"
	ClientJUnit      = "junit-xml"
	ClientAllure     = "allure"
)

// Scenario is one recorded situation and what TraceReports must do with it.
type Scenario struct {
	ID     string `json:"id"`
	Client string `json:"client"`
	Kind   string `json:"kind"` // ok | nok | flaky | suite
	Title  string `json:"title"`
	What   string `json:"what"` // qué muestra, para el playground
	// Run is the run name; Test selects the tests to record (pytest -k, Playwright -g regex,
	// Gradle --tests, go test -run regex); empty records the whole suite.
	Run  string `json:"run"`
	Test string `json:"-"`
	// Bugs of the shop while recording (and while checking the Lab): the orders one is on by default.
	Bugs   map[string]bool `json:"-"`
	Expect Expect          `json:"expect"`
	// Hidden scenarios are not listed in the playground: the earlier nights of the demo.
	Hidden bool `json:"hidden,omitempty"`
}

// Expect is what TraceReports must show after receiving a scenario.
type Expect struct {
	RunStatus string            `json:"run_status"` // PASS | FAIL
	Tests     map[string]string `json:"tests"`      // nombre del test -> PASS | FAIL | SKIP
	// Focus is the test whose evidence is checked (incident, network, Lab, locator...); empty:
	// the first failed one (by name), or the first one.
	Focus    string `json:"focus,omitempty"`
	Incident string `json:"incident,omitempty"` // backend | error: el incidente que agrupa el fallo
	// Lab: the failed backend call is resent from a capsule and reproduces while the shop is broken.
	Lab bool `json:"lab,omitempty"`
	// Locator: TraceReports suggests a new selector for the broken one.
	Locator string `json:"locator,omitempty"`
	// NoLocator: the element did not show up because the backend failed, so TraceReports must not
	// suggest another selector (that would send the developer the wrong way).
	NoLocator bool `json:"no_locator,omitempty"`
	// Flaky: the test passed after a retry.
	Flaky bool `json:"flaky,omitempty"`
	// Masked: the shop password never reaches the stored evidence.
	Masked bool `json:"masked,omitempty"`
	// Command: the command that runs the test locally contains this.
	Command    string `json:"command,omitempty"`
	Screenshot bool   `json:"screenshot,omitempty"`
	Network    bool   `json:"network,omitempty"`
	// Builtin is the category the local rules engine must give the focus test (none: no failed
	// test, so no diagnosis). It only counts for the hit rate of selftest, never makes a scenario
	// fail by itself (see BuiltinCheck). allure-import has no network capture: the evidence only
	// shows an element that never appeared, so the expected answer is the locator one.
	Builtin string `json:"builtin,omitempty"`
}

// Scenarios is the catalog, in the order the playground shows it.
var Scenarios = []Scenario{
	{ID: "rest-ok", Client: ClientREST, Kind: "ok", Title: "REST API · a passing run",
		What:   "Any language can report over the REST API: a run with two passing tests and their steps.",
		Run:    "Shop smoke · REST API",
		Expect: Expect{Builtin: "none", RunStatus: "PASS", Tests: map[string]string{"health check answers": "PASS", "catalog lists 3 products": "PASS"}}},
	{ID: "rest-nok-backend", Client: ClientREST, Kind: "nok", Title: "REST API · backend answers 500",
		What:   "A test reported over REST with its network: the failing call becomes a backend incident you can resend in the Lab.",
		Run:    "Orders · REST API",
		Expect: Expect{Builtin: "BACKEND_TIMEOUT", RunStatus: "FAIL", Tests: map[string]string{"create an order": "FAIL"}, Incident: "backend", Lab: true, Masked: true, Network: true}},

	{ID: "pytest-ok", Client: ClientPytest, Kind: "ok", Title: "pytest + Playwright · sign in",
		What: "A real pytest-playwright test: steps, a screenshot and every call the browser made, with the password masked.",
		Run:  "Shop · pytest + Playwright", Test: "test_sign_in_shows_the_catalog",
		Expect: Expect{Builtin: "none", RunStatus: "PASS", Tests: map[string]string{"test_sign_in_shows_the_catalog[chromium]": "PASS"}, Masked: true, Network: true, Screenshot: true,
			Command: "pytest"}},
	{ID: "pytest-nok-backend", Client: ClientPytest, Kind: "nok", Title: "pytest + Playwright · checkout fails (500)",
		What: "The order is never confirmed because POST /api/orders answers 500: backend incident, screenshot, console and the Lab.",
		Run:  "Shop · pytest + Playwright", Test: "test_checkout_pays_the_order",
		Expect: Expect{Builtin: "BACKEND_TIMEOUT", RunStatus: "FAIL", Tests: map[string]string{"test_checkout_pays_the_order[chromium]": "FAIL"}, NoLocator: true, Incident: "backend", Lab: true, Masked: true,
			Network: true, Screenshot: true, Command: "pytest"}},
	{ID: "pytest-nok-selector", Client: ClientPytest, Kind: "nok", Title: "pytest + Playwright · a selector changed",
		What: "The test still uses #add-to-cart-btn; the page now has data-testid=\"add-to-cart\": TraceReports suggests the new locator.",
		Run:  "Shop · pytest + Playwright", Test: "test_add_to_cart_with_the_old_selector",
		Expect: Expect{Builtin: "LOCATOR_CHANGED", RunStatus: "FAIL", Tests: map[string]string{"test_add_to_cart_with_the_old_selector[chromium]": "FAIL"}, Incident: "error", Locator: "add-to-cart",
			Screenshot: true, Command: "pytest"}},

	{ID: "playwright-ok", Client: ClientPlaywright, Kind: "ok", Title: "Playwright Test (JS) · sign in",
		What: "A real Playwright Test run with the TraceReports reporter: steps, attachments and network.",
		Run:  "Shop · Playwright (JS)", Test: "signs in and sees the catalog",
		Expect: Expect{Builtin: "none", RunStatus: "PASS", Tests: map[string]string{"Shop › signs in and sees the catalog": "PASS"}, Masked: true, Network: true,
			Command: "npx playwright test"}},
	{ID: "playwright-nok-backend", Client: ClientPlaywright, Kind: "nok", Title: "Playwright Test (JS) · checkout fails (500)",
		What: "Same backend failure seen from JavaScript: it groups into a backend incident and the Lab reproduces it.",
		Run:  "Shop · Playwright (JS)", Test: "pays an order with a card",
		Expect: Expect{Builtin: "BACKEND_TIMEOUT", RunStatus: "FAIL", Tests: map[string]string{"Shop › pays an order with a card": "FAIL"}, NoLocator: true, Incident: "backend", Lab: true, Masked: true,
			Network: true, Command: "npx playwright test"}},
	{ID: "playwright-flaky", Client: ClientPlaywright, Kind: "flaky", Title: "Playwright Test (JS) · passes on the retry",
		What: "Recommendations fail on the first call (503) and work on the second: the test is marked as passed after retry (flaky).",
		Run:  "Shop · Playwright (JS)", Test: "shows recommendations on the product page", Bugs: map[string]bool{"orders": true, "flaky": true},
		Expect: Expect{Builtin: "none", RunStatus: "PASS", Tests: map[string]string{"Shop › shows recommendations on the product page": "PASS"}, Flaky: true, Network: true}},

	{ID: "selenium-ok", Client: ClientSelenium, Kind: "ok", Title: "Selenium + JUnit 5 (Java) · sign in",
		What: "A real Selenium WebDriver test with the JUnit 5 extension: steps, a screenshot and the browser's network from Chrome's performance log.",
		Run:  "Shop · Selenium (Java)", Test: "com.demoshop.ShopTest.signInShowsCatalog",
		Expect: Expect{Builtin: "none", RunStatus: "PASS", Tests: map[string]string{"Signing in shows the catalog": "PASS"}, Screenshot: true, Network: true, Masked: true,
			Command: "gradlew test"}},
	{ID: "selenium-nok-selector", Client: ClientSelenium, Kind: "nok", Title: "Selenium + JUnit 5 (Java) · a selector changed",
		What: "NoSuchElementException for #add-to-cart-btn: screenshot, page snapshot and the suggested locator.",
		Run:  "Shop · Selenium (Java)", Test: "com.demoshop.ShopTest.addToCartWithOldSelector",
		Expect: Expect{Builtin: "LOCATOR_CHANGED", RunStatus: "FAIL", Tests: map[string]string{"Add to cart with the old selector": "FAIL"}, Incident: "error", Locator: "add-to-cart",
			Screenshot: true, Network: true, Command: "gradlew test"}},

	{ID: "go-ok", Client: ClientGo, Kind: "ok", Title: "Go (go test) · API passes",
		What: "An API test in Go with the Go client: the call it made appears in the Network tab.",
		Run:  "Shop API · Go", Test: "TestCatalog",
		Expect: Expect{Builtin: "none", RunStatus: "PASS", Tests: map[string]string{"Catalog lists the products": "PASS"}, Network: true, Command: "go test"}},
	{ID: "go-nok-backend", Client: ClientGo, Kind: "nok", Title: "Go (go test) · API answers 500",
		What: "The API test that creates an order fails with a 500: backend incident and the Lab, from Go.",
		Run:  "Shop API · Go", Test: "TestCreateOrder",
		Expect: Expect{Builtin: "BACKEND_TIMEOUT", RunStatus: "FAIL", Tests: map[string]string{"Create an order": "FAIL"}, Incident: "backend", Lab: true, Masked: true, Network: true,
			Command: "go test"}},

	{ID: "junit-import", Client: ClientJUnit, Kind: "nok", Title: "JUnit XML import (Maven Surefire)",
		What:   "No client at all: a Surefire XML report imported with POST /api/v1/import/junit (passed, failed and skipped tests).",
		Run:    "Inventory · Maven (JUnit XML)",
		Expect: Expect{Builtin: "BACKEND_TIMEOUT", RunStatus: "FAIL", Tests: map[string]string{"reservesStock": "PASS", "rejectsNegativeQuantity": "FAIL", "syncsWithWarehouse": "SKIP"}}},
	{ID: "allure-import", Client: ClientAllure, Kind: "nok", Title: "Allure import (pytest + allure-pytest)",
		What: "No TraceReports client: the allure-results folder of a pytest + Playwright suite that only uses Allure, imported with POST /api/v1/import/allure. Steps, screenshots and each failure diagnosed, without reading the Allure report test by test.",
		Run:  "Shop · Allure (allure-pytest)",
		Expect: Expect{Builtin: "LOCATOR_CHANGED", RunStatus: "FAIL", Tests: map[string]string{"test_sign_in_shows_the_catalog[chromium]": "PASS", "test_checkout_pays_the_order[chromium]": "FAIL",
			"test_add_to_cart_with_the_old_selector[chromium]": "FAIL"}, Focus: "test_checkout_pays_the_order[chromium]", Screenshot: true,
			Command: "pytest 'test_shop_allure.py::test_"}},

	// Suites completas: una ejecución con todos los tests de un cliente, como una noche real.
	{ID: "pytest-suite", Client: ClientPytest, Kind: "suite", Title: "pytest + Playwright · the whole suite",
		What: "The three Python tests in one run, like a real night: one passes, the checkout fails on the backend and another on a changed selector.",
		Run:  "Shop · pytest + Playwright",
		Expect: Expect{Builtin: "BACKEND_TIMEOUT", RunStatus: "FAIL", Tests: map[string]string{"test_sign_in_shows_the_catalog[chromium]": "PASS",
			"test_checkout_pays_the_order[chromium]": "FAIL", "test_add_to_cart_with_the_old_selector[chromium]": "FAIL"},
			Focus: "test_checkout_pays_the_order[chromium]", NoLocator: true, Incident: "backend", Lab: true, Masked: true, Network: true, Screenshot: true, Command: "pytest"}},
	{ID: "playwright-suite", Client: ClientPlaywright, Kind: "suite", Title: "Playwright Test (JS) · the whole suite",
		What: "The three JavaScript tests in one run: one passes, one fails on the backend and one passes on the retry.",
		Run:  "Shop · Playwright (JS)", Bugs: map[string]bool{"orders": true, "flaky": true},
		Expect: Expect{Builtin: "BACKEND_TIMEOUT", RunStatus: "FAIL", Tests: map[string]string{"Shop › signs in and sees the catalog": "PASS", "Shop › pays an order with a card": "FAIL",
			"Shop › shows recommendations on the product page": "PASS"}, Focus: "Shop › pays an order with a card", NoLocator: true, Incident: "backend", Lab: true, Masked: true,
			Network: true, Flaky: true, Command: "npx playwright test"}},
	{ID: "selenium-suite", Client: ClientSelenium, Kind: "suite", Title: "Selenium + JUnit 5 (Java) · the whole suite",
		What: "The two Java tests in one run: signing in passes, adding to the cart fails on a changed selector.",
		Run:  "Shop · Selenium (Java)", Test: "com.demoshop.ShopTest",
		Expect: Expect{Builtin: "LOCATOR_CHANGED", RunStatus: "FAIL", Tests: map[string]string{"Signing in shows the catalog": "PASS", "Add to cart with the old selector": "FAIL"},
			Focus: "Add to cart with the old selector", Incident: "error", Locator: "add-to-cart", Screenshot: true, Network: true, Masked: true,
			Command: "gradlew test"}},
	{ID: "go-suite", Client: ClientGo, Kind: "suite", Title: "Go (go test) · the whole suite",
		What: "Both Go API tests in one run: the catalog passes and creating an order fails with a 500.",
		Run:  "Shop API · Go", Test: "TestCatalog|TestCreateOrder",
		Expect: Expect{Builtin: "BACKEND_TIMEOUT", RunStatus: "FAIL", Tests: map[string]string{"Catalog lists the products": "PASS", "Create an order": "FAIL"},
			Focus: "Create an order", Incident: "backend", Lab: true, Masked: true, Network: true, Command: "go test"}},

	// Noches anteriores (la historia de la demo): las mismas suites con el backend sano.
	{ID: "pytest-night", Client: ClientPytest, Kind: "ok", Hidden: true, Title: "pytest + Playwright · a green night",
		What: "History for the demo: the suite with a working backend.", Run: "Shop · pytest + Playwright", Test: "not old_selector",
		Bugs:   map[string]bool{"orders": false},
		Expect: Expect{Builtin: "none", RunStatus: "PASS", Tests: map[string]string{"test_sign_in_shows_the_catalog[chromium]": "PASS", "test_checkout_pays_the_order[chromium]": "PASS"}, Masked: true}},
	{ID: "playwright-night", Client: ClientPlaywright, Kind: "ok", Hidden: true, Title: "Playwright Test (JS) · a green night",
		What: "History for the demo: the suite with a working backend.", Run: "Shop · Playwright (JS)",
		Test: "signs in and sees the catalog|pays an order with a card", Bugs: map[string]bool{"orders": false},
		Expect: Expect{Builtin: "none", RunStatus: "PASS", Tests: map[string]string{"Shop › signs in and sees the catalog": "PASS", "Shop › pays an order with a card": "PASS"}, Masked: true}},
}

// ScenarioByID returns a scenario of the catalog.
func ScenarioByID(id string) (Scenario, bool) {
	for _, s := range Scenarios {
		if s.ID == id {
			return s, true
		}
	}
	return Scenario{}, false
}

// ShopPlaceholder replaces the shop's origin in the recordings: each replay puts the current one.
const ShopPlaceholder = "http://__shop__"

// RecordShop is where the shop listens while recording.
const RecordShop = "http://127.0.0.1:18180"
