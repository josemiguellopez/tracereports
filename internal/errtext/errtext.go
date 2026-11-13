// Package errtext turns a test's error message into the short text a person reads first (the title of
// an incident, the evidence of an escalation, the triage of a test): without the wrappers frameworks put
// around it, the first sentence that says something, cut on a word and never in the middle of a letter.
package errtext

import (
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	// a wrapper that names the test file before a serialized list of errors:
	// "Errores acumulados para el file_name:test_x.py: [\"Error Linea 37 …\", \"…\"]"
	fileWrapRe = regexp.MustCompile(`(?s)^[^\n\[]{0,240}?[\w./\\-]+\.(?:py|js|mjs|cjs|ts|tsx|jsx|java|kt|cs|rb|go|php|feature|robot|spec)\b[^\n\[]{0,40}?:\s*\[\s*(.*)$`)
	// a whole message that is a serialized list: ["uno", "dos"] or ['uno', 'dos']
	listRe = regexp.MustCompile(`(?s)^\[\s*(.*)$`)
	// Python/JSON quotes escaped inside the list
	escRe = strings.NewReplacer(`\"`, `"`, `\'`, `'`, `\n`, "\n", `\t`, " ")
)

// Useful is the first informative line of an error message, without the file-name wrapper and the
// serialized list around it ("" when the message has nothing).
func Useful(msg string) string {
	s := strings.TrimSpace(msg)
	if m := fileWrapRe.FindStringSubmatch(s); m != nil {
		s = firstItem(m[1])
	} else if m := listRe.FindStringSubmatch(s); m != nil && quoted(m[1]) {
		s = firstItem(m[1])
	}
	header := "" // "Errores:" sobre la lista: solo si no hay nada mejor
	for _, l := range strings.Split(s, "\n") {
		if l = strings.Join(strings.Fields(l), " "); !informative(l) {
			continue
		}
		if !strings.HasSuffix(l, ":") {
			return l
		}
		if header == "" {
			header = l
		}
	}
	if header != "" {
		return header
	}
	return strings.Join(strings.Fields(s), " ")
}

// firstItem is the first element of a serialized list of strings (its quotes and escapes undone).
func firstItem(s string) string {
	s = strings.TrimSpace(s)
	if !quoted(s) {
		return strings.TrimSpace(strings.TrimSuffix(s, "]"))
	}
	q := s[0]
	s = s[1:]
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' {
			i++
			continue
		}
		if s[i] == q {
			return strings.TrimSpace(escRe.Replace(s[:i]))
		}
	}
	return strings.TrimSpace(strings.TrimRight(escRe.Replace(s), `"']`)) // la lista llegó cortada
}

func quoted(s string) bool { return s != "" && (s[0] == '"' || s[0] == '\'') }

// informative: a line with words, not just a prefix that opens a list or a block.
func informative(l string) bool {
	if l == "" || strings.HasSuffix(l, "[") || strings.HasSuffix(l, "{") {
		return false
	}
	letters := 0
	for _, r := range l {
		if unicode.IsLetter(r) {
			letters++
		}
	}
	return letters >= 3
}

// Cut leaves s in at most n characters (letters, not bytes): on a word when there is one near the end,
// without a dangling "[", "(", ":" or ",", and with "…" when something was left out.
func Cut(s string, n int) string {
	s = strings.TrimSpace(s)
	if n <= 0 || utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n]
	if i := lastSpace(r); i >= n*2/3 {
		r = r[:i]
	}
	out := strings.TrimRightFunc(string(r), func(c rune) bool { return unicode.IsSpace(c) || strings.ContainsRune("[({:,;-=\"'", c) })
	return out + "…"
}

func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if unicode.IsSpace(r[i]) {
			return i
		}
	}
	return -1
}

// Short is Useful and then Cut: the text for a title or a line of evidence.
func Short(msg string, n int) string { return Cut(Useful(msg), n) }
