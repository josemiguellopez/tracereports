// Package builtin diagnoses failed tests without an LLM: a versioned catalog of deterministic
// rules over the evidence TraceReports already correlates (the backend call that caused the
// failure, the error message and stack trace, the failed step, the browser console, the history
// of the test and how much of the run failed). It never touches the network, needs no model and
// gives the same answer for the same evidence, in milliseconds.
//
// Each rule reads an Input and, when it applies, proposes a category (the same four the LLM
// uses), a confidence and the facts it relies on. The engine keeps the rule with the highest
// priority (then confidence, then id: always the same winner) and writes its conclusion with the
// templates of templates.go, in Spanish or English.
package builtin

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/locator"
)

// Version identifies the rule catalog. It changes when a rule is added or its meaning changes, so
// a stored diagnosis says which catalog wrote it (it is shown as the "model" of the provider).
const Version = "rules-v1"

// Categories of the diagnosis: the same ones the LLM answers with (ai.Categories).
const (
	CategoryLocator = "LOCATOR_CHANGED"
	CategoryBackend = "BACKEND_TIMEOUT"
	CategoryLogic   = "LOGIC_BUG"
	CategoryInfra   = "INFRA_ERROR"
)

// Confidence of a rule: how directly the evidence supports its conclusion.
const (
	Low    = 1
	Medium = 2
	High   = 3
)

// Call is the backend call that most plausibly caused the failure (see ai.causalConn).
type Call struct {
	Method   string
	Endpoint string // host + ruta normalizada (sin query: nunca lleva secretos)
	Host     string
	Status   int // 0 = sin respuesta
	Failed   bool
	Error    string // texto del error de red (net::ERR_..., timeout)
	BeforeMs int64  // cuánto antes del fallo empezó; < 0 = desconocido
}

// Input is the evidence of one failed test.
type Input struct {
	Lang       string // es | en
	Error      string // mensaje de error del test
	Trace      string
	FailedStep string // mensaje del primer paso FAIL
	Location   string // archivo:línea del frame más interno del proyecto
	Causal     *Call
	PageErrors []string // errores de JavaScript no capturados de la página
	// Stability of the test in its history (db.Classify): flaky | persistent | "".
	Stability string
	Flips     int
	// RunFailed and RunTotal: how much of the run failed (environment down).
	RunFailed, RunTotal int
}

// Diagnosis is the conclusion of the engine for one failed test.
type Diagnosis struct {
	Rule       string   `json:"rule"`
	Category   string   `json:"category"`
	Confidence int      `json:"confidence"`
	Summary    string   `json:"summary"`
	Suggestion string   `json:"suggestion"`
	Evidence   []string `json:"evidence"`
	// Conclusive is false when no rule explained the failure (the default rule).
	Conclusive bool `json:"conclusive"`
}

// match is what a rule proposes: the template vars fill its texts.
type match struct {
	category   string
	confidence int
	vars       map[string]string
	evidence   []string
}

// Rule is one entry of the catalog.
type Rule struct {
	ID       string
	Priority int // mayor gana
	Match    func(in *Input) *match
}

// Thresholds of the run-level rules.
const (
	EnvDownMinTests = 5  // con menos tests, una ejecución mayormente fallida no dice nada del ambiente
	EnvDownPercent  = 80 // % de tests fallidos desde el que se sospecha del ambiente
)

// Catalog is the ordered list of rules (highest priority first). The default rule is not in it:
// it applies when none of these does.
var Catalog = []Rule{
	{"backend_5xx", 100, ruleBackend5xx},
	{"backend_rate_limited", 95, ruleRateLimited},
	{"network_unreachable", 95, ruleCausalUnreachable},
	{"backend_no_response", 90, ruleNoResponse},
	{"auth_rejected", 85, ruleAuthRejected},
	{"backend_status", 80, ruleStatusInMessage},
	{"network_error", 78, ruleNetworkInMessage},
	{"js_error", 75, ruleJSError},
	{"backend_client_error", 72, ruleClientError},
	{"locator_not_found", 70, ruleLocator},
	{"assertion", 60, ruleAssertion},
	{"environment_down", 50, ruleEnvironmentDown},
	{"timeout", 40, ruleTimeout},
	{"flaky", 30, ruleFlaky},
}

// DefaultRule is the id of the conclusion when no rule applies.
const DefaultRule = "no_conclusive_rule"

// Diagnose runs the catalog over the evidence of a failed test.
func Diagnose(in Input) Diagnosis {
	if in.Lang != "en" {
		in.Lang = "es"
	}
	type hit struct {
		rule Rule
		m    *match
	}
	var hits []hit
	for _, r := range Catalog {
		if m := r.Match(&in); m != nil {
			hits = append(hits, hit{r, m})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.rule.Priority != b.rule.Priority {
			return a.rule.Priority > b.rule.Priority
		}
		if a.m.confidence != b.m.confidence {
			return a.m.confidence > b.m.confidence
		}
		return a.rule.ID < b.rule.ID
	})
	if len(hits) == 0 {
		return render(in.Lang, DefaultRule, &match{category: CategoryLogic, confidence: Low,
			vars: map[string]string{"error": firstLine(in.Error, in.FailedStep, in.Lang)}}, false)
	}
	best := hits[0]
	d := render(in.Lang, best.rule.ID, best.m, true)
	if in.Stability == "flaky" && best.rule.ID != "flaky" {
		d.Summary += " " + fill(texts[in.Lang]["flaky_note"].Summary, map[string]string{"flips": strconv.Itoa(in.Flips)})
	}
	return d
}

func render(lang, rule string, m *match, conclusive bool) Diagnosis {
	t := texts[lang][rule]
	return Diagnosis{Rule: rule, Category: m.category, Confidence: m.confidence, Conclusive: conclusive,
		Summary: fill(t.Summary, m.vars), Suggestion: fill(t.Suggestion, m.vars), Evidence: m.evidence}
}

var placeholder = regexp.MustCompile(`\{(\w+)\}`)

// fill replaces {name} with vars[name] (a missing var is left empty, never as "{name}").
func fill(s string, vars map[string]string) string {
	return placeholder.ReplaceAllStringFunc(s, func(p string) string { return vars[p[1:len(p)-1]] })
}

// ─── reglas de la llamada causal ────────────────────────────────────────────

func ruleBackend5xx(in *Input) *match {
	c := in.Causal
	if c == nil || c.Failed || c.Status < 500 {
		return nil
	}
	return callMatch(in, CategoryBackend, High)
}

func ruleRateLimited(in *Input) *match {
	if c := in.Causal; c != nil && !c.Failed && c.Status == 429 {
		return callMatch(in, CategoryBackend, High)
	}
	return nil
}

func ruleCausalUnreachable(in *Input) *match {
	c := in.Causal
	if c == nil || !(c.Failed || c.Status == 0) {
		return nil
	}
	kind := netKind(c.Error)
	if kind == "" || kind == "timeout" {
		return nil
	}
	m := callMatch(in, CategoryInfra, High)
	m.vars["target"], m.vars["kind"] = c.Host, texts[in.Lang]["kind_"+kind].Summary
	return m
}

func ruleNoResponse(in *Input) *match {
	c := in.Causal
	if c == nil {
		return nil
	}
	if c.Status == 408 || ((c.Failed || c.Status == 0) && (netKind(c.Error) == "timeout" || netKind(c.Error) == "")) {
		return callMatch(in, CategoryBackend, High)
	}
	return nil
}

func ruleAuthRejected(in *Input) *match {
	if c := in.Causal; c != nil && !c.Failed && (c.Status == 401 || c.Status == 403) {
		return callMatch(in, CategoryInfra, High)
	}
	if st := statusInMessage(in.Error); st == 401 || st == 403 {
		return &match{category: CategoryInfra, confidence: Medium, evidence: []string{firstLine(in.Error, "", in.Lang)},
			vars: map[string]string{"call": "HTTP " + strconv.Itoa(st), "endpoint": "", "target": ""}}
	}
	return nil
}

func ruleClientError(in *Input) *match {
	c := in.Causal
	if c == nil || c.Failed || c.Status < 400 || c.Status >= 500 || c.Status == 401 || c.Status == 403 || c.Status == 408 || c.Status == 429 {
		return nil
	}
	return callMatch(in, CategoryLogic, Medium)
}

func callMatch(in *Input, category string, confidence int) *match {
	call := describeCall(in.Causal, in.Lang)
	return &match{category: category, confidence: confidence, evidence: []string{call},
		vars: map[string]string{"call": call, "endpoint": in.Causal.Endpoint, "target": in.Causal.Host}}
}

// describeCall is the fact cited by the conclusion: "POST shop/api/orders → HTTP 500, 0.4 s before
// the failure".
func describeCall(c *Call, lang string) string {
	outcome := "HTTP " + strconv.Itoa(c.Status)
	if c.Failed || c.Status == 0 {
		outcome = texts[lang]["no_response"].Summary
		if e := strings.TrimPrefix(strings.TrimSpace(c.Error), "net::"); e != "" {
			outcome += " (" + clip(e, 80) + ")"
		}
	}
	s := fmt.Sprintf("%s %s → %s", c.Method, c.Endpoint, outcome)
	if c.BeforeMs >= 0 {
		secs := strconv.FormatFloat(float64(c.BeforeMs)/1000, 'f', 1, 64)
		if lang == "es" {
			secs = strings.Replace(secs, ".", ",", 1)
		}
		s += fill(texts[lang]["before_failure"].Summary, map[string]string{"secs": secs})
	}
	return s
}

// ─── reglas del mensaje de error ────────────────────────────────────────────

var (
	dnsRe     = regexp.MustCompile(`(?i)ERR_NAME_NOT_RESOLVED|no such host|getaddrinfo|Name or service not known|ENOTFOUND|nodename nor servname|NameResolutionError|UnknownHostException|Temporary failure in name resolution`)
	refusedRe = regexp.MustCompile(`(?i)ERR_CONNECTION_REFUSED|connection refused|ECONNREFUSED|ConnectException|actively refused|NewConnectionError`)
	tlsRe     = regexp.MustCompile(`(?i)ERR_CERT_|ERR_SSL_|x509:|SSLError|SSLHandshake|certificate verify failed|tls: |handshake failure|PKIX path`)
	resetRe   = regexp.MustCompile(`(?i)ERR_CONNECTION_RESET|ERR_CONNECTION_CLOSED|ECONNRESET|connection reset|ERR_INTERNET_DISCONNECTED|ERR_NETWORK_CHANGED|EOF occurred|broken pipe`)
	// excepciones de conexión sin más detalle (Python ConnectionError, Java SocketException)
	connRe    = regexp.MustCompile(`\b(?:Connection(?:Aborted)?Error|SocketException|ProtocolError|RemoteDisconnected)\b`)
	timeoutRe = regexp.MustCompile(`(?i)ERR_TIMED_OUT|timed? ?out|deadline exceeded|ETIMEDOUT|ReadTimeout|SocketTimeout|Timeout \d+ ?ms exceeded|TimeoutError|TimeoutException`)
)

// netKind classifies a network error text: dns | refused | tls | reset | timeout | connection | "".
func netKind(s string) string {
	switch {
	case dnsRe.MatchString(s):
		return "dns"
	case refusedRe.MatchString(s):
		return "refused"
	case tlsRe.MatchString(s):
		return "tls"
	case resetRe.MatchString(s):
		return "reset"
	case timeoutRe.MatchString(s):
		return "timeout"
	case connRe.MatchString(s):
		return "connection"
	}
	return ""
}

func ruleNetworkInMessage(in *Input) *match {
	kind := netKind(in.Error + "\n" + in.Trace)
	if kind == "" || kind == "timeout" {
		return nil
	}
	target := hostInMessage(in.Error + "\n" + in.Trace)
	if target == "" {
		target = texts[in.Lang]["the_service"].Summary
	}
	return &match{category: CategoryInfra, confidence: High, evidence: []string{firstLine(in.Error, "", in.Lang)},
		vars: map[string]string{"target": target, "kind": texts[in.Lang]["kind_"+kind].Summary, "call": firstLine(in.Error, "", in.Lang)}}
}

var hostRe = regexp.MustCompile(`(?i)(?:https?|wss?)://([a-z0-9.-]+(?::\d+)?)|host='([a-z0-9.-]+)'|lookup ([a-z0-9.-]+)`)

func hostInMessage(s string) string {
	if m := hostRe.FindStringSubmatch(s); m != nil {
		for _, g := range m[1:] {
			if g != "" {
				return g
			}
		}
	}
	return ""
}

// statusAfterRe finds the actual value of a failed check ("got 500", "but was: <500>", "Received: 500",
// "actual 500"); statusExpRe the expected one, to tell an HTTP status from any other number.
var (
	statusAfterRe = regexp.MustCompile(`(?i)(?:\bgot(?: status)?|but was|\bactual(?: value| status)?|\breceived)\s*:?\s*<?(\d{3})\b>?`)
	statusExpRe   = regexp.MustCompile(`(?i)expected(?: status)?\s*:?\s*<?([1-5]\d\d)\b|\bwant\s+([1-5]\d\d)\b`)
	statusWordRe  = regexp.MustCompile(`(?i)\bstatus\b|\bHTTP\b|status_code|statusCode`)
)

// statusInMessage returns the HTTP status a failed check received (0 if the message is not about
// an HTTP status). The number must look like a status: the message says "status"/"HTTP" or the
// expected value is a status too ("expected: <400> but was: <500>").
func statusInMessage(msg string) int {
	m := statusAfterRe.FindStringSubmatch(msg)
	if m == nil {
		return 0
	}
	st, _ := strconv.Atoi(m[1])
	if st < 100 || st > 599 {
		return 0
	}
	if !statusWordRe.MatchString(msg) && !statusExpRe.MatchString(msg) {
		return 0
	}
	return st
}

func ruleStatusInMessage(in *Input) *match {
	st := statusInMessage(in.Error)
	if st < 500 && st != 429 && st != 408 {
		return nil
	}
	return &match{category: CategoryBackend, confidence: High, evidence: []string{firstLine(in.Error, "", in.Lang)},
		vars: map[string]string{"actual": strconv.Itoa(st), "error": firstLine(in.Error, "", in.Lang)}}
}

// visibilityRe: the element the test waited for never showed up (Playwright expect, Cypress).
var visibilityRe = regexp.MustCompile(`(?i)element\(s\) not found|expected to be visible|toBeVisible\(\) failed|to_be_visible|Expected: visible|never (?:appeared|became visible)`)

func locatorFailure(in *Input) (bool, string) {
	text := in.Error + "\n" + in.Trace + "\n" + in.FailedStep
	sel := locator.ExtractSelector(in.Error, in.Trace)
	if sel == "" {
		sel = locator.ExtractSelector(in.FailedStep)
	}
	return locator.LooksLikeLocatorFailure(text) || visibilityRe.MatchString(text), sel
}

func ruleJSError(in *Input) *match {
	if len(in.PageErrors) == 0 {
		return nil
	}
	if ok, _ := locatorFailure(in); !ok {
		return nil
	}
	js := clip(strings.Join(strings.Fields(in.PageErrors[0]), " "), 160)
	return &match{category: CategoryLogic, confidence: Medium, evidence: []string{js, firstLine(in.Error, in.FailedStep, in.Lang)},
		vars: map[string]string{"console": js}}
}

func ruleLocator(in *Input) *match {
	ok, sel := locatorFailure(in)
	if !ok {
		return nil
	}
	conf := High
	if sel == "" || visibilityRe.MatchString(in.Error+"\n"+in.Trace) && !locator.LooksLikeLocatorFailure(in.Error+"\n"+in.Trace) {
		conf = Medium // el elemento no apareció: selector roto o la página no lo mostró
	}
	target := texts[in.Lang]["the_element"].Summary
	if sel != "" {
		target = clip(sel, 120)
	}
	return &match{category: CategoryLocator, confidence: conf, evidence: []string{firstLine(in.Error, in.FailedStep, in.Lang)},
		vars: map[string]string{"selector": target}}
}

var (
	assertRe = regexp.MustCompile(`(?i)AssertionError|AssertionFailedError|ComparisonFailure|assert(?:ion)? failed|\bassert\b.+==|\bexpect\(.*\)\.(?:not\.)?to\w+\(|\bexpected\b.*\b(?:but|got|received|actual|to (?:be|equal))\b|\bgot\b.+\bwant\b|\bwant\b.+\bgot\b`)
	// valores esperado/real, en los formatos más comunes (JUnit, Playwright/Jest, Go, pytest, chai)
	expectedActualRes = []*regexp.Regexp{
		regexp.MustCompile(`(?i)expected:?\s*<(.+?)>\s*but was:?\s*<(.+?)>`),
		regexp.MustCompile(`(?is)Expected(?: value| string| pattern)?:\s*(.+?)\s*\n?\s*(?:Received|Actual)(?: value| string)?:\s*([^\n]+)`),
		regexp.MustCompile(`(?i)expected\s+(.+?),?\s+(?:but )?got\s+([^\n]+)`),
		regexp.MustCompile(`(?i)got\s+(.+?),?\s+want\s+([^\n]+)`),
		regexp.MustCompile(`(?m)assert\s+(.+?)\s+==\s+([^\n]+)`),
	}
)

func ruleAssertion(in *Input) *match {
	text := in.Error + "\n" + in.Trace
	if !assertRe.MatchString(text) {
		return nil
	}
	vars := map[string]string{"detail": firstLine(in.Error, in.FailedStep, in.Lang), "location": ""}
	ev := []string{firstLine(in.Error, in.FailedStep, in.Lang)}
	for i, re := range expectedActualRes {
		if m := re.FindStringSubmatch(text); m != nil {
			exp, act := clip(strings.TrimSpace(m[1]), 80), clip(strings.TrimSpace(m[2]), 80)
			if i == 3 { // Go: "got X, want Y"
				exp, act = act, exp
			}
			if i == 4 { // pytest: "assert real == esperado"
				exp, act = act, exp
			}
			vars["detail"] = fill(texts[in.Lang]["expected_actual"].Summary, map[string]string{"expected": exp, "actual": act})
			break
		}
	}
	if in.Location != "" {
		vars["location"] = fill(texts[in.Lang]["at_location"].Summary, map[string]string{"location": in.Location})
		ev = append(ev, in.Location)
	}
	return &match{category: CategoryLogic, confidence: Medium, evidence: ev, vars: vars}
}

func ruleEnvironmentDown(in *Input) *match {
	if in.RunTotal < EnvDownMinTests || in.RunFailed*100 < EnvDownPercent*in.RunTotal {
		return nil
	}
	pct := strconv.Itoa(in.RunFailed * 100 / in.RunTotal)
	return &match{category: CategoryInfra, confidence: Medium, evidence: []string{fmt.Sprintf("%d/%d", in.RunFailed, in.RunTotal)},
		vars: map[string]string{"pct": pct, "failed": strconv.Itoa(in.RunFailed), "total": strconv.Itoa(in.RunTotal)}}
}

func ruleTimeout(in *Input) *match {
	if !timeoutRe.MatchString(in.Error + "\n" + in.Trace) {
		return nil
	}
	return &match{category: CategoryBackend, confidence: Low, evidence: []string{firstLine(in.Error, in.FailedStep, in.Lang)},
		vars: map[string]string{"error": firstLine(in.Error, in.FailedStep, in.Lang)}}
}

func ruleFlaky(in *Input) *match {
	if in.Stability != "flaky" {
		return nil
	}
	return &match{category: CategoryInfra, confidence: Low, evidence: []string{strconv.Itoa(in.Flips)},
		vars: map[string]string{"flips": strconv.Itoa(in.Flips)}}
}

// ─── utilidades ─────────────────────────────────────────────────────────────

// firstLine is the first non-empty line of the error (or of the failed step), clipped.
func firstLine(msg, step, lang string) string {
	for _, s := range []string{msg, step} {
		for _, l := range strings.Split(s, "\n") {
			if l = strings.Join(strings.Fields(l), " "); l != "" {
				return clip(l, 160)
			}
		}
	}
	return texts[lang]["no_message"].Summary
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}
