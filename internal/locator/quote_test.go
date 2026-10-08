package locator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

// Valores reales del DOM que rompían los snippets (y, si fueran hostiles, cambiarían el código
// copiado). Nada se ejecuta: se parsean los literales generados y se comparan con el original.
var trickyValues = []string{
	"O'Reilly-login", `say "hi"`, `back\slash`, "line1\nline2", "mix'\"\\end", "tab\there", "ñandú ✓",
	"</script>", "${x}", "back`tick", "cr\rlf",
}

// jsLiteral reads a JavaScript string literal ('…' or "…") at the start of s and returns its value
// and the rest. A raw newline or an unknown escape is a syntax error, as in JavaScript.
func jsLiteral(s string) (string, string, error) {
	if s == "" || (s[0] != '\'' && s[0] != '"') {
		return "", s, fmt.Errorf("no string literal at %q", s)
	}
	quote := s[0]
	var b strings.Builder
	for i := 1; i < len(s); {
		c := s[i]
		switch {
		case c == quote:
			return b.String(), s[i+1:], nil
		case c == '\n' || c == '\r':
			return "", "", fmt.Errorf("raw line break inside a literal")
		case c == '\\':
			if i+1 >= len(s) {
				return "", "", fmt.Errorf("dangling backslash")
			}
			e := s[i+1]
			i += 2
			switch e {
			case 'n':
				b.WriteByte('\n')
			case 'r':
				b.WriteByte('\r')
			case 't':
				b.WriteByte('\t')
			case '\\', '\'', '"', '`':
				b.WriteByte(e)
			case 'x':
				v, err := strconv.ParseUint(s[i:i+2], 16, 8)
				if err != nil {
					return "", "", err
				}
				b.WriteRune(rune(v))
				i += 2
			case 'u':
				v, err := strconv.ParseUint(s[i:i+4], 16, 16)
				if err != nil {
					return "", "", err
				}
				b.WriteRune(rune(v))
				i += 4
			default:
				return "", "", fmt.Errorf("unexpected escape \\%c", e)
			}
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
		}
	}
	return "", "", fmt.Errorf("unterminated literal")
}

// pyLiteral: the same for Python ('…' or "…"), whose escapes used here match JavaScript's.
func pyLiteral(s string) (string, string, error) { return jsLiteral(s) }

// cssString reads a CSS string ("…") at the start of s: \\ \" and hex escapes ("\a ").
func cssString(s string) (string, string, error) {
	if s == "" || s[0] != '"' {
		return "", s, fmt.Errorf("no CSS string at %q", s)
	}
	var b strings.Builder
	hex := regexp.MustCompile(`^[0-9a-fA-F]{1,6} ?`)
	for i := 1; i < len(s); {
		switch c := s[i]; {
		case c == '"':
			return b.String(), s[i+1:], nil
		case c == '\n':
			return "", "", fmt.Errorf("raw newline in a CSS string")
		case c == '\\':
			if m := hex.FindString(s[i+1:]); m != "" {
				v, _ := strconv.ParseUint(strings.TrimSpace(m), 16, 32)
				b.WriteRune(rune(v))
				i += 1 + len(m)
			} else {
				r, size := utf8.DecodeRuneInString(s[i+1:])
				b.WriteRune(r)
				i += 1 + size
			}
		default:
			r, size := utf8.DecodeRuneInString(s[i:])
			b.WriteRune(r)
			i += size
		}
	}
	return "", "", fmt.Errorf("unterminated CSS string")
}

// call parses `page.method(<lit>[, extra])` and returns the method, the literal and the extra.
func callOf(snippet, prefix string, lit func(string) (string, string, error)) (method, value, extra string, err error) {
	m := regexp.MustCompile(`^` + regexp.QuoteMeta(prefix) + `(\w+)\(`).FindStringSubmatch(snippet)
	if m == nil {
		return "", "", "", fmt.Errorf("not a %s call: %s", prefix, snippet)
	}
	value, rest, err := lit(snippet[len(m[0]):])
	if err != nil {
		return "", "", "", fmt.Errorf("%v in %s", err, snippet)
	}
	if !strings.HasSuffix(rest, ")") {
		return "", "", "", fmt.Errorf("trailing code after the literal in %s", snippet)
	}
	return m[1], value, strings.TrimSuffix(rest, ")"), nil
}

// selectorValue extracts the attribute value of a generated selector ([attr="…"], role=x[name="…"],
// text="…", #ident).
func selectorValue(sel string) (string, error) {
	if strings.HasPrefix(sel, "#") {
		return sel[1:], nil
	}
	i := strings.Index(sel, "=\"")
	if i < 0 {
		return "", fmt.Errorf("no quoted value in %s", sel)
	}
	v, rest, err := cssString(sel[i+1:])
	if err != nil {
		return "", err
	}
	if rest != "" && rest != "]" {
		return "", fmt.Errorf("trailing selector text %q in %s", rest, sel)
	}
	return v, nil
}

func elementsFor(v string) []Element {
	return []Element{
		{Tag: "div", TestID: v, Visible: true},
		{Tag: "div", TestID: v, TestIDAttr: "data-test", Visible: true},
		{Tag: "button", Text: v, Visible: true},
		{Tag: "input", Label: v, Visible: true},
		{Tag: "input", Placeholder: v, Visible: true},
		{Tag: "div", ID: v, Visible: true},
		{Tag: "input", Name: v, Visible: true},
		{Tag: "span", Text: v, Visible: true},
	}
}

func TestGeneratedSnippetsKeepTheValueAndParse(t *testing.T) {
	var js, py []string
	for _, v := range trickyValues {
		for _, e := range elementsFor(v) {
			for _, s := range options(e) {
				// el selector conserva el valor
				if sv, err := selectorValue(s.Selector); err != nil || (sv != v && strings.TrimSpace(v) == v) {
					t.Errorf("%s %q: selector %s -> %q (%v)", s.Kind, v, s.Selector, sv, err)
				}
				for _, lang := range []struct {
					name, code, prefix string
					lit                func(string) (string, string, error)
				}{{"js", s.JS, "page.", jsLiteral}, {"python", s.Python, "page.", pyLiteral}} {
					method, val, extra, err := callOf(lang.code, lang.prefix, lang.lit)
					if err != nil {
						t.Errorf("%s %s %q: %v", lang.name, s.Kind, v, err)
						continue
					}
					want := v
					if method == "locator" {
						want = s.Selector // page.locator('<selector>')
					}
					if s.Kind == "role" && (method == "getByRole" || method == "get_by_role") {
						// getByRole('<role>', { name: '<valor>' }) / get_by_role("<role>", name="<valor>")
						name, _, err := lang.lit(strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(extra, ", { name: "), ", name="), " "))
						if err != nil || name != v {
							t.Errorf("%s role name %q: %q %v in %s", lang.name, v, name, err, lang.code)
						}
						continue
					}
					if val != want {
						t.Errorf("%s %s %q: literal %q, want %q (%s)", lang.name, s.Kind, v, val, want, lang.code)
					}
				}
				js = append(js, s.JS+";")
				py = append(py, s.Python)
			}
		}
	}
	checkSyntax(t, js, py)
}

// checkSyntax parses (never runs) every snippet with node --check and Python's ast, when available.
func checkSyntax(t *testing.T, js, py []string) {
	dir := t.TempDir()
	if node, err := exec.LookPath("node"); err == nil {
		f := filepath.Join(dir, "snippets.js")
		os.WriteFile(f, []byte("async function never(page) {\n"+strings.Join(js, "\n")+"\n}\n"), 0o644)
		if out, err := exec.Command(node, "--check", f).CombinedOutput(); err != nil {
			t.Errorf("node --check: %s", out)
		}
	} else {
		t.Log("node not found: JS syntax checked only by the literal parser")
	}
	for _, name := range []string{"python3", "python"} {
		bin, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		if exec.Command(bin, "-c", "import ast").Run() != nil {
			continue // p. ej. el atajo de Microsoft Store que no es un intérprete real
		}
		f := filepath.Join(dir, "snippets.py")
		os.WriteFile(f, []byte(strings.Join(py, "\n")+"\n"), 0o644)
		out, err := exec.Command(bin, "-c", "import ast,sys; ast.parse(open(sys.argv[1], encoding='utf-8').read())", f).CombinedOutput()
		if err != nil {
			t.Errorf("python ast.parse: %s", out)
		}
		return
	}
	t.Log("python not found: Python syntax checked only by the literal parser")
}

func TestNormalSnippetsAreUnchanged(t *testing.T) {
	got := map[string]Suggestion{}
	for _, e := range []Element{
		{Tag: "button", TestID: "login-btn", Visible: true},
		{Tag: "input", Label: "Email", Placeholder: "you@example.com", Name: "email", ID: "email", Visible: true},
	} {
		for _, s := range options(e) {
			got[s.Kind+":"+s.Selector] = s
		}
	}
	for key, want := range map[string][2]string{
		`testid:[data-testid="login-btn"]`:            {`page.getByTestId('login-btn')`, `page.get_by_test_id("login-btn")`},
		`label:[aria-label="Email"]`:                  {`page.getByLabel('Email')`, `page.get_by_label("Email")`},
		`placeholder:[placeholder="you@example.com"]`: {`page.getByPlaceholder('you@example.com')`, `page.get_by_placeholder("you@example.com")`},
		`id:#email`:                {`page.locator('#email')`, `page.locator("#email")`},
		`name:input[name="email"]`: {`page.locator('input[name="email"]')`, `page.locator('input[name="email"]')`},
	} {
		s, ok := got[key]
		if !ok {
			t.Errorf("missing %s", key)
			continue
		}
		if s.JS != want[0] || s.Python != want[1] {
			t.Errorf("%s changed:\n js %s\n py %s", key, s.JS, s.Python)
		}
	}
}
