// Package repro builds what a developer needs to reproduce a failure: the cURL of a backend
// call and the command that runs the test locally. It mirrors curlOf and reproCommands of the
// UI (web/tracereports_features.js), so a ticket says the same as the report.
package repro

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

// Mask replaces secrets that were masked when the call was stored.
const Mask = "***"

var (
	sensitiveKey = regexp.MustCompile(`(?i)(pass(word)?|pwd|clave|token|secret|session|auth|cookie|rut|api[_-]?key)`)
	rut          = regexp.MustCompile(`\b\d{1,2}\.?\d{3}\.?\d{3}-[\dkK]\b`)
	masked       = regexp.MustCompile(`(?i)<masked>|%3Cmasked%3E|<rut>`)
	nonWord      = regexp.MustCompile(`\W+`)
)

// shq quotes a value for a POSIX shell.
func shq(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

var (
	shSafe   = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)
	commitRe = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)
	// nombres de header que pueden ir entre comillas dobles junto a la variable: sin nada que la shell
	// interprete ahí ($ ` " \ !). Un token HTTP válido no basta: admite | & ` $ ! ' * ~ ^ #
	plainHeader = regexp.MustCompile(`^[A-Za-z0-9_.-]+$`)
)

// shArg leaves a word as is when it only has characters a shell does not interpret, and quotes
// it otherwise: normal commands stay identical, anything else becomes a literal argument.
func shArg(s string) string {
	if shSafe.MatchString(s) {
		return s
	}
	return shq(s)
}

// ValidCommit reports whether c is a Git commit id: 4 to 64 hex characters (abbreviated, SHA-1 or
// SHA-256). It is the format of the commit a run carries; anything else is not used in commands.
func ValidCommit(c string) bool { return commitRe.MatchString(c) }

// NormalizeCommit returns the commit in lowercase, or "" when it is not a valid Git commit id.
func NormalizeCommit(c string) string {
	c = strings.TrimSpace(c)
	if !ValidCommit(c) {
		return ""
	}
	return strings.ToLower(c)
}

// Curl is the call as a cURL command. Masked or sensitive headers become $VARIABLES (the
// developer exports the real value) and sensitive query or JSON fields become ***.
func Curl(c *db.NetConn, p *redact.Policy) string {
	u := c.URL
	if pu, err := url.Parse(u); err == nil && pu.Host != "" {
		q := pu.Query()
		changed := false
		for k := range q {
			if sensitiveKey.MatchString(k) {
				q.Set(k, Mask)
				changed = true
			}
		}
		if changed {
			pu.RawQuery = q.Encode()
		}
		u = pu.String()
	}
	u = masked.ReplaceAllString(strings.ReplaceAll(u, "%2A%2A%2A", Mask), Mask)
	method := c.Method
	if method == "" {
		method = "GET"
	}
	// el método es un argumento literal: un token HTTP válido puede llevar | & ` $ y otros que la
	// shell interpreta (datos antiguos o de un cliente cualquiera)
	parts := []string{fmt.Sprintf("curl -X %s %s", shArg(method), shq(u))}
	for _, k := range sortedKeys(c.RequestHeaders) {
		v := c.RequestHeaders[k]
		if strings.HasPrefix(k, ":") {
			continue
		}
		if v == redact.Mask || sensitiveKey.MatchString(k) || p.SensitiveHeader(k) {
			// solo se expande la variable generada (envVar: [A-Z0-9_], nunca empieza con dígito);
			// el nombre va literal: entre comillas dobles solo si no tiene nada que la shell interprete
			if plainHeader.MatchString(k) {
				parts = append(parts, fmt.Sprintf(`-H "%s: $%s"`, k, envVar(k)))
			} else {
				parts = append(parts, "-H "+shq(k+": ")+`"$`+envVar(k)+`"`)
			}
		} else {
			parts = append(parts, "-H "+shq(k+": "+v))
		}
	}
	if c.PostData != "" {
		body := masked.ReplaceAllString(rut.ReplaceAllString(c.PostData, Mask), Mask)
		var v any
		if json.Unmarshal([]byte(c.PostData), &v) == nil {
			before, _ := json.Marshal(v)
			if after, err := json.Marshal(maskValue(v, "")); err == nil && string(after) != string(before) {
				body = string(after) // con algo enmascarado; si no, el body tal como se envió
			}
		}
		parts = append(parts, "--data-raw "+shq(body))
	}
	return strings.Join(parts, " \\\n  ")
}

// envVar is the shell variable the developer exports for a masked header (AUTHORIZATION,
// X_API_KEY...): a valid name, never starting with a digit.
func envVar(header string) string {
	v := nonWord.ReplaceAllString(strings.ToUpper(header), "_")
	if v == "" || (v[0] >= '0' && v[0] <= '9') {
		v = "H_" + v
	}
	return v
}

// maskValue masks sensitive fields of a decoded JSON value (by key, and RUT-like values).
func maskValue(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = maskValue(val, k)
		}
		return x
	case []any:
		for i := range x {
			x[i] = maskValue(x[i], "")
		}
		return x
	case nil:
		return nil
	}
	if key != "" && sensitiveKey.MatchString(key) {
		return Mask
	}
	if s, ok := v.(string); ok {
		return masked.ReplaceAllString(rut.ReplaceAllString(s, Mask), Mask)
	}
	return v
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ { // pocos headers: inserción
		for j := i; j > 0 && strings.ToLower(keys[j]) < strings.ToLower(keys[j-1]); j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// Command is one way to run the test locally.
type Command struct {
	Label string `json:"label"` // herramienta: pytest, Playwright, Maven, Gradle, Go
	Cmd   string `json:"cmd"`
	// Scope: what it runs. ScopeTest, only the failed test; ScopeFile / ScopeClass, the whole file or
	// class (a test that depends on earlier ones, like a flow split in steps, fails alone for another reason).
	Scope string `json:"scope"`
}

// Scopes of a command.
const (
	ScopeTest  = "test"
	ScopeFile  = "file"
	ScopeClass = "class"
)

var (
	pwKey   = regexp.MustCompile(`^(.*?) > (.*?)(?: \[([^\]]+)\])?$`)
	javaKey = regexp.MustCompile(`^([\w.$]+)#([\w$]+)`)
	// Allure keys: allure-playwright "file:line:col [Project=chromium]", allure-junit5
	// "package.Class.method" and allure-pytest "pkg.module#test" (see pytestNodeID).
	allurePWKey   = regexp.MustCompile(`^(\S+?\.[cm]?[jt]sx?):(\d+):\d+(?: \[Project=([^\],]+)\])?$`)
	allureJavaKey = regexp.MustCompile(`^([\w$]+(?:\.[\w$]+)*\.[A-Z][\w$]*)\.([\w$]+)(?: \[.*\])?$`)
	reSyntax      = regexp.MustCompile(`[.*+?^${}()|\[\]\\]`)
)

func reEsc(s string) string { return reSyntax.ReplaceAllString(s, `\$0`) }

// pytestNodeID turns the allure-pytest key "pkg.module.TestClass#test_x [p=1]" into the pytest
// node id "pkg/module.py::TestClass::test_x" (every parametrization). A pytest node id is returned
// as is.
func pytestNodeID(key string) string {
	mod, fn, ok := strings.Cut(key, "#")
	if !ok || strings.Contains(key, "::") {
		return key
	}
	fn, _, _ = strings.Cut(fn, " [")
	parts := strings.Split(mod, ".")
	i := len(parts)
	for i > 1 && parts[i-1] != "" && unicode.IsUpper(rune(parts[i-1][0])) {
		i-- // las clases van al final del nombre: TestCart
	}
	id := strings.Join(parts[:i], "/") + ".py"
	for _, c := range parts[i:] {
		id += "::" + c
	}
	return id + "::" + fn
}

// Commands runs the test locally according to the framework of the run and the test key
// (pytest nodeid, Playwright title path, Java Class#method, Go package/TestName): first only the
// test, then its whole file or class. No git checkout: the commit of the run is shown apart (Script),
// a checkout fails with local changes and leaves a detached HEAD.
func Commands(framework, key string) []Command {
	fw := strings.ToLower(framework)
	var out, whole []Command
	if key == "" || strings.HasPrefix(key, "name:") {
		return nil
	}
	project := func(p string) string {
		if p == "" {
			return ""
		}
		return " --project=" + shq(p)
	}
	switch fw {
	case "pytest":
		id := pytestNodeID(key)
		out = append(out, Command{"pytest", "pytest " + shq(id), ScopeTest})
		if file, _, ok := strings.Cut(id, "::"); ok && file != "" {
			whole = append(whole, Command{"pytest", "pytest " + shq(file), ScopeFile})
		}
	case "playwright":
		if m := allurePWKey.FindStringSubmatch(key); m != nil { // Allure: archivo:línea:columna
			out = append(out, Command{"Playwright", "npx playwright test " + shq(m[1]+":"+m[2]) + project(m[3]), ScopeTest})
			whole = append(whole, Command{"Playwright", "npx playwright test " + shq(m[1]) + project(m[3]), ScopeFile})
		} else if m := pwKey.FindStringSubmatch(key); m != nil {
			t := strings.Split(m[2], " > ")
			out = append(out, Command{"Playwright", fmt.Sprintf("npx playwright test %s -g %s", shq(m[1]), shq("^"+reEsc(t[len(t)-1])+"$")) + project(m[3]), ScopeTest})
			whole = append(whole, Command{"Playwright", "npx playwright test " + shq(m[1]) + project(m[3]), ScopeFile})
		}
	case "junit5", "junit", "maven", "gradle", "testng", "junit-platform":
		if m := allureJavaKey.FindStringSubmatch(key); m != nil && !strings.Contains(key, "#") {
			key = m[1] + "#" + m[2] // Allure: paquete.Clase.metodo
		}
		if m := javaKey.FindStringSubmatch(key); m != nil {
			cls := m[1][strings.LastIndex(m[1], ".")+1:]
			out = append(out, Command{"Maven", "mvn test -Dtest=" + shq(cls+"#"+m[2]), ScopeTest},
				Command{"Gradle", "./gradlew test --tests " + shq(m[1]+"."+m[2]), ScopeTest})
			whole = append(whole, Command{"Maven", "mvn test -Dtest=" + shq(cls), ScopeClass},
				Command{"Gradle", "./gradlew test --tests " + shq(m[1]), ScopeClass})
		}
	case "go":
		parts := strings.Split(key, "/")
		for i, p := range parts {
			if !strings.HasPrefix(p, "Test") {
				continue
			}
			pkg := strings.Join(parts[:i], "/")
			run := make([]string, 0, len(parts)-i)
			for _, s := range parts[i:] {
				run = append(run, "^"+reEsc(s)+"$")
			}
			if pkg != "" {
				pkg += "/"
			}
			out = append(out, Command{"Go", fmt.Sprintf("go test %s -run %s", shArg("./"+pkg+"..."), shq(strings.Join(run, "/"))), ScopeTest})
			break
		}
	}
	return append(out, whole...)
}

// dependsRe: the failure is about something an earlier test of the same file should have left
// (empty data, a step skipped because the previous one failed): run alone, the test fails for that.
var dependsRe = regexp.MustCompile(`(?i)\bvac[ií][oa]s?\b|\bempty\b|\bis (?:none|null|undefined)\b|\bnonetype\b|\bomitid[oa]s?\b|\bomitted\b|\bskipped because\b|\bpaso anterior\b|\bprevious (?:step|test)\b|\bprecondici[oó]n\b|\bprecondition\b|\bdepende de\b|\bdepends on\b`)

// DependsOnEarlier reports whether a failure message says the test needed an earlier one.
func DependsOnEarlier(errorMessage string) bool {
	if len(errorMessage) > 600 {
		errorMessage = errorMessage[:600]
	}
	return dependsRe.MatchString(errorMessage)
}

// Ordered puts the whole file or class first when fileFirst (a test that depends on earlier ones).
func Ordered(cmds []Command, fileFirst bool) []Command {
	if !fileFirst {
		return cmds
	}
	out := make([]Command, 0, len(cmds))
	for _, c := range cmds {
		if c.Scope != ScopeTest {
			out = append(out, c)
		}
	}
	for _, c := range cmds {
		if c.Scope == ScopeTest {
			out = append(out, c)
		}
	}
	return out
}

var scriptText = map[string]map[string]string{
	"es": {ScopeTest: "Este test", ScopeFile: "Todo el archivo (si el test depende de otros anteriores del mismo archivo)",
		ScopeClass: "Toda la clase (si el test depende de otros anteriores de la misma clase)",
		"commit":   "La ejecución usó el commit %s; si tu copia está en otra versión: git checkout %s"},
	"en": {ScopeTest: "This test", ScopeFile: "The whole file (if the test depends on earlier ones of the same file)",
		ScopeClass: "The whole class (if the test depends on earlier ones of the same class)",
		"commit":   "The run used commit %s; if your copy is on another version: git checkout %s"},
}

// Script is the commands as one shell block, each under a comment that says what it runs, and the
// commit of the run as a note (never chained to the command).
func Script(cmds []Command, commit, lang string) string {
	t := scriptText[lang]
	if t == nil {
		t = scriptText["es"]
	}
	var b strings.Builder
	for i, c := range cmds {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "# %s · %s\n%s\n", t[c.Scope], c.Label, c.Cmd)
	}
	if commit = NormalizeCommit(commit); commit != "" && len(cmds) > 0 {
		if len(commit) > 12 {
			commit = commit[:12]
		}
		fmt.Fprintf(&b, "\n# "+t["commit"]+"\n", commit, commit)
	}
	return strings.TrimRight(b.String(), "\n")
}
