// Package release answers "can we ship?" for a run: a go / risk / no_go decision from
// configurable criteria, the checks behind it and the state of each functional area (the tests'
// tags), in words a business stakeholder understands.
//
// Criteria come from TRACEREPORTS_RELEASE_GATE, "key=value" separated by ";":
//
//	min_pass_rate=95        % of non-skipped tests that must pass (default 95)
//	critical=smoke,checkout tags whose failures block the release (default none)
//	max_new_failures=0      failures that did not happen in the previous run before it is a risk (default 0)
//	max_flaky=3             flaky tests tolerated before it is a risk (default 3)
package release

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Decisions.
const (
	Go    = "go"
	Risk  = "risk"
	NoGo  = "no_go"
	Block = "block" // severidad de un check que impide salir
	Warn  = "warn"  // severidad de un check que es un riesgo
)

// Gate is the set of criteria.
type Gate struct {
	MinPassRate    float64  `json:"min_pass_rate"`
	Critical       []string `json:"critical"`
	MaxNewFailures int      `json:"max_new_failures"`
	MaxFlaky       int      `json:"max_flaky"`
}

// Default criteria.
func Default() Gate { return Gate{MinPassRate: 95, MaxNewFailures: 0, MaxFlaky: 3} }

// Parse reads the criteria ("" = Default).
func Parse(spec string) (Gate, error) {
	g := Default()
	for _, part := range strings.Split(spec, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			return g, fmt.Errorf("release gate: %q must be key=value", part)
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "min_pass_rate":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 100 {
				return g, fmt.Errorf("release gate: min_pass_rate must be 0-100")
			}
			g.MinPassRate = f
		case "critical":
			g.Critical = nil
			for _, t := range strings.Split(v, ",") {
				if t = strings.TrimSpace(t); t != "" {
					g.Critical = append(g.Critical, t)
				}
			}
		case "max_new_failures", "max_flaky":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return g, fmt.Errorf("release gate: %s must be a number >= 0", k)
			}
			if k == "max_flaky" {
				g.MaxFlaky = n
			} else {
				g.MaxNewFailures = n
			}
		default:
			return g, fmt.Errorf("release gate: unknown criterion %q", k)
		}
	}
	return g, nil
}

// Check is one criterion and how the run did.
type Check struct {
	ID       string   `json:"id"`
	OK       bool     `json:"ok"`
	Severity string   `json:"severity"` // block | warn
	Detail   string   `json:"detail"`   // datos para la frase (la UI la escribe en su idioma)
	Tests    []string `json:"tests,omitempty"`
}

// Feature is a functional area (a tag) and its state in the run.
type Feature struct {
	Name        string `json:"name"`
	Critical    bool   `json:"critical"`
	Total       int    `json:"total"`
	Passed      int    `json:"passed"`
	Failed      int    `json:"failed"`
	Quarantined int    `json:"quarantined"`
	Status      string `json:"status"` // ok | fail | warn | skip
}

// Decision is the answer for a run.
type Decision struct {
	RunID    int64     `json:"run_id"`
	Decision string    `json:"decision"`
	PassRate float64   `json:"pass_rate"`
	Gate     Gate      `json:"gate"`
	Checks   []Check   `json:"checks"`
	Features []Feature `json:"features"`
}

// NoTag names the area of the tests without tags.
const NoTag = ""

// Evaluate decides for a run. newFailures are the names of the tests that failed now but not in
// the previous run (nil when there is no previous run).
func Evaluate(g Gate, d *db.RunDetail, newFailures []string) *Decision {
	crit := map[string]bool{}
	for _, t := range g.Critical {
		crit[strings.ToLower(t)] = true
	}
	out := &Decision{RunID: d.ID, Gate: g}
	areas := map[string]*Feature{}
	var critFails, quarantined, flaky []string
	counted, passed := 0, 0
	for _, t := range d.Tests {
		q := t.Quarantine != nil && t.Quarantine.Active && t.Status == "FAIL"
		if t.Status != "SKIP" && !q {
			counted++
			if t.Status == "PASS" {
				passed++
			}
		}
		if q {
			quarantined = append(quarantined, t.Name)
		}
		if t.Flaky {
			flaky = append(flaky, t.Name)
		}
		tags := splitTags(t.Category)
		if len(tags) == 0 {
			tags = []string{NoTag}
		}
		for _, tag := range tags {
			f := areas[strings.ToLower(tag)]
			if f == nil {
				f = &Feature{Name: tag, Critical: crit[strings.ToLower(tag)]}
				areas[strings.ToLower(tag)] = f
			}
			f.Total++
			switch {
			case q:
				f.Quarantined++
			case t.Status == "PASS":
				f.Passed++
			case t.Status == "FAIL":
				f.Failed++
				if f.Critical {
					critFails = append(critFails, t.Name)
				}
			}
		}
	}
	if counted > 0 {
		out.PassRate = float64(int(float64(passed)/float64(counted)*1000+0.5)) / 10
	}
	for _, f := range areas {
		switch {
		case f.Failed > 0:
			f.Status = "fail"
		case f.Quarantined > 0:
			f.Status = "warn"
		case f.Passed == 0:
			f.Status = "skip"
		default:
			f.Status = "ok"
		}
		out.Features = append(out.Features, *f)
	}
	rank := map[string]int{"fail": 0, "warn": 1, "ok": 2, "skip": 3}
	sort.SliceStable(out.Features, func(i, j int) bool {
		a, b := out.Features[i], out.Features[j]
		if a.Critical != b.Critical {
			return a.Critical
		}
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] < rank[b.Status]
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})

	add := func(id, sev string, ok bool, detail string, tests []string) {
		out.Checks = append(out.Checks, Check{ID: id, Severity: sev, OK: ok, Detail: detail, Tests: uniq(tests)})
	}
	add("complete", Block, !d.Incomplete && d.Running == 0, "", nil)
	if len(g.Critical) > 0 {
		add("critical", Block, len(critFails) == 0, strings.Join(g.Critical, ", "), critFails)
	}
	add("pass_rate", Block, counted == 0 || out.PassRate >= g.MinPassRate, fmt.Sprintf("%.1f/%.1f", out.PassRate, g.MinPassRate), nil)
	if newFailures != nil {
		add("new_failures", Warn, len(newFailures) <= g.MaxNewFailures, fmt.Sprintf("%d/%d", len(newFailures), g.MaxNewFailures), newFailures)
	}
	add("quarantined", Warn, len(quarantined) == 0, strconv.Itoa(len(quarantined)), quarantined)
	add("flaky", Warn, len(flaky) <= g.MaxFlaky, fmt.Sprintf("%d/%d", len(flaky), g.MaxFlaky), flaky)

	out.Decision = Go
	for _, c := range out.Checks {
		if !c.OK && c.Severity == Block {
			out.Decision = NoGo
			break
		}
		if !c.OK {
			out.Decision = Risk
		}
	}
	return out
}

func splitTags(category string) []string {
	var out []string
	for _, t := range strings.Split(category, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func uniq(list []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
