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
