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
)

// Scenario is one recorded situation and what TraceReports must do with it.
type Scenario struct {
	ID     string `json:"id"`
	Client string `json:"client"`
	Kind   string `json:"kind"` // ok | nok | flaky
	Title  string `json:"title"`
	What   string `json:"what"` // qué muestra, para el playground
	// Run is the run name; Test selects the test to record (pytest -k, Playwright -g, Gradle
	// --tests, go test -run).
	Run  string `json:"run"`
	Test string `json:"-"`
	// Bugs of the shop while recording (and while checking the Lab): the orders one is on by default.
	Bugs   map[string]bool `json:"-"`
	Expect Expect          `json:"expect"`
}

// Expect is what TraceReports must show after receiving a scenario.
type Expect struct {
	RunStatus string            `json:"run_status"`         // PASS | FAIL
	Tests     map[string]string `json:"tests"`              // nombre del test -> PASS | FAIL | SKIP
	Incident  string            `json:"incident,omitempty"` // backend | error: el incidente que agrupa el fallo
	// Lab: the failed backend call is resent from a capsule and reproduces while the shop is broken.
	Lab bool `json:"lab,omitempty"`
	// Locator: TraceReports suggests a new selector for the broken one.
	Locator string `json:"locator,omitempty"`
	// Flaky: the test passed after a retry.
	Flaky bool `json:"flaky,omitempty"`
	// Masked: the shop password never reaches the stored evidence.
	Masked bool `json:"masked,omitempty"`
	// Command: the command that runs the test locally contains this.
	Command    string `json:"command,omitempty"`
	Screenshot bool   `json:"screenshot,omitempty"`
	Network    bool   `json:"network,omitempty"`
}

// Scenarios is the catalog, in the order the playground shows it.
var Scenarios = []Scenario{
	{ID: "rest-ok", Client: ClientREST, Kind: "ok", Title: "REST API · a passing run",
		What:   "Any language can report over the REST API: a run with two passing tests and their steps.",
		Run:    "Shop smoke · REST API",
		Expect: Expect{RunStatus: "PASS", Tests: map[string]string{"health check answers": "PASS", "catalog lists 3 products": "PASS"}}},
	{ID: "rest-nok-backend", Client: ClientREST, Kind: "nok", Title: "REST API · backend answers 500",
		What:   "A test reported over REST with its network: the failing call becomes a backend incident you can resend in the Lab.",
		Run:    "Orders · REST API",
		Expect: Expect{RunStatus: "FAIL", Tests: map[string]string{"create an order": "FAIL"}, Incident: "backend", Lab: true, Masked: true, Network: true}},

	{ID: "pytest-ok", Client: ClientPytest, Kind: "ok", Title: "pytest + Playwright · sign in",
		What: "A real pytest-playwright test: steps, a screenshot and every call the browser made, with the password masked.",
		Run:  "Shop · pytest + Playwright", Test: "test_sign_in_shows_the_catalog",
		Expect: Expect{RunStatus: "PASS", Tests: map[string]string{"test_sign_in_shows_the_catalog[chromium]": "PASS"}, Masked: true, Network: true, Screenshot: true,
			Command: "pytest"}},
	{ID: "pytest-nok-backend", Client: ClientPytest, Kind: "nok", Title: "pytest + Playwright · checkout fails (500)",
		What: "The order is never confirmed because POST /api/orders answers 500: backend incident, screenshot, console and the Lab.",
		Run:  "Shop · pytest + Playwright", Test: "test_checkout_pays_the_order",
		Expect: Expect{RunStatus: "FAIL", Tests: map[string]string{"test_checkout_pays_the_order[chromium]": "FAIL"}, Incident: "backend", Lab: true, Masked: true,
			Network: true, Screenshot: true, Command: "pytest"}},
	{ID: "pytest-nok-selector", Client: ClientPytest, Kind: "nok", Title: "pytest + Playwright · a selector changed",
		What: "The test still uses #add-to-cart-btn; the page now has data-testid=\"add-to-cart\": TraceReports suggests the new locator.",
		Run:  "Shop · pytest + Playwright", Test: "test_add_to_cart_with_the_old_selector",
		Expect: Expect{RunStatus: "FAIL", Tests: map[string]string{"test_add_to_cart_with_the_old_selector[chromium]": "FAIL"}, Incident: "error", Locator: "add-to-cart",
			Screenshot: true, Command: "pytest"}},

	{ID: "playwright-ok", Client: ClientPlaywright, Kind: "ok", Title: "Playwright Test (JS) · sign in",
		What: "A real Playwright Test run with the TraceReports reporter: steps, attachments and network.",
		Run:  "Shop · Playwright (JS)", Test: "signs in and sees the catalog",
		Expect: Expect{RunStatus: "PASS", Tests: map[string]string{"Shop › signs in and sees the catalog": "PASS"}, Masked: true, Network: true,
			Command: "npx playwright test"}},
	{ID: "playwright-nok-backend", Client: ClientPlaywright, Kind: "nok", Title: "Playwright Test (JS) · checkout fails (500)",
		What: "Same backend failure seen from JavaScript: it groups into a backend incident and the Lab reproduces it.",
		Run:  "Shop · Playwright (JS)", Test: "pays an order with a card",
		Expect: Expect{RunStatus: "FAIL", Tests: map[string]string{"Shop › pays an order with a card": "FAIL"}, Incident: "backend", Lab: true, Masked: true,
			Network: true, Command: "npx playwright test"}},
	{ID: "playwright-flaky", Client: ClientPlaywright, Kind: "flaky", Title: "Playwright Test (JS) · passes on the retry",
		What: "Recommendations fail on the first call (503) and work on the second: the test is marked as passed after retry (flaky).",
		Run:  "Shop · Playwright (JS)", Test: "shows recommendations on the product page", Bugs: map[string]bool{"orders": true, "flaky": true},
		Expect: Expect{RunStatus: "PASS", Tests: map[string]string{"Shop › shows recommendations on the product page": "PASS"}, Flaky: true, Network: true}},

	{ID: "selenium-ok", Client: ClientSelenium, Kind: "ok", Title: "Selenium + JUnit 5 (Java) · sign in",
		What: "A real Selenium WebDriver test with the JUnit 5 extension: steps and a screenshot.",
		Run:  "Shop · Selenium (Java)", Test: "com.demoshop.ShopTest.signInShowsCatalog",
		Expect: Expect{RunStatus: "PASS", Tests: map[string]string{"Signing in shows the catalog": "PASS"}, Screenshot: true, Command: "gradlew test"}},
	{ID: "selenium-nok-selector", Client: ClientSelenium, Kind: "nok", Title: "Selenium + JUnit 5 (Java) · a selector changed",
		What: "NoSuchElementException for #add-to-cart-btn: screenshot, page snapshot and the suggested locator.",
		Run:  "Shop · Selenium (Java)", Test: "com.demoshop.ShopTest.addToCartWithOldSelector",
		Expect: Expect{RunStatus: "FAIL", Tests: map[string]string{"Add to cart with the old selector": "FAIL"}, Incident: "error", Locator: "add-to-cart",
			Screenshot: true, Command: "gradlew test"}},

	{ID: "go-ok", Client: ClientGo, Kind: "ok", Title: "Go (go test) · API passes",
		What: "An API test in Go with the Go client: the call it made appears in the Network tab.",
		Run:  "Shop API · Go", Test: "TestCatalog",
		Expect: Expect{RunStatus: "PASS", Tests: map[string]string{"Catalog lists the products": "PASS"}, Network: true, Command: "go test"}},
	{ID: "go-nok-backend", Client: ClientGo, Kind: "nok", Title: "Go (go test) · API answers 500",
		What: "The API test that creates an order fails with a 500: backend incident and the Lab, from Go.",
		Run:  "Shop API · Go", Test: "TestCreateOrder",
		Expect: Expect{RunStatus: "FAIL", Tests: map[string]string{"Create an order": "FAIL"}, Incident: "backend", Lab: true, Masked: true, Network: true,
			Command: "go test"}},

	{ID: "junit-import", Client: ClientJUnit, Kind: "nok", Title: "JUnit XML import (Maven Surefire)",
		What:   "No client at all: a Surefire XML report imported with POST /api/v1/import/junit (passed, failed and skipped tests).",
		Run:    "Inventory · Maven (JUnit XML)",
		Expect: Expect{RunStatus: "FAIL", Tests: map[string]string{"reservesStock": "PASS", "rejectsNegativeQuantity": "FAIL", "syncsWithWarehouse": "SKIP"}}},
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
