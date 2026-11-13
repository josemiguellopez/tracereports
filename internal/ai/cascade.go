package ai

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Failures in cascade. In a test file whose tests depend on each other (the first one creates the
// order, the next ones edit, send and receive it), when the first one fails the next ones fail too,
// and each would be its own incident: "number cannot be empty" says nothing of the cause. A later
// failure joins the incident of the first failed test of its file only on evidence that it points
// to it: its message names that test, or says that data from before is missing or that the test
// was skipped because a previous step failed. A test that passes in between breaks the chain.

// precondRe: the failure is about something an earlier test should have left (empty data, a step
// skipped because the previous one failed), not about what this test checks.
var precondRe = regexp.MustCompile(`(?i)\bvac[ií][oa]s?\b|\bempty\b|\bis (?:none|null|undefined)\b|\bnonetype\b|\bomitid[oa]s?\b|\bomitted\b|\bskipped because\b|\bpaso anterior\b|\bprevious (?:step|test)\b|\bprecondici[oó]n\b|\bprecondition\b|\bdepende de\b|\bdepends on\b`)

const (
	// hungMin: a first test that ran at least this long before failing is reported as hung.
	hungMin = 5 * time.Minute
	// hungFactor: and at least this many times longer than the tests that failed after it.
	hungFactor = 10
)

// testFile is the file of a test from its key ("tests/x.py::Class::test" -> "tests/x.py"), or the
// suite (file or class) the client reported; "" when neither is known (a test reported by name
// only): those never form a cascade.
func testFile(t *db.Test) string {
	if t.KeyApprox {
		return ""
	}
	if i := strings.Index(t.Key, "::"); i > 0 {
		return t.Key[:i]
	}
	return t.Suite
}

func duration(t *db.Test) int64 {
	if t.EndedAt == nil || *t.EndedAt < t.StartedAt {
		return 0
	}
	return *t.EndedAt - t.StartedAt
}

// pointsTo: the failure of t points to root (see the package comment above).
func pointsTo(t, root *db.Test) bool {
	msg := t.ErrorMessage
	if len(msg) > 600 {
		msg = msg[:600]
	}
	if root.Name != "" && strings.Contains(strings.ToLower(msg), strings.ToLower(root.Name)) {
		return true
	}
	return precondRe.MatchString(msg)
}

// cascades returns, for each first failed test of a file with later failures that point to it,
// those later failed tests (in the order they ran).
func cascades(tests []db.Test) map[int64][]*db.Test {
	byFile := map[string][]*db.Test{}
	for i := range tests {
		if f := testFile(&tests[i]); f != "" {
			byFile[f] = append(byFile[f], &tests[i])
		}
	}
	out := map[int64][]*db.Test{}
	for _, ts := range byFile {
		sort.SliceStable(ts, func(i, j int) bool {
			if ts[i].StartedAt != ts[j].StartedAt {
				return ts[i].StartedAt < ts[j].StartedAt
			}
			return ts[i].ID < ts[j].ID
		})
		var root *db.Test
		for _, t := range ts {
			switch {
			case root == nil:
				if t.Status == "FAIL" {
					root = t
				}
			case t.Status == "PASS" || t.Status == "WARNING":
				root = nil // la cadena se cortó: lo que falle después no se le atribuye
			case t.Status == "FAIL" && pointsTo(t, root):
				out[root.ID] = append(out[root.ID], t)
			}
		}
	}
	return out
}

// orderedRoots: the first tests of each cascade, in the order of the run.
func orderedRoots(tests []db.Test, chains map[int64][]*db.Test) []*db.Test {
	var out []*db.Test
	for i := range tests {
		if len(chains[tests[i].ID]) > 0 {
			out = append(out, &tests[i])
		}
	}
	return out
}

// cascadeIncident turns the incident of the first failed test into the incident of the cascade:
// the same cause, with the later tests and the evidence that links them.
func cascadeIncident(rootIn db.Incident, root *db.Test, followers []*db.Test) db.Incident {
	in := rootIn
	in.Key = "cascade:" + rootIn.Key
	in.Kind = "cascade"
	in.Evidence = append([]db.Evidence(nil), rootIn.Evidence...)
	first := strings.TrimSpace(strings.SplitN(followers[0].ErrorMessage, "\n", 2)[0])
	if len(first) > 110 {
		first = first[:110] + "…"
	}
	text := fmt.Sprintf("%s falló primero; %d test(s) siguientes del mismo archivo fallaron después por eso (%s)",
		root.Name, len(followers), first)
	in.Evidence = append([]db.Evidence{{TestID: followers[0].ID, Kind: "cascade", Text: text}}, in.Evidence...)
	if d := duration(root); d >= hungMin.Milliseconds() {
		var longest int64
		for _, f := range followers {
			longest = max(longest, duration(f))
		}
		if d >= hungFactor*max(longest, 1000) {
			in.Evidence = append(in.Evidence, db.Evidence{TestID: root.ID, Kind: "hung",
				Text: fmt.Sprintf("%s estuvo %s antes de fallar (los siguientes, %s como mucho)", root.Name,
					shortDur(d), shortDur(longest))})
		}
	}
	return in
}

func shortDur(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%d h %02d min", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int(d.Minutes()))
	default:
		return fmt.Sprintf("%.0f s", d.Seconds())
	}
}
