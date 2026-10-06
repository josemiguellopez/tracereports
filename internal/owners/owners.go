// Package owners assigns an owner (team or person) to each test, like a CODEOWNERS file: one rule
// per line, "<pattern> <owner>", and the last rule that matches wins.
//
//	# comentarios y líneas vacías se ignoran
//	*                         @qa-team
//	tests/checkout/*          Equipo pagos
//	*::test_login*            @auth
//	tag:smoke                 @qa-smoke
//	suite:"Billing / *"       Facturación
//
// A plain pattern is matched against the test identity (pytest nodeid, file > title, package
// path...); "tag:" against each of its tags; "suite:" against its suite. "*" matches any text
// (including "/"); the match is case-insensitive and covers the whole value. A pattern with
// spaces goes in double quotes; the owner is the rest of the line and may have spaces.
package owners

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// Rules is an ordered set of owner rules.
type Rules struct {
	rules []rule
}

type rule struct {
	field string // key | tag | suite
	re    *regexp.Regexp
	owner string
}

// Test is what a rule looks at.
type Test struct {
	Key, Suite string
	Tags       []string
}

// Parse reads rules from text (one per line, or separated by ";" for an environment variable).
func Parse(text string) (*Rules, error) {
	r := &Rules{}
	sc := bufio.NewScanner(strings.NewReader(strings.ReplaceAll(text, ";", "\n")))
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		field, rest := "key", line
		for _, prefix := range []string{"tag", "suite"} {
			if after, found := strings.CutPrefix(rest, prefix+":"); found {
				field, rest = prefix, after
			}
		}
		var pattern, owner string
		if quoted, found := strings.CutPrefix(rest, `"`); found {
			end := strings.IndexByte(quoted, '"')
			if end < 0 {
				return nil, fmt.Errorf("owners line %d: unclosed quote", n)
			}
			pattern, owner = quoted[:end], quoted[end+1:]
		} else {
			pattern, owner, _ = strings.Cut(rest, " ")
		}
		owner = strings.TrimSpace(owner)
		if owner == "" {
			return nil, fmt.Errorf("owners line %d: %q needs a pattern and an owner", n, line)
		}
		if pattern == "" {
			return nil, fmt.Errorf("owners line %d: empty pattern", n)
		}
		r.rules = append(r.rules, rule{field: field, re: glob(pattern), owner: owner})
	}
	return r, sc.Err()
}

// FromEnv reads TRACEREPORTS_OWNERS_FILE (a file) and TRACEREPORTS_OWNERS (inline rules, after the
// file's). nil when neither is set.
func FromEnv() (*Rules, error) {
	var text strings.Builder
	if path := strings.TrimSpace(os.Getenv("TRACEREPORTS_OWNERS_FILE")); path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("TRACEREPORTS_OWNERS_FILE: %w", err)
		}
		text.Write(b)
		text.WriteString("\n")
	}
	text.WriteString(os.Getenv("TRACEREPORTS_OWNERS"))
	if strings.TrimSpace(text.String()) == "" {
		return nil, nil
	}
	return Parse(text.String())
}

func glob(pattern string) *regexp.Regexp {
	parts := strings.Split(pattern, "*")
	for i, p := range parts {
		parts[i] = regexp.QuoteMeta(p)
	}
	return regexp.MustCompile(`(?is)^` + strings.Join(parts, ".*") + `$`)
}

// Owner returns the owner of a test ("" when no rule matches). Nil rules own nothing.
func (r *Rules) Owner(t Test) string {
	if r == nil {
		return ""
	}
	owner := ""
	for _, ru := range r.rules {
		if ru.matches(t) {
			owner = ru.owner // la última que aplica gana, como CODEOWNERS
		}
	}
	return owner
}

// Len is the number of rules.
func (r *Rules) Len() int {
	if r == nil {
		return 0
	}
	return len(r.rules)
}

func (ru rule) matches(t Test) bool {
	switch ru.field {
	case "tag":
		for _, tag := range t.Tags {
			if ru.re.MatchString(strings.TrimSpace(tag)) {
				return true
			}
		}
		return false
	case "suite":
		return t.Suite != "" && ru.re.MatchString(t.Suite)
	}
	return t.Key != "" && ru.re.MatchString(t.Key)
}

// Tags splits a test category ("smoke, checkout") into tags.
func Tags(category string) []string {
	var out []string
	for _, t := range strings.Split(category, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}
