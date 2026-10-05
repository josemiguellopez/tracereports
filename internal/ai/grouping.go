package ai

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Failure grouping. An incident joins failed tests only on discriminating evidence:
//   - backend: the same failing call (method, host and normalized path, outcome) that happened
//     shortly before the test failed and plausibly caused it;
//   - error: the same signature (exception type, normalized message and the innermost frame of
//     the project's code).
// The AI category alone never merges two failures: two LOGIC_BUGs are two incidents unless their
// signatures match.

const (
	// causalSlack: a request still counts as "before the failure" if it started up to this long
	// after the first FAIL step (steps and requests come from different clocks/threads).
	causalSlack = 2 * time.Second
	// causalWindow: a request older than this, relative to the failure, is not proposed as cause.
	causalWindow = 2 * time.Minute
)

// failureTime is when the test failed: its first FAIL step, otherwise its end (0 = unknown).
func failureTime(t *db.Test) int64 {
	for _, l := range t.Logs {
		if l.Status == "FAIL" {
			return l.Timestamp
		}
	}
	if t.EndedAt != nil {
		return *t.EndedAt
	}
	return 0
}

// strongNetError: the backend did not answer or answered with a server-side problem.
func strongNetError(c db.NetConn) bool {
	return c.Failed || c.Status == 0 || c.Status >= 500 || c.Status == 408 || c.Status == 429
}

// causalConn picks, among the connections of a failed test, the error that most plausibly caused
// the failure, or nil:
//   - expected responses (declared by the test) are never candidates;
//   - only requests started before the failure (with slack) and within causalWindow count;
//   - a server-side error (no response, 5xx, 408, 429) wins, the latest one before the failure;
//   - a 4xx counts only when the application did not move on: no later successful response
//     from the same host. A 401/404 the app recovered from does not explain a later UI failure.
func causalConn(conns []db.NetConn, failAt int64) *db.NetConn {
	inWindow := func(c db.NetConn) bool {
		if failAt == 0 || c.StartedAt == 0 {
			return true
		}
		return c.StartedAt <= failAt+causalSlack.Milliseconds() && failAt-c.StartedAt <= causalWindow.Milliseconds()
	}
	var strong, weak *db.NetConn
	for i := range conns {
		c := conns[i]
		if c.Expected || !inWindow(c) || !(c.Failed || c.Status >= 400) {
			continue
		}
		if strongNetError(c) {
			strong = &conns[i]
			continue
		}
		host, _ := db.NormalizeEndpoint(c.URL)
		recovered := false
		for _, later := range conns[i+1:] {
			if h, _ := db.NormalizeEndpoint(later.URL); h == host && later.Status > 0 && later.Status < 400 && !later.Failed {
				recovered = true
				break
			}
		}
		if !recovered {
			weak = &conns[i]
		}
	}
	if strong != nil {
		return strong
	}
	return weak
}

func connOutcome(c *db.NetConn) string {
	if c.Failed || c.Status == 0 {
		return "sin respuesta (" + strings.TrimPrefix(c.ErrorText, "net::") + ")"
	}
	return fmt.Sprintf("HTTP %d", c.Status)
}

var (
	excTypeRe = regexp.MustCompile(`^\s*(?:E\s+)?([A-Za-z_][\w.]*(?:Error|Exception|Timeout|Failure|Exit|Interrupt))\b:?`)
	numberRe  = regexp.MustCompile(`\d+`)
	hexIDRe   = regexp.MustCompile(`\b[0-9a-fA-F]{12,}\b|[0-9a-fA-F]{8}-[0-9a-fA-F-]{27,}`)
	spaceRe   = regexp.MustCompile(`\s+`)
	// frames: Python traceback (File "x.py", line 12), pytest long repr (x.py:12: Error) and Go (x_test.go:12)
	pyFrameRe = regexp.MustCompile(`File "([^"]+)", line (\d+)`)
	pathRe    = regexp.MustCompile(`(?m)([\w./\\-]+\.(?:py|go|js|ts|java|kt|cs|rb)):(\d+)`)
	libPathRe = regexp.MustCompile(`(?i)site-packages|dist-packages|[/\\]lib[/\\]python|<frozen|[/\\]go[/\\]src[/\\]|[/\\]pkg[/\\]mod[/\\]|node_modules|_pytest|pluggy`)
)

// errorSignature builds the grouping key of a failure without a causal backend call: exception
// type, message with variable parts (numbers, ids) replaced, and the innermost frame of the
// project's own code (shared page objects or helpers make related failures share it).
func errorSignature(msg, trace string) (sig, exc, location string) {
	first := strings.TrimSpace(strings.SplitN(strings.TrimSpace(msg), "\n", 2)[0])
	if m := excTypeRe.FindStringSubmatch(first); m != nil {
		exc = m[1]
		first = strings.TrimSpace(first[len(m[0]):])
	} else if m := excTypeRe.FindStringSubmatch(lastErrorLine(trace)); m != nil {
		exc = m[1]
	}
	norm := hexIDRe.ReplaceAllString(first, "#")
	norm = numberRe.ReplaceAllString(norm, "#")
	norm = spaceRe.ReplaceAllString(norm, " ")
	if len(norm) > 120 {
		norm = norm[:120]
	}
	location = innermostFrame(trace)
	return exc + "|" + norm + "|" + location, exc, location
}

func lastErrorLine(trace string) string {
	lines := strings.Split(trace, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); strings.HasPrefix(l, "E ") || excTypeRe.MatchString(l) {
			return l
		}
	}
	return ""
}

// innermostFrame returns "file:line" of the deepest frame outside libraries ("" if none).
func innermostFrame(trace string) string {
	loc := ""
	for _, m := range pyFrameRe.FindAllStringSubmatch(trace, -1) {
		if !libPathRe.MatchString(m[1]) {
			loc = shortPath(m[1]) + ":" + m[2]
		}
	}
	if loc != "" {
		return loc
	}
	for _, m := range pathRe.FindAllStringSubmatch(trace, -1) {
		if !libPathRe.MatchString(m[1]) {
			loc = shortPath(m[1]) + ":" + m[2]
		}
	}
	return loc
}

// shortPath keeps the last two path elements (machine-independent: CI and laptops share keys).
func shortPath(p string) string {
	p = strings.ReplaceAll(p, "\\", "/")
	parts := strings.FieldsFunc(p, func(r rune) bool { return r == '/' })
	if len(parts) > 2 {
		parts = parts[len(parts)-2:]
	}
	return strings.Join(parts, "/")
}

// incidentFor classifies one failed test into its incident (without TestIDs/TestNames).
func (a *Analyzer) incidentFor(t *db.Test) (db.Incident, error) {
	full, err := a.store.GetTest(t.ID) // con pasos: el momento del fallo y la captura
	if err != nil {
		return db.Incident{}, err
	}
	conns, err := a.store.NetworkTimeline(t.ID)
	if err != nil {
		return db.Incident{}, err
	}
	failAt := failureTime(full)
	var in db.Incident
	if conn := causalConn(conns, failAt); conn != nil {
		host, path := db.NormalizeEndpoint(conn.URL)
		outcome := connOutcome(conn)
		in = db.Incident{Key: "backend:" + conn.Method + " " + host + path + " " + outcome, Kind: "backend", Host: host,
			Title: fmt.Sprintf("Backend: %s %s%s → %s", conn.Method, host, path, outcome)}
		fact := fmt.Sprintf("%s %s → %s", conn.Method, conn.URL, outcome)
		if failAt > 0 && conn.StartedAt > 0 {
			fact += fmt.Sprintf(" (%.1f s antes del fallo)", float64(failAt-conn.StartedAt)/1000)
		}
		in.Evidence = append(in.Evidence, db.Evidence{TestID: t.ID, Kind: "network", Ref: conn.ID, Text: fact})
	} else {
		sig, exc, loc := errorSignature(t.ErrorMessage, t.ErrorTrace)
		title := strings.TrimSpace(strings.SplitN(t.ErrorMessage, "\n", 2)[0])
		if title == "" {
			title = "Fallo sin mensaje de error"
		}
		if len(title) > 110 {
			title = title[:110] + "…"
		}
		in = db.Incident{Key: "error:" + sig, Kind: "error", Title: title, Exception: exc, Location: loc}
		in.Evidence = append(in.Evidence, db.Evidence{TestID: t.ID, Kind: "error", Text: title})
	}
	if t.Triage != nil && t.Triage.State == "DONE" {
		in.Category = t.Triage.Category
	}
	for i := len(full.Logs) - 1; i >= 0; i-- {
		if l := full.Logs[i]; l.Screenshot != "" && l.Status == "FAIL" {
			in.Evidence = append(in.Evidence, db.Evidence{TestID: t.ID, Kind: "screenshot", Ref: l.ID, Text: l.Message, URL: l.Screenshot})
			break
		}
	}
	return in, nil
}
