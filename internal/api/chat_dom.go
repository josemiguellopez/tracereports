package api

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/locator"
)

// Lo que Trace Buddy sabe de la página del fallo (el snapshot del DOM) y de las herramientas del test,
// calculado aquí y no por el modelo: un modelo local chico no puede leer un DOM entero, pero sí usar
// "el skeleton es //div[...] y está visible" para escribir la espera. Sin esto preguntaba en bucle
// por el framework o mandaba a buscar el DOM en la pestaña Red.

// chatTooling is the test's tool and language, from the run's framework and the error, so the chat
// never asks for it: "Selenium (Python)", "Playwright (JavaScript/TypeScript)"… or "" when unknown.
func chatTooling(framework, errMsg, errTrace string) string {
	fw := strings.ToLower(framework)
	text := errMsg + "\n" + errTrace
	lang := ""
	switch {
	case strings.Contains(fw, "pytest") || strings.Contains(fw, "python") || strings.Contains(text, ".py\""):
		lang = "Python"
	case strings.Contains(fw, "junit") || strings.Contains(fw, "testng") || strings.Contains(fw, "java") || strings.Contains(fw, "maven") || strings.Contains(fw, "gradle"):
		lang = "Java"
	case strings.Contains(fw, "playwright") || strings.Contains(fw, "jest") || strings.Contains(fw, "mocha") || strings.Contains(fw, "cypress") || strings.Contains(fw, "vitest"):
		lang = "JavaScript/TypeScript"
	case strings.Contains(fw, "go"):
		lang = "Go"
	}
	tool := ""
	switch {
	case seleniumError.MatchString(text):
		tool = "Selenium"
	case strings.Contains(fw, "cypress") || strings.Contains(text, "cy."):
		tool = "Cypress"
	case strings.Contains(fw, "playwright") || playwrightError.MatchString(text):
		tool = "Playwright"
	}
	switch {
	case tool != "" && lang != "":
		return tool + " (" + lang + ")"
	case tool != "":
		return tool
	case lang != "":
		return lang
	}
	return ""
}

var (
	// TimeoutException es de Selenium (Python y Java); Playwright lanza TimeoutError
	seleniumError   = regexp.MustCompile(`(?i)selenium|webdriver|NoSuchElement|StaleElementReference|ElementClickIntercepted|ElementNotInteractable|\bTimeoutException\b|\bBy\.(?:id|xpath|css|cssSelector|name|className|ID|XPATH|CSS_SELECTOR|NAME|CLASS_NAME)\b`)
	playwrightError = regexp.MustCompile(`(?i)playwright|\blocator\(|get_by_\w+\(|getBy\w+\(|waiting for locator`)
)

// domHint is one element (or a group with the same locator) of the page at the failure.
type domHint struct {
	desc    string // <div class="skeleton-row">
	xpath   string
	css     string
	count   int
	visible int
}

func (h domHint) line() string {
	s := fmt.Sprintf("- %s · XPath: %s", h.desc, h.xpath)
	if h.css != "" {
		s += " · CSS: " + h.css
	}
	if h.count > 1 {
		s += fmt.Sprintf(" · %d elements, %d visible", h.count, h.visible)
	} else if h.visible > 0 {
		s += " · visible"
	} else {
		s += " · hidden"
	}
	return s
}

// pageHints is what the chat needs from the snapshot: the elements the failed selector matches (and
// the clickable element around each), the loading indicators and what covers the page.
type pageHints struct {
	url      string
	failed   string
	parsed   bool // the selector could be read (classes, id or text)
	targets  []domHint
	loaders  []domHint
	overlays []domHint
}

var (
	loadingTextRe = regexp.MustCompile(`(?i)^(cargando|loading)\b`)
	loaderRe      = regexp.MustCompile(`(?i)skeleton|shimmer|spinner|loader|loading|preloader|placeholder-(?:glow|wave)|progress|cargando|blockui`)
	overlayRe     = regexp.MustCompile(`(?i)overlay|backdrop|blockui|cdk-overlay`)
	autoIDRe      = regexp.MustCompile(`\d{3,}|[a-f0-9]{8,}|^:`)
	cssIdentRe    = regexp.MustCompile(`^[A-Za-z_][\w-]*$`)
	// lo que se puede leer de un XPath o CSS sin evaluarlo: clases, id y texto
	selClassRe = regexp.MustCompile(`contains\(\s*@class\s*,\s*['"]([^'"]+)['"]\s*\)|@class\s*=\s*['"]([^'"]+)['"]`)
	selIDRe    = regexp.MustCompile(`@id\s*=\s*['"]([^'"]+)['"]`)
	selTextRe  = regexp.MustCompile(`(?:text\(\)|normalize-space\(\.?\)|\.)\s*,\s*['"]([^'"]+)['"]|(?:text\(\)|normalize-space\(\.?\))\s*=\s*['"]([^'"]+)['"]`)
	cssClassRe = regexp.MustCompile(`\.([A-Za-z_][\w-]*)`)
	cssIDRe    = regexp.MustCompile(`#([A-Za-z_][\w-]*)`)
)

// selectorParts reads the classes, id and text a selector asks for.
func selectorParts(sel string) (classes []string, id, text string) {
	sel = strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(sel, "xpath="), "css="))
	if strings.HasPrefix(sel, "/") || strings.HasPrefix(sel, "(") {
		for _, m := range selClassRe.FindAllStringSubmatch(sel, -1) {
			classes = append(classes, strings.Fields(m[1]+" "+m[2])...)
		}
		if m := selIDRe.FindStringSubmatch(sel); m != nil {
			id = m[1]
		}
		if m := selTextRe.FindStringSubmatch(sel); m != nil {
			text = m[1] + m[2]
		}
		return classes, id, text
	}
	for _, m := range cssClassRe.FindAllStringSubmatch(sel, -1) {
		classes = append(classes, m[1])
	}
	if m := cssIDRe.FindStringSubmatch(sel); m != nil {
		id = m[1]
	}
	return classes, id, text
}

func classTokens(n *locator.Node) []string { return strings.Fields(n.Attrs["class"]) }

func hasAll(have, want []string) bool {
	for _, w := range want {
		found := false
		for _, h := range have {
			if h == w {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func xpathLit(s string) string {
	if !strings.Contains(s, "'") {
		return "'" + s + "'"
	}
	return `"` + s + `"`
}

// nodeLocator is a stable locator of a node: id, test id, a meaningful class (the one that matched
// `prefer` first) or its short text.
func nodeLocator(n *locator.Node, prefer *regexp.Regexp) (xpath, css string) {
	if id := n.Attrs["id"]; id != "" && !autoIDRe.MatchString(id) {
		if cssIdentRe.MatchString(id) {
			css = "#" + id
		}
		return "//*[@id=" + xpathLit(id) + "]", css
	}
	for _, a := range []string{"data-testid", "data-test", "data-test-id"} {
		if v := n.Attrs[a]; v != "" {
			return "//*[@" + a + "=" + xpathLit(v) + "]", "[" + a + "=" + xpathLit(v) + "]"
		}
	}
	toks := classTokens(n)
	pick := ""
	for _, c := range toks {
		if prefer != nil && prefer.MatchString(c) {
			pick = c
			break
		}
	}
	if pick == "" {
		for _, c := range toks {
			if !autoIDRe.MatchString(c) && len(c) > 2 {
				pick = c
				break
			}
		}
	}
	if pick != "" {
		xpath = "//" + n.Tag + "[contains(concat(' ',normalize-space(@class),' '),' " + pick + " ')]"
		if cssIdentRe.MatchString(pick) {
			css = n.Tag + "." + pick
		}
		return xpath, css
	}
	if t := strings.TrimSpace(n.Text); t != "" && len([]rune(t)) <= 40 && t != "•••" {
		return "//" + n.Tag + "[normalize-space()=" + xpathLit(t) + "]", ""
	}
	return "//" + n.Tag, n.Tag
}

func nodeDesc(n *locator.Node) string {
	var b strings.Builder
	b.WriteString("<" + n.Tag)
	for _, a := range []string{"id", "class", "role", "aria-busy", "data-testid"} {
		if v := n.Attrs[a]; v != "" {
			fmt.Fprintf(&b, ` %s="%s"`, a, clipText(v, 60))
		}
	}
	b.WriteString(">")
	if t := strings.TrimSpace(n.Text); t != "" {
		b.WriteString(" " + clipText(t, 40))
	}
	return b.String()
}

func clickable(n *locator.Node) bool {
	return n.Tag == "button" || n.Tag == "a" || n.Attrs["role"] == "button" || n.Tag == "input"
}

// group adds a node under its locator: several equal skeleton rows are one line "N elements".
func group(list []domHint, n *locator.Node, prefer *regexp.Regexp) []domHint {
	x, c := nodeLocator(n, prefer)
	for i := range list {
		if list[i].xpath == x {
			list[i].count++
			if n.Visible {
				list[i].visible++
			}
			return list
		}
	}
	h := domHint{desc: nodeDesc(n), xpath: x, css: c, count: 1}
	if n.Visible {
		h.visible = 1
	}
	return append(list, h)
}

// readPageHints reads the stored snapshot (with nodes; without them only the failed selector is known).
func readPageHints(raw, failed string) *pageHints {
	var snap locator.Snapshot
	if raw == "" || json.Unmarshal([]byte(raw), &snap) != nil || len(snap.Nodes) == 0 {
		return nil
	}
	h := &pageHints{url: snap.URL, failed: failed}
	nodes := snap.Nodes
	page := 1
	if snap.Page != nil {
		page = max(1, snap.Page.W*snap.Page.H)
	}
	classes, id, text := selectorParts(failed)
	h.parsed = len(classes) > 0 || id != "" || text != ""
	for i := range nodes {
		n := &nodes[i]
		if h.parsed && hasAll(classTokens(n), classes) && (id == "" || n.Attrs["id"] == id) &&
			(text == "" || strings.Contains(strings.ToLower(n.Text), strings.ToLower(text))) {
			// el elemento que falló y lo clickeable alrededor (un <i> de ícono dentro de un botón)
			target := n
			for p, hops := n.P, 0; p >= 0 && p < len(nodes) && hops < 4 && !clickable(target); p, hops = nodes[p].P, hops+1 {
				if clickable(&nodes[p]) {
					target = &nodes[p]
				}
			}
			if len(h.targets) < 4 {
				hint := domHint{desc: nodeDesc(n), count: 1}
				if n.Visible {
					hint.visible = 1
				}
				hint.xpath, hint.css = nodeLocator(target, nil)
				if target != n {
					hint.desc += " inside " + nodeDesc(target)
				}
				h.targets = append(h.targets, hint)
			}
		}
		marker := n.Attrs["class"] + " " + n.Attrs["id"]
		if loaderRe.MatchString(marker) || n.Attrs["aria-busy"] == "true" || n.Attrs["role"] == "progressbar" ||
			(len([]rune(n.Text)) < 30 && loadingTextRe.MatchString(strings.TrimSpace(n.Text))) {
			if len(h.loaders) < 6 {
				h.loaders = group(h.loaders, n, loaderRe)
			}
			continue
		}
		if n.Visible && overlayRe.MatchString(marker) && n.W*n.H*4 >= page && len(h.overlays) < 3 {
			h.overlays = group(h.overlays, n, overlayRe)
		}
	}
	return h
}

// prompt is the page section of the chat's evidence (English: it goes to the model).
func (h *pageHints) prompt() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Page at the moment of the failure (DOM snapshot taken by TraceReports; the user can open it in the Inspector tab):\n")
	if h.url != "" {
		fmt.Fprintf(&b, "URL: %s\n", h.url)
	}
	if h.failed != "" {
		switch {
		case !h.parsed:
			fmt.Fprintf(&b, "Failed selector: %s (it could not be matched against the snapshot here; the Inspector can test it).\n", h.failed)
		case len(h.targets) == 0:
			fmt.Fprintf(&b, "Failed selector: %s. Nothing in the page matched it at the failure: the element was not there yet (or the selector changed).\n", h.failed)
		default:
			fmt.Fprintf(&b, "Failed selector: %s. Elements it matches at the failure:\n", h.failed)
			for _, t := range h.targets {
				b.WriteString(t.line() + "\n")
			}
		}
	}
	if len(h.loaders) > 0 {
		b.WriteString("Loading indicators present at the failure (skeleton, spinner, loading; wait until they disappear before clicking):\n")
		for _, l := range h.loaders {
			b.WriteString(l.line() + "\n")
		}
	} else {
		b.WriteString("No loading indicator (skeleton, spinner, loading, aria-busy) was in the page at the failure.\n")
	}
	if len(h.overlays) > 0 {
		b.WriteString("Large elements covering the page (they can intercept the click):\n")
		for _, o := range h.overlays {
			b.WriteString(o.line() + "\n")
		}
	}
	return b.String()
}

// fallback is a deterministic answer from the page, for when the model only repeats itself.
func (h *pageHints) fallback(lang, tooling string) string {
	es := lang != "en"
	t := func(sp, en string) string {
		if es {
			return sp
		}
		return en
	}
	var b strings.Builder
	b.WriteString(t("Con la página del momento del fallo (snapshot del DOM):\n", "From the page at the moment of the failure (DOM snapshot):\n"))
	if len(h.loaders) > 0 {
		l := h.loaders[0]
		b.WriteString(t("Indicador de carga presente: ", "Loading indicator present: ") + l.xpath + "\n")
		b.WriteString(t("Espera a que desaparezca antes del clic:\n", "Wait for it to disappear before the click:\n"))
		b.WriteString(waitCode(tooling, l.xpath, true) + "\n")
	}
	if len(h.targets) > 0 {
		b.WriteString(t("Elemento del clic: ", "Element to click: ") + h.targets[0].xpath + "\n")
		b.WriteString(waitCode(tooling, h.targets[0].xpath, false) + "\n")
	}
	if len(h.loaders) == 0 && len(h.targets) == 0 {
		b.WriteString(t("No había indicadores de carga ni el elemento buscado en ese momento. ", "There was no loading indicator nor the element you looked for at that moment. "))
	}
	b.WriteString(t("Ábrela en la pestaña Inspector (clic derecho sobre un elemento → QA / Automation) para probar el XPath y copiar el código.",
		"Open it in the Inspector tab (right click an element → QA / Automation) to try the XPath and copy the code."))
	return b.String()
}

// waitCode is an explicit wait in the test's tool: gone=true waits for the element to disappear.
func waitCode(tooling, xpath string, gone bool) string {
	q := strings.ReplaceAll(xpath, `"`, `\"`)
	switch {
	case strings.HasPrefix(tooling, "Selenium (Java"):
		if gone {
			return `new WebDriverWait(driver, Duration.ofSeconds(15)).until(ExpectedConditions.invisibilityOfElementLocated(By.xpath("` + q + `")));`
		}
		return `new WebDriverWait(driver, Duration.ofSeconds(15)).until(ExpectedConditions.elementToBeClickable(By.xpath("` + q + `"))).click();`
	case strings.HasPrefix(tooling, "Playwright (JavaScript"):
		if gone {
			return `await page.locator("xpath=` + q + `").first().waitFor({ state: "hidden" });`
		}
		return `await page.locator("xpath=` + q + `").first().click();`
	case strings.HasPrefix(tooling, "Playwright"):
		if gone {
			return `page.locator("xpath=` + q + `").first.wait_for(state="hidden")`
		}
		return `page.locator("xpath=` + q + `").first.click()`
	}
	if gone {
		return `WebDriverWait(driver, 15).until(EC.invisibility_of_element_located((By.XPATH, "` + q + `")))`
	}
	return `WebDriverWait(driver, 15).until(EC.element_to_be_clickable((By.XPATH, "` + q + `"))).click()`
}
