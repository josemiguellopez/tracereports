// Package locator recommends robust replacement selectors when a test fails because an
// element could not be found (locator drift). It works on a snapshot of the page taken by
// the client at the moment of the failure: every relevant element with its attributes and
// its position on screen (so the UI can draw a bounding box over the failure screenshot).
package locator

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// Snapshot is the page state captured by the client when the test failed.
type Snapshot struct {
	URL      string    `json:"url"`
	Title    string    `json:"title"`
	Viewport Size      `json:"viewport"`
	Elements []Element `json:"elements"`
}

// Size is a viewport in CSS pixels.
type Size struct {
	W int `json:"w"`
	H int `json:"h"`
}

// Element is one candidate element of the page.
type Element struct {
	Tag         string `json:"tag"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Role        string `json:"role"`
	TestID      string `json:"testid"`
	TestIDAttr  string `json:"testid_attr"` // data-testid | data-test-id | data-test
	Label       string `json:"label"`       // aria-label o <label> asociado
	Placeholder string `json:"placeholder"`
	Type        string `json:"type"`
	Text        string `json:"text"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	W           int    `json:"w"`
	H           int    `json:"h"`
	Visible     bool   `json:"visible"`
}

// Box is an element position on the screenshot, in CSS pixels of the viewport.
type Box struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// Suggestion is a replacement selector for the broken one.
type Suggestion struct {
	Kind       string `json:"kind"`       // testid | role | label | placeholder | id | name | text
	Selector   string `json:"selector"`   // selector CSS/Playwright legible
	Python     string `json:"python"`     // Playwright Python
	JS         string `json:"js"`         // Playwright JS/TS
	Robustness string `json:"robustness"` // alta | media | baja
	Reason     string `json:"reason"`
	Score      int    `json:"score"`
	Element    string `json:"element"` // descripción corta del elemento
	Box        *Box   `json:"box,omitempty"`
}

// ─── extracción del selector roto ──────────────────────────────────────────

var selectorPatterns = []*regexp.Regexp{
	regexp.MustCompile(`waiting for (?:get_by_\w+\(.*?\)|getBy\w+\(.*?\)|locator\("((?:[^"\\]|\\.)+)"\))`),
	regexp.MustCompile(`locator\("((?:[^"\\]|\\.)+)"\)`),
	regexp.MustCompile(`locator\('((?:[^'\\]|\\.)+)'\)`),
	regexp.MustCompile(`"selector"\s*:\s*"((?:[^"\\]|\\.)+)"`), // Selenium NoSuchElement
	// selenium-webdriver (JS): "Waiting for element to be located By(css selector, #x)"
	regexp.MustCompile(`By\((?:css selector|xpath|id|name|class name|link text|partial link text|tag name),\s*(.+?)\)(?:\s|$)`),
	// Selenium Java: "located by: By.xpath: //button (tried for 5 second(s))"
	regexp.MustCompile(`(?m)By\.(?:cssSelector|xpath|id|name|className|linkText|partialLinkText|tagName):\s*(.+?)\s*(?:\(tried|$)`),
	regexp.MustCompile(`(?i)(?:unable to (?:locate|find) element|no such element)[^:]*:\s*(\S+)`), // genérico
	regexp.MustCompile(`(?i)(?:selector|locator|xpath)\s*[:=]\s*['"]((?:[^'"\\]|\\.)+)['"]`),
}

var gettersPattern = regexp.MustCompile(`(get_by_\w+|getBy\w+)\(([^)]*)\)`)

// ExtractSelector finds the selector that failed in an error message / stack trace.
func ExtractSelector(texts ...string) string {
	for _, text := range texts {
		if m := gettersPattern.FindString(text); m != "" && strings.Contains(text, "waiting for") {
			return m
		}
		for _, re := range selectorPatterns {
			if m := re.FindStringSubmatch(text); len(m) > 1 && m[1] != "" {
				return strings.ReplaceAll(m[1], `\"`, `"`)
			}
		}
	}
	return ""
}

// LooksLikeLocatorFailure reports whether an error is about an element that was not found.
func LooksLikeLocatorFailure(text string) bool {
	t := strings.ToLower(text)
	for _, k := range []string{"waiting for locator", "waiting for get_by", "waiting for getby", "no such element",
		"unable to locate element", "element not found", "nosuchelement", "stale element", "strict mode violation",
		"not visible", "timeout esperando", "no se encontró el elemento"} {
		if strings.Contains(t, k) {
			return true
		}
	}
	return false
}

// ─── recomendación ─────────────────────────────────────────────────────────

var stopwords = map[string]bool{
	"xpath": true, "css": true, "input": true, "button": true, "div": true, "span": true, "data": true,
	"test": true, "id": true, "testid": true, "class": true, "name": true, "type": true, "contains": true,
	"text": true, "normalize": true, "space": true, "and": true, "or": true, "the": true, "nth": true,
	"child": true, "of": true, "first": true, "last": true, "get": true, "by": true, "role": true,
	"label": true, "placeholder": true, "locator": true, "a": true, "li": true, "ul": true, "form": true,
	"submit": false, "oxd": true, "el": true, "aria": true, "following": true, "sibling": true, "parent": true,
}

var wordSplit = regexp.MustCompile(`[A-Za-z][a-z]+|[A-Z]+(?:[a-z]+)?|\d+`)

// tokens splits a selector or attribute into lowercase words (camelCase, kebab, snake).
func tokens(s string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }) {
		for _, w := range wordSplit.FindAllString(part, -1) {
			w = strings.ToLower(w)
			if len(w) < 3 || stopwords[w] || seen[w] {
				continue
			}
			seen[w] = true
			out = append(out, w)
		}
	}
	return out
}

var tagInSelector = regexp.MustCompile(`(?i)(?:^|[\s/>+~(])(input|button|a|select|textarea|label|h[1-6])(?:[\[.#:\s)]|$)`)

func selectorTag(sel string) string {
	if m := tagInSelector.FindStringSubmatch(sel); len(m) > 1 {
		return strings.ToLower(m[1])
	}
	if strings.Contains(sel, `"button"`) || strings.Contains(sel, `'button'`) {
		return "button"
	}
	if strings.Contains(sel, `"textbox"`) || strings.Contains(sel, `'textbox'`) {
		return "input"
	}
	return ""
}

func implicitRole(e Element) string {
	if e.Role != "" {
		return e.Role
	}
	switch e.Tag {
	case "button":
		return "button"
	case "a":
		return "link"
	case "select":
		return "combobox"
	case "textarea":
		return "textbox"
	case "h1", "h2", "h3", "h4", "h5", "h6":
		return "heading"
	case "input":
		switch e.Type {
		case "checkbox":
			return "checkbox"
		case "radio":
			return "radio"
		case "submit", "button":
			return "button"
		case "", "text", "email", "password", "search", "tel", "url", "number":
			return "textbox"
		}
	}
	return ""
}

func accessibleName(e Element) string {
	for _, v := range []string{e.Label, e.Text, e.Placeholder} {
		if v = strings.TrimSpace(v); v != "" && len(v) <= 60 {
			return v
		}
	}
	return ""
}

var generatedID = regexp.MustCompile(`\d{3,}|[a-f0-9]{8,}|^(?:mui|ember|react|ng|v)-?[a-z0-9]*\d`)

func q(s string) string { return strings.ReplaceAll(s, `"`, `\"`) }

// options returns the selector strategies available for an element, most robust first.
func options(e Element) []Suggestion {
	var out []Suggestion
	if e.TestID != "" {
		attr := e.TestIDAttr
		if attr == "" {
			attr = "data-testid"
		}
		css := fmt.Sprintf(`[%s="%s"]`, attr, q(e.TestID))
		s := Suggestion{Kind: "testid", Selector: css, Robustness: "alta", Reason: "atributo de test dedicado: no cambia con el diseño ni con el texto",
			Python: fmt.Sprintf(`page.locator('%s')`, css), JS: fmt.Sprintf(`page.locator('%s')`, css)}
		if attr == "data-testid" {
			s.Python = fmt.Sprintf(`page.get_by_test_id("%s")`, q(e.TestID))
			s.JS = fmt.Sprintf(`page.getByTestId('%s')`, e.TestID)
		}
		out = append(out, s)
	}
	if role, name := implicitRole(e), accessibleName(e); role != "" && name != "" {
		out = append(out, Suggestion{Kind: "role", Robustness: "alta",
			Selector: fmt.Sprintf(`role=%s[name="%s"]`, role, q(name)),
			Python:   fmt.Sprintf(`page.get_by_role("%s", name="%s")`, role, q(name)),
			JS:       fmt.Sprintf(`page.getByRole('%s', { name: '%s' })`, role, strings.ReplaceAll(name, "'", `\'`)),
			Reason:   "rol + nombre accesible: así lo encuentra el usuario y sobrevive a cambios de CSS"})
	}
	if e.Label != "" && e.Tag != "button" && e.Tag != "a" {
		out = append(out, Suggestion{Kind: "label", Robustness: "alta", Selector: fmt.Sprintf(`[aria-label="%s"]`, q(e.Label)),
			Python: fmt.Sprintf(`page.get_by_label("%s")`, q(e.Label)), JS: fmt.Sprintf(`page.getByLabel('%s')`, e.Label),
			Reason: "etiqueta accesible del campo"})
	}
	if e.Placeholder != "" {
		out = append(out, Suggestion{Kind: "placeholder", Robustness: "media", Selector: fmt.Sprintf(`[placeholder="%s"]`, q(e.Placeholder)),
			Python: fmt.Sprintf(`page.get_by_placeholder("%s")`, q(e.Placeholder)), JS: fmt.Sprintf(`page.getByPlaceholder('%s')`, e.Placeholder),
			Reason: "placeholder visible; cambia si se edita el texto del campo"})
	}
	if e.ID != "" && !generatedID.MatchString(e.ID) {
		css := "#" + e.ID
		out = append(out, Suggestion{Kind: "id", Robustness: "media", Selector: css, Python: fmt.Sprintf(`page.locator("%s")`, css),
			JS: fmt.Sprintf(`page.locator('%s')`, css), Reason: "id estable (no parece autogenerado)"})
	}
	if e.Name != "" {
		css := fmt.Sprintf(`%s[name="%s"]`, e.Tag, q(e.Name))
		out = append(out, Suggestion{Kind: "name", Robustness: "media", Selector: css, Python: fmt.Sprintf(`page.locator('%s')`, css),
			JS: fmt.Sprintf(`page.locator('%s')`, css), Reason: "atributo name del formulario"})
	}
	if t := strings.TrimSpace(e.Text); t != "" && len(t) <= 40 && implicitRole(e) == "" {
		out = append(out, Suggestion{Kind: "text", Robustness: "baja", Selector: fmt.Sprintf(`text="%s"`, q(t)),
			Python: fmt.Sprintf(`page.get_by_text("%s", exact=True)`, q(t)), JS: fmt.Sprintf(`page.getByText('%s', { exact: true })`, t),
			Reason: "texto visible; frágil ante cambios de copy o idioma"})
	}
	return out
}

func describe(e Element) string {
	d := "<" + e.Tag
	if e.Type != "" {
		d += ` type="` + e.Type + `"`
	}
	d += ">"
	if n := accessibleName(e); n != "" {
		d += " " + n
	}
	return d
}

// Suggest ranks elements of the snapshot that likely replace the broken selector and
// returns up to max suggestions (one per element, its most robust strategy first).
// isLayoutContainer reports wrappers like <div id="app"> that cover most of the page: their
// text contains every word of the screen, so they would "match" any broken selector.
func isLayoutContainer(e Element, vp Size) bool {
	if e.TestID != "" || implicitRole(e) != "" || vp.W == 0 || vp.H == 0 {
		return false
	}
	return float64(e.W)*float64(e.H) > 0.4*float64(vp.W)*float64(vp.H)
}

func Suggest(failed string, snap Snapshot, max int) []Suggestion {
	want := tokens(failed)
	tag := selectorTag(failed)
	type scored struct {
		e     Element
		score int
		hits  []string
	}
	var cands []scored
	for _, e := range snap.Elements {
		if !e.Visible || e.W == 0 || e.H == 0 || isLayoutContainer(e, snap.Viewport) {
			continue
		}
		hay := strings.ToLower(strings.Join([]string{e.TestID, e.ID, e.Name, e.Label, e.Placeholder, e.Text, e.Type}, " "))
		score := 0
		var hits []string
		for _, w := range want {
			if strings.Contains(hay, w) {
				score += 3
				hits = append(hits, w)
			}
		}
		if tag != "" && (e.Tag == tag || (tag == "button" && implicitRole(e) == "button")) {
			score += 2
		}
		if tag == "" && implicitRole(e) != "" {
			score++ // sin pistas del tag, preferir elementos interactivos
		}
		if len(hits) == 0 && (tag == "" || score < 2) {
			continue
		}
		cands = append(cands, scored{e, score, hits})
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].score > cands[j].score })

	var out []Suggestion
	seen := map[string]bool{}
	for _, c := range cands {
		opts := options(c.e)
		if len(opts) == 0 {
			continue
		}
		s := opts[0]
		if seen[s.Selector] {
			continue
		}
		seen[s.Selector] = true
		s.Score = c.score
		s.Element = describe(c.e)
		s.Box = &Box{X: c.e.X, Y: c.e.Y, W: c.e.W, H: c.e.H}
		if len(c.hits) > 0 {
			s.Reason += fmt.Sprintf(" · coincide con «%s» del selector roto", strings.Join(c.hits, "», «"))
		}
		out = append(out, s)
		if len(out) == max {
			break
		}
	}
	return out
}
