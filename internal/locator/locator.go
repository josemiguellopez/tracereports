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
	Kind     string `json:"kind"`     // testid | role | label | placeholder | id | name | text
	Selector string `json:"selector"` // selector CSS/Playwright legible
	Python   string `json:"python"`   // Playwright Python
	JS       string `json:"js"`       // Playwright JS/TS
	// Selenium (driver.find_element / findElement) with the most natural By for the strategy:
	// CSS, ID, NAME, or XPath when Selenium has no equivalent (role + name, label, text).
	SeleniumPython string `json:"selenium_python"`
	SeleniumJava   string `json:"selenium_java"`
	SeleniumJS     string `json:"selenium_js"`
	Robustness     string `json:"robustness"` // alta | media | baja
	Reason         string `json:"reason"`
	Score          int    `json:"score"`
	Element        string `json:"element"` // descripción corta del elemento
	Box            *Box   `json:"box,omitempty"`
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

// waitingGetter is the getter Playwright was waiting for when it failed ("waiting for
// get_by_role(...)"). Only the one right after "waiting for": a traceback also quotes earlier lines
// of the test (an earlier click that worked), and those are not the selector that failed.
var waitingGetter = regexp.MustCompile(`waiting for ((?:get_by_\w+|getBy\w+)\([^)]*\))`)

// pySeleniumBy is the locator on the line that failed in a pytest traceback (the one marked with
// ">"): Selenium for Python says nothing about it in a TimeoutException ("Message: "), only the
// test's code does: `esperar(driver, (By.CSS_SELECTOR, "#menu"))`, `driver.find_element(By.ID, "pay")`.
var pySeleniumBy = regexp.MustCompile(`(?m)^>.*?By\.(CSS_SELECTOR|XPATH|ID|NAME|CLASS_NAME|LINK_TEXT|PARTIAL_LINK_TEXT|TAG_NAME)\s*,\s*(?:"((?:[^"\\]|\\.)+)"|'((?:[^'\\]|\\.)+)')`)

// pySeleniumNotFound: the Selenium for Python errors of an element that never showed up.
var pySeleniumNotFound = regexp.MustCompile(`selenium\.common\.exceptions\.(?:TimeoutException|NoSuchElementException)`)

// ExtractSelector finds the selector that failed in an error message / stack trace.
func ExtractSelector(texts ...string) string {
	for _, text := range texts {
		if m := waitingGetter.FindStringSubmatch(text); m != nil {
			return m[1]
		}
		for _, re := range selectorPatterns {
			if m := re.FindStringSubmatch(text); len(m) > 1 && m[1] != "" {
				return strings.ReplaceAll(m[1], `\"`, `"`)
			}
		}
	}
	if all := strings.Join(texts, "\n"); pySeleniumNotFound.MatchString(all) {
		if m := pySeleniumBy.FindStringSubmatch(all); m != nil {
			return pySeleniumSelector(m[1], m[2]+m[3])
		}
	}
	return ""
}

// pySeleniumSelector writes a Selenium for Python locator as the CSS or XPath it means.
func pySeleniumSelector(by, value string) string {
	switch by {
	case "ID":
		return "#" + value
	case "NAME":
		return `[name="` + value + `"]`
	case "CLASS_NAME":
		return "." + value
	case "LINK_TEXT", "PARTIAL_LINK_TEXT":
		return "text=" + value
	default: // CSS_SELECTOR, XPATH, TAG_NAME
		return value
	}
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

// namedButton: an id or class that says it is a button (#add-to-cart-btn, .submit-button).
var namedButton = regexp.MustCompile(`(?i)[#.\-_](?:btn|button)(?:$|[-_.\s\]])|[#.](?:btn|button)[-_]`)

// containerRoles are roles of regions whose text is their children's, like containerTags.
var containerRoles = map[string]bool{"navigation": true, "banner": true, "main": true, "contentinfo": true,
	"complementary": true, "region": true, "list": true, "menubar": true, "tablist": true, "toolbar": true, "group": true}

// interactiveRoles are the roles a test usually clicks or types into.
var interactiveRoles = map[string]bool{"button": true, "link": true, "textbox": true, "checkbox": true, "radio": true,
	"combobox": true, "menuitem": true, "tab": true, "option": true, "switch": true, "searchbox": true}

// containerTags are elements whose text is their children's: they are not proposed for it.
var containerTags = map[string]bool{"nav": true, "header": true, "footer": true, "main": true, "section": true,
	"aside": true, "form": true, "ul": true, "ol": true, "table": true}

func selectorTag(sel string) string {
	if m := tagInSelector.FindStringSubmatch(sel); len(m) > 1 {
		return strings.ToLower(m[1])
	}
	if strings.Contains(sel, `"button"`) || strings.Contains(sel, `'button'`) || namedButton.MatchString(sel) {
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

// Literales para el código generado. Cada contexto tiene su propio escape: un valor del DOM
// (O'Reilly, comillas, barras, saltos de línea) nunca cierra el literal ni cambia el código.

// lit is a JavaScript or Python string literal delimited by quote (' or "): both languages accept
// these escapes. Normal values come out exactly as before.
func lit(s string, quote byte) string {
	var b strings.Builder
	b.WriteByte(quote)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case r == rune(quote):
			b.WriteByte('\\')
			b.WriteByte(quote)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		case r == 0x2028 || r == 0x2029: // separadores de línea: JavaScript los trata como salto
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte(quote)
	return b.String()
}

func js(s string) string    { return lit(s, '\'') } // 'valor' (estilo de los snippets JS)
func py(s string) string    { return lit(s, '"') }  // "valor" (estilo de los snippets Python)
func pySel(s string) string { return lit(s, '\'') } // 'selector' (Python, con comillas dobles adentro)

// css is a CSS string ("…") for attribute values in selectors: \ and " escaped, control
// characters and line breaks as hex escapes, as CSS requires.
func css(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\%x `, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

var cssIdent = regexp.MustCompile(`^-?[A-Za-z_][A-Za-z0-9_-]*$`)

// idSelector is #id for a plain identifier and [id="…"] for anything else (O'Reilly, espacios…).
func idSelector(id string) string {
	if cssIdent.MatchString(id) {
		return "#" + id
	}
	return "[id=" + css(id) + "]"
}

// javaLit is a Java string literal: Java has no \x escapes, so control characters go as \uXXXX.
func javaLit(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch {
		case r == '\\' || r == '"':
			b.WriteByte('\\')
			b.WriteRune(r)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f || r == 0x2028 || r == 0x2029:
			fmt.Fprintf(&b, `\u%04x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// xpathLit is an XPath string literal. XPath 1.0 has no escapes: a value with both kinds of quotes
// is built with concat().
func xpathLit(s string) string {
	switch {
	case !strings.Contains(s, `"`):
		return `"` + s + `"`
	case !strings.Contains(s, `'`):
		return `'` + s + `'`
	}
	parts := strings.Split(s, `"`)
	out := make([]string, 0, 2*len(parts))
	for i, p := range parts {
		if i > 0 {
			out = append(out, `'"'`)
		}
		if p != "" {
			out = append(out, `"`+p+`"`)
		}
	}
	return "concat(" + strings.Join(out, ", ") + ")"
}

// by is a Selenium locator in each language: Python (By.CSS_SELECTOR, '…'), Java and JS.
type by struct{ py, java, js string }

func byCSS(sel string) by {
	return by{"By.CSS_SELECTOR, " + pySel(sel), "By.cssSelector(" + javaLit(sel) + ")", "By.css(" + js(sel) + ")"}
}
func byXPath(x string) by {
	return by{"By.XPATH, " + pySel(x), "By.xpath(" + javaLit(x) + ")", "By.xpath(" + js(x) + ")"}
}
func byID(id string) by {
	return by{"By.ID, " + py(id), "By.id(" + javaLit(id) + ")", "By.id(" + js(id) + ")"}
}
func byName(n string) by {
	return by{"By.NAME, " + py(n), "By.name(" + javaLit(n) + ")", "By.name(" + js(n) + ")"}
}

// withSelenium fills the Selenium snippets of s.
func withSelenium(s Suggestion, b by) Suggestion {
	s.SeleniumPython = "driver.find_element(" + b.py + ")"
	s.SeleniumJava = "driver.findElement(" + b.java + ")"
	s.SeleniumJS = "driver.findElement(" + b.js + ")"
	return s
}

// xpathBase is the element in XPath: its explicit role, or its tag.
func xpathBase(e Element) string {
	if e.Role != "" {
		return "//*[@role=" + xpathLit(e.Role) + "]"
	}
	tag := e.Tag
	if tag == "" {
		tag = "*"
	}
	return "//" + tag
}

// labelXPath matches an element by its accessible label: aria-label, or a <label for> pointing at it.
func labelXPath(e Element, label string) string {
	l := xpathLit(label)
	return xpathBase(e) + "[@aria-label=" + l + " or @id=//label[normalize-space()=" + l + "]/@for]"
}

// nameXPath matches an element by the accessible name the recommender used (label, text or placeholder).
func nameXPath(e Element) string {
	if l := strings.TrimSpace(e.Label); l != "" && len(l) <= 60 {
		return labelXPath(e, l)
	}
	if t := strings.TrimSpace(e.Text); t != "" && len(t) <= 60 {
		return xpathBase(e) + "[normalize-space()=" + xpathLit(t) + "]"
	}
	return xpathBase(e) + "[@placeholder=" + xpathLit(strings.TrimSpace(e.Placeholder)) + "]"
}

// options returns the selector strategies available for an element, most robust first.
func options(e Element) []Suggestion {
	out := playwrightOptions(e)
	for i, s := range out {
		switch s.Kind {
		case "testid", "placeholder":
			out[i] = withSelenium(s, byCSS(s.Selector))
		case "role":
			out[i] = withSelenium(s, byXPath(nameXPath(e)))
		case "label":
			out[i] = withSelenium(s, byXPath(labelXPath(e, e.Label)))
		case "id":
			if cssIdent.MatchString(e.ID) {
				out[i] = withSelenium(s, byID(e.ID))
			} else {
				out[i] = withSelenium(s, byCSS(s.Selector))
			}
		case "name":
			out[i] = withSelenium(s, byName(e.Name))
		case "text":
			out[i] = withSelenium(s, byXPath(xpathBase(e)+"[normalize-space()="+xpathLit(strings.TrimSpace(e.Text))+"]"))
		}
	}
	return out
}

// playwrightOptions are the strategies with their Playwright snippets.
func playwrightOptions(e Element) []Suggestion {
	var out []Suggestion
	if e.TestID != "" {
		attr := e.TestIDAttr
		if attr == "" {
			attr = "data-testid"
		}
		sel := fmt.Sprintf(`[%s=%s]`, attr, css(e.TestID))
		s := Suggestion{Kind: "testid", Selector: sel, Robustness: "alta", Reason: "atributo de test dedicado: no cambia con el diseño ni con el texto",
			Python: "page.locator(" + pySel(sel) + ")", JS: "page.locator(" + js(sel) + ")"}
		if attr == "data-testid" {
			s.Python = "page.get_by_test_id(" + py(e.TestID) + ")"
			s.JS = "page.getByTestId(" + js(e.TestID) + ")"
		}
		out = append(out, s)
	}
	if role, name := implicitRole(e), accessibleName(e); role != "" && name != "" {
		out = append(out, Suggestion{Kind: "role", Robustness: "alta",
			Selector: fmt.Sprintf(`role=%s[name=%s]`, role, css(name)),
			Python:   fmt.Sprintf(`page.get_by_role(%s, name=%s)`, py(role), py(name)),
			JS:       fmt.Sprintf(`page.getByRole(%s, { name: %s })`, js(role), js(name)),
			Reason:   "rol + nombre accesible: así lo encuentra el usuario y sobrevive a cambios de CSS"})
	}
	if e.Label != "" && e.Tag != "button" && e.Tag != "a" {
		out = append(out, Suggestion{Kind: "label", Robustness: "alta", Selector: "[aria-label=" + css(e.Label) + "]",
			Python: "page.get_by_label(" + py(e.Label) + ")", JS: "page.getByLabel(" + js(e.Label) + ")",
			Reason: "etiqueta accesible del campo"})
	}
	if e.Placeholder != "" {
		out = append(out, Suggestion{Kind: "placeholder", Robustness: "media", Selector: "[placeholder=" + css(e.Placeholder) + "]",
			Python: "page.get_by_placeholder(" + py(e.Placeholder) + ")", JS: "page.getByPlaceholder(" + js(e.Placeholder) + ")",
			Reason: "placeholder visible; cambia si se edita el texto del campo"})
	}
	if e.ID != "" && !generatedID.MatchString(e.ID) {
		sel := idSelector(e.ID)
		out = append(out, Suggestion{Kind: "id", Robustness: "media", Selector: sel, Python: "page.locator(" + py(sel) + ")",
			JS: "page.locator(" + js(sel) + ")", Reason: "id estable (no parece autogenerado)"})
	}
	if e.Name != "" {
		sel := e.Tag + "[name=" + css(e.Name) + "]"
		out = append(out, Suggestion{Kind: "name", Robustness: "media", Selector: sel, Python: "page.locator(" + pySel(sel) + ")",
			JS: "page.locator(" + js(sel) + ")", Reason: "atributo name del formulario"})
	}
	if t := strings.TrimSpace(e.Text); t != "" && len(t) <= 40 && implicitRole(e) == "" {
		out = append(out, Suggestion{Kind: "text", Robustness: "baja", Selector: "text=" + css(t),
			Python: "page.get_by_text(" + py(t) + ", exact=True)", JS: "page.getByText(" + js(t) + ", { exact: true })",
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
		text := e.Text
		if containerTags[e.Tag] || containerRoles[e.Role] {
			text = "" // el texto de un contenedor es el de sus hijos (un nav con "Cart 0" no es el botón)
		}
		hay := strings.ToLower(strings.Join([]string{e.TestID, e.ID, e.Name, e.Label, e.Placeholder, text, e.Type}, " "))
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
		if tag == "" && interactiveRoles[implicitRole(e)] {
			score++ // sin pistas del tag, preferir elementos interactivos (no landmarks ni mensajes)
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
