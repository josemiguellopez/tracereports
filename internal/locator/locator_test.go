package locator

import (
	"strings"
	"testing"
)

func TestExtractSelector(t *testing.T) {
	cases := map[string]string{
		`TimeoutError: Locator.click: Timeout 15000ms exceeded.
Call log:
  - waiting for locator("xpath=//button[@data-test='login-submit']").first`: `xpath=//button[@data-test='login-submit']`,
		`Message: no such element: Unable to locate element: {"method":"css selector","selector":"input[data-test-id='create-board-title-input']"}`:                         `input[data-test-id='create-board-title-input']`,
		`Error: waiting for get_by_role("button", name="Guardar")`:                                                                                                          `get_by_role("button", name="Guardar")`,
		"TimeoutError: Waiting for element to be located By(css selector, #boton-que-no-existe)\nWait timed out after 5162ms":                                               `#boton-que-no-existe`,
		"org.openqa.selenium.TimeoutException: Expected condition failed: waiting for presence of element located by: By.xpath: //button[@id='go'] (tried for 5 second(s))": `//button[@id='go']`,
		"NoSuchElementException: By.cssSelector: input[name='user']":                                                                                                        `input[name='user']`,
		`AssertionError: 3 != 4`: ``,
	}
	for in, want := range cases {
		if got := ExtractSelector(in); got != want {
			t.Errorf("ExtractSelector(%.40q) = %q, want %q", in, got, want)
		}
	}
	if !LooksLikeLocatorFailure(`waiting for locator("#x")`) || LooksLikeLocatorFailure("AssertionError: 3 != 4") {
		t.Error("LooksLikeLocatorFailure")
	}
}

func TestSuggestFindsReplacement(t *testing.T) {
	snap := Snapshot{Viewport: Size{1366, 768}, Elements: []Element{
		{Tag: "input", Name: "username", Placeholder: "Username", X: 500, Y: 300, W: 300, H: 40, Visible: true},
		{Tag: "input", Name: "password", Type: "password", Placeholder: "Password", X: 500, Y: 380, W: 300, H: 40, Visible: true},
		{Tag: "button", Type: "submit", Text: "Login", X: 500, Y: 460, W: 300, H: 44, Visible: true},
		{Tag: "a", Text: "Forgot your password?", X: 560, Y: 520, W: 180, H: 20, Visible: true},
		{Tag: "button", Text: "Hidden", Visible: false},
	}}

	got := Suggest(`xpath=//button[@data-test='login-submit']`, snap, 3)
	if len(got) == 0 || got[0].Kind != "role" || got[0].Python != `page.get_by_role("button", name="Login")` {
		t.Fatalf("best suggestion for a broken login button: %+v", got)
	}
	if got[0].Box == nil || got[0].Box.Y != 460 || !strings.Contains(got[0].Reason, "login") {
		t.Errorf("box/reason: %+v", got[0])
	}

	got = Suggest(`input[data-test-id='user-name-field']`, snap, 3)
	if len(got) == 0 || !strings.Contains(got[0].Element, "Username") {
		t.Fatalf("username field: %+v", got)
	}

	withTestID := Snapshot{Elements: []Element{{Tag: "button", TestID: "save-board", TestIDAttr: "data-testid", Text: "Save", W: 10, H: 10, Visible: true}}}
	got = Suggest(`#saveBoardBtn`, withTestID, 3)
	if len(got) != 1 || got[0].Kind != "testid" || got[0].Python != `page.get_by_test_id("save-board")` || got[0].Robustness != "alta" {
		t.Fatalf("data-testid must win: %+v", got)
	}
}

func TestSuggestSkipsLayoutContainers(t *testing.T) {
	snap := Snapshot{Viewport: Size{W: 1366, H: 768}, Elements: []Element{
		{Tag: "div", ID: "app", Text: "Login Username Password Login", Visible: true, W: 1366, H: 768},
		{Tag: "button", Type: "submit", Text: "Login", Visible: true, X: 315, Y: 560, W: 464, H: 46},
	}}
	for _, s := range Suggest("//button[@data-test='login-submit']", snap, 5) {
		if s.Selector == "#app" {
			t.Fatalf("suggested the page wrapper: %+v", s)
		}
	}
}

// A traceback also quotes earlier lines of the test: the selector that failed is the one Playwright
// was waiting for, not the first getter of the text.
func TestExtractSelectorIgnoresEarlierGetters(t *testing.T) {
	trace := `tests/e2e/test_checkout.py:31: in test_checkout_pays_the_order
    page.get_by_test_id("add-to-cart").click()
tests/e2e/test_checkout.py:36: in test_checkout_pays_the_order
    expect(page.locator(".order-confirmed")).to_be_visible(timeout=5000)
AssertionError: Locator expected to be visible
Call log:
  - waiting for locator(".order-confirmed")`
	if got := ExtractSelector("AssertionError: Locator expected to be visible", trace); got != ".order-confirmed" {
		t.Fatalf("got %q", got)
	}
	getter := "Call log:\n  - waiting for get_by_role(\"button\", name=\"Pay\")\n    page.get_by_test_id(\"add-to-cart\")"
	if got := ExtractSelector(getter); got != `get_by_role("button", name="Pay")` {
		t.Fatalf("getter: got %q", got)
	}
}

// Selenium for Python: a TimeoutException says nothing about the locator, the failing line of the
// pytest traceback (">") does. Only for "not found" errors, and only that line.
func TestExtractSelectorPythonSelenium(t *testing.T) {
	trace := "    def test_abrir_pim(driver, paso):\n" +
		"        login(driver, paso, \"Admin\", \"admin123\")\n" +
		"        with paso(\"Abrir el módulo PIM\"):\n" +
		">           esperar(driver, (By.CSS_SELECTOR, \"#menu_pim_viewPimModule\"), segundos=5).click()\n" +
		"\n" +
		"    def esperar(driver, locator, segundos=20):\n" +
		">       return WebDriverWait(driver, segundos).until(EC.visibility_of_element_located(locator))\n" +
		"E       selenium.common.exceptions.TimeoutException: Message:\n"
	msg := "selenium.common.exceptions.TimeoutException: Message: Stacktrace: chromedriver!GetHandleVerifier"
	if got := ExtractSelector(msg, trace); got != "#menu_pim_viewPimModule" {
		t.Fatalf("css: %q", got)
	}
	byID := ">       driver.find_element(By.ID, 'pay').click()\nE   selenium.common.exceptions.NoSuchElementException: Message: no such element"
	if got := ExtractSelector("NoSuchElementException", byID); got != "#pay" {
		t.Fatalf("by id: %q", got)
	}
	// an assertion on an element that was found is not a broken locator
	assertion := ">       assert driver.find_element(By.ID, \"total\").text == \"$10\"\nE       AssertionError: assert '$12' == '$10'"
	if got := ExtractSelector("AssertionError: assert '$12' == '$10'", assertion); got != "" {
		t.Fatalf("assertion is not a locator failure: %q", got)
	}
}

// Each suggestion also comes in Selenium (Python, Java, JS), with the most natural By.
func TestSeleniumSnippets(t *testing.T) {
	get := func(e Element, kind string) Suggestion {
		for _, s := range options(e) {
			if s.Kind == kind {
				return s
			}
		}
		t.Fatalf("no %s suggestion for %+v", kind, e)
		return Suggestion{}
	}
	s := get(Element{Tag: "button", TestID: "add-to-cart", TestIDAttr: "data-testid", Text: "Add to cart"}, "testid")
	if s.SeleniumPython != `driver.find_element(By.CSS_SELECTOR, '[data-testid="add-to-cart"]')` ||
		s.SeleniumJava != `driver.findElement(By.cssSelector("[data-testid=\"add-to-cart\"]"))` ||
		s.SeleniumJS != `driver.findElement(By.css('[data-testid="add-to-cart"]'))` {
		t.Fatalf("testid: %q | %q | %q", s.SeleniumPython, s.SeleniumJava, s.SeleniumJS)
	}
	if s := get(Element{Tag: "a", Text: "PIM"}, "role"); s.SeleniumPython != `driver.find_element(By.XPATH, '//a[normalize-space()="PIM"]')` {
		t.Fatalf("role by text: %q", s.SeleniumPython)
	}
	if s := get(Element{Tag: "input", Type: "password", Label: "Password"}, "role"); s.SeleniumJava !=
		`driver.findElement(By.xpath("//input[@aria-label=\"Password\" or @id=//label[normalize-space()=\"Password\"]/@for]"))` {
		t.Fatalf("role by label: %q", s.SeleniumJava)
	}
	if s := get(Element{Tag: "input", ID: "email"}, "id"); s.SeleniumPython != `driver.find_element(By.ID, "email")` || s.SeleniumJS != `driver.findElement(By.id('email'))` {
		t.Fatalf("id: %q %q", s.SeleniumPython, s.SeleniumJS)
	}
	if s := get(Element{Tag: "input", Name: "username"}, "name"); s.SeleniumJava != `driver.findElement(By.name("username"))` {
		t.Fatalf("name: %q", s.SeleniumJava)
	}
	// both quotes in the text: XPath 1.0 has no escapes, so concat()
	s = get(Element{Tag: "span", Text: `O'Reilly "books"`}, "text")
	if s.SeleniumPython != `driver.find_element(By.XPATH, '//span[normalize-space()=concat("O\'Reilly ", \'"\', "books", \'"\')]')` {
		t.Fatalf("quotes: %q", s.SeleniumPython)
	}
	if got := xpathLit(`it's`); got != `"it's"` {
		t.Fatalf("xpathLit: %q", got)
	}
	if got := javaLit("a\u0001b"); got != `"a\u0001b"` {
		t.Fatalf("javaLit: %q", got)
	}
}

// "#add-to-cart-btn" names a button: the button goes before the "Added to your cart" message, and a
// nav is not proposed for the text of its links.
func TestSuggestPrefersTheNamedButton(t *testing.T) {
	snap := Snapshot{Viewport: Size{W: 1280, H: 800}, Elements: []Element{
		{Tag: "div", Role: "status", ID: "added", Text: "Added to your cart", Visible: true, W: 400, H: 40},
		{Tag: "nav", Label: "Main", Text: "Products Cart 0 Sign in", Visible: true, W: 300, H: 40},
		{Tag: "button", Type: "button", TestID: "add-to-cart", TestIDAttr: "data-testid", Text: "Add to cart", Visible: true, W: 120, H: 40},
	}}
	got := Suggest("#add-to-cart-btn", snap, 5)
	if len(got) == 0 || got[0].Kind != "testid" || !strings.Contains(got[0].Selector, "add-to-cart") {
		t.Fatalf("first: %+v", got)
	}
	for _, s := range got {
		if strings.HasPrefix(s.Element, "<nav") {
			t.Fatalf("a nav is not a replacement for a button: %+v", s)
		}
	}
	for _, sel := range []string{"#add-to-cart-btn", ".submit-button", "#btn-pay", "button.x"} {
		if selectorTag(sel) != "button" {
			t.Fatalf("%s should hint a button", sel)
		}
	}
	if selectorTag("#subtitle") != "" || selectorTag("#rebuttons-list") != "" {
		t.Fatal("no button hint in unrelated ids")
	}
}

// OrangeHRM: "#menu_pim_viewPimModule" broke; the PIM link goes before the navigation landmarks,
// whose text is the text of all their links.
func TestSuggestPrefersTheLinkOverNavigationLandmarks(t *testing.T) {
	snap := Snapshot{Viewport: Size{W: 1280, H: 800}, Elements: []Element{
		{Tag: "nav", Role: "navigation", Label: "Sidepanel", Text: "Admin PIM Leave Time", Visible: true, W: 250, H: 600},
		{Tag: "a", Text: "PIM", Visible: true, W: 200, H: 40},
		{Tag: "nav", Role: "navigation", Label: "Topbar Menu", Text: "User Management Job", Visible: true, W: 900, H: 50},
	}}
	got := Suggest("#menu_pim_viewPimModule", snap, 5)
	if len(got) == 0 || got[0].Element != "<a> PIM" {
		t.Fatalf("first: %+v", got)
	}
}
