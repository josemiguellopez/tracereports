package locator

import (
	"regexp"
	"strings"
)

// Present tells whether a selector finds an element in a structural snapshot, without a browser. It
// only answers what it can be sure of: sure is false for a selector it cannot read (axes, positions,
// functions other than contains/normalize-space/translate, CSS combinators). It reads the common shapes
// of a test's selectors: //tag[@attr='v'], contains(@class,'v'), contains(normalize-space(.),'v')
// (also with translate(…) to lower case), text()='v', and CSS #id, .class, [attr=v] and tag.
func Present(sel string, nodes []Node) (found, sure bool) {
	sel = strings.TrimSpace(sel)
	if sel == "" || len(nodes) == 0 {
		return false, false
	}
	var q *query
	if strings.HasPrefix(sel, "/") || strings.HasPrefix(sel, "(") {
		q = parseXPath(sel)
	} else {
		q = parseCSS(sel)
	}
	if q == nil {
		return false, false
	}
	texts := subtreeTexts(nodes)
	for i, n := range nodes {
		if q.match(n, texts[i]) {
			return true, true
		}
	}
	return false, true
}

// query is one element test: its tag ("" = any) and what its attributes and its text must hold.
type query struct {
	tag   string
	conds []cond
}

type cond struct {
	attr  string // "" = el texto del elemento (con sus descendientes)
	value string
	exact bool // = en vez de contains
	lower bool // comparado en minúsculas (translate(…) o un CSS de clase)
	word  bool // una clase de CSS: una palabra entera del atributo class
}

func (q *query) match(n Node, text string) bool {
	if q.tag != "" && !strings.EqualFold(q.tag, n.Tag) {
		return false
	}
	for _, c := range q.conds {
		got := text
		if c.attr != "" {
			v, ok := n.Attrs[c.attr]
			if !ok {
				return false
			}
			got = v
		}
		want := c.value
		if c.lower {
			got, want = strings.ToLower(got), strings.ToLower(want)
		}
		switch {
		case c.word:
			if !hasWord(got, want) {
				return false
			}
		case c.exact:
			if got != want {
				return false
			}
		default:
			if !strings.Contains(got, want) {
				return false
			}
		}
	}
	return true
}

func hasWord(s, w string) bool {
	for _, f := range strings.Fields(s) {
		if f == w {
			return true
		}
	}
	return false
}

// subtreeTexts: the text of each node with its descendants (what "." and normalize-space(.) see),
// with spaces normalized.
func subtreeTexts(nodes []Node) []string {
	parts := make([][]string, len(nodes))
	for i := len(nodes) - 1; i >= 0; i-- { // un hijo va después de su padre: de atrás hacia adelante
		n := nodes[i]
		own := append([]string{n.Text}, parts[i]...)
		parts[i] = own
		if n.P >= 0 && n.P < i {
			parts[n.P] = append(own, parts[n.P]...)
		}
	}
	out := make([]string, len(nodes))
	for i, p := range parts {
		out[i] = strings.Join(strings.Fields(strings.Join(p, " ")), " ")
	}
	return out
}

var (
	xpStepRe = regexp.MustCompile(`^\(?//([a-zA-Z][\w-]*|\*)\[(.+)\]\)?$`)
	xpBareRe = regexp.MustCompile(`^//([a-zA-Z][\w-]*)$`)
	// las condiciones que se entienden, unidas con "and"
	xpCondRes = []*regexp.Regexp{
		regexp.MustCompile(`^@([\w:-]+)\s*=\s*(?:'([^']*)'|"([^"]*)")$`),
		regexp.MustCompile(`^contains\(\s*@([\w:-]+)\s*,\s*(?:'([^']*)'|"([^"]*)")\s*\)$`),
		regexp.MustCompile(`^contains\(\s*(?:normalize-space\((?:\.|text\(\))?\)|\.|text\(\))\s*,\s*(?:'([^']*)'|"([^"]*)")\s*\)$`),
		regexp.MustCompile(`^(?:normalize-space\((?:\.|text\(\))?\)|\.|text\(\))\s*=\s*(?:'([^']*)'|"([^"]*)")$`),
		regexp.MustCompile(`^contains\(\s*translate\(\s*(?:normalize-space\((?:\.|text\(\))?\)|\.|text\(\))\s*,\s*'[A-ZÁÉÍÓÚÑ]+'\s*,\s*'[a-záéíóúñ]+'\s*\)\s*,\s*(?:'([^']*)'|"([^"]*)")\s*\)$`),
	}
)

func parseXPath(sel string) *query {
	if m := xpBareRe.FindStringSubmatch(sel); m != nil {
		return &query{tag: m[1]}
	}
	m := xpStepRe.FindStringSubmatch(sel)
	if m == nil {
		return nil
	}
	q := &query{tag: m[1]}
	if q.tag == "*" {
		q.tag = ""
	}
	for _, part := range splitAnd(m[2]) {
		c, ok := xpCond(strings.TrimSpace(part))
		if !ok {
			return nil
		}
		q.conds = append(q.conds, c)
	}
	return q
}

func xpCond(p string) (cond, bool) {
	val := func(m []string, i int) string {
		if m[i] != "" {
			return m[i]
		}
		return m[i+1]
	}
	if m := xpCondRes[0].FindStringSubmatch(p); m != nil {
		return cond{attr: m[1], value: val(m, 2), exact: true}, true
	}
	if m := xpCondRes[1].FindStringSubmatch(p); m != nil {
		return cond{attr: m[1], value: val(m, 2)}, true
	}
	if m := xpCondRes[2].FindStringSubmatch(p); m != nil {
		return cond{value: strings.Join(strings.Fields(val(m, 1)), " ")}, true
	}
	if m := xpCondRes[3].FindStringSubmatch(p); m != nil {
		return cond{value: strings.Join(strings.Fields(val(m, 1)), " "), exact: true}, true
	}
	if m := xpCondRes[4].FindStringSubmatch(p); m != nil {
		return cond{value: strings.Join(strings.Fields(val(m, 1)), " "), lower: true}, true
	}
	return cond{}, false
}

// splitAnd splits the predicate on " and " outside quotes and parentheses ("or" is not read).
func splitAnd(s string) []string {
	var out []string
	depth, start := 0, 0
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '\'' || c == '"':
			quote = c
		case c == '(' || c == '[':
			depth++
		case c == ')' || c == ']':
			depth--
		case depth == 0 && strings.HasPrefix(s[i:], " or "):
			return []string{"\x00"} // no se entiende: xpCond lo rechaza
		case depth == 0 && strings.HasPrefix(s[i:], " and "):
			out = append(out, s[start:i])
			start = i + 5
			i += 4
		}
	}
	return append(out, s[start:])
}

var (
	cssRe     = regexp.MustCompile(`^([a-zA-Z][\w-]*)?((?:#[\w-]+|\.[\w-]+|\[[\w:-]+(?:[*^$~]?=(?:"[^"]*"|'[^']*'|[\w-]+))?\])*)$`)
	cssPartRe = regexp.MustCompile(`#[\w-]+|\.[\w-]+|\[([\w:-]+)(?:([*^$~]?=)(?:"([^"]*)"|'([^']*)'|([\w-]+)))?\]`)
)

func parseCSS(sel string) *query {
	m := cssRe.FindStringSubmatch(sel)
	if m == nil || (m[1] == "" && m[2] == "") {
		return nil
	}
	q := &query{tag: m[1]}
	for _, p := range cssPartRe.FindAllStringSubmatch(m[2], -1) {
		switch {
		case strings.HasPrefix(p[0], "#"):
			q.conds = append(q.conds, cond{attr: "id", value: p[0][1:], exact: true})
		case strings.HasPrefix(p[0], "."):
			q.conds = append(q.conds, cond{attr: "class", value: p[0][1:], word: true})
		default:
			v := p[3] + p[4] + p[5]
			switch p[2] {
			case "":
				q.conds = append(q.conds, cond{attr: p[1], value: ""})
			case "=":
				q.conds = append(q.conds, cond{attr: p[1], value: v, exact: true})
			case "*=":
				q.conds = append(q.conds, cond{attr: p[1], value: v})
			case "~=":
				q.conds = append(q.conds, cond{attr: p[1], value: v, word: true})
			default:
				return nil // ^= y $=: no se leen
			}
		}
	}
	return q
}
