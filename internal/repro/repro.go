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
	shSafe    = regexp.MustCompile(`^[A-Za-z0-9_./:=@%+,-]+$`)
	commitRe  = regexp.MustCompile(`^[0-9a-fA-F]{4,64}$`)
	httpToken = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$") // RFC 9110: método y nombre de header
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
	if !httpToken.MatchString(method) {
		method = shq(method) // un método raro (datos antiguos) no se interpreta en la shell
	}
	parts := []string{fmt.Sprintf("curl -X %s %s", method, shq(u))}
	for _, k := range sortedKeys(c.RequestHeaders) {
		v := c.RequestHeaders[k]
		if strings.HasPrefix(k, ":") {
			continue
		}
		if v == redact.Mask || sensitiveKey.MatchString(k) || p.SensitiveHeader(k) {
			if !httpToken.MatchString(k) { // dentro de comillas dobles la shell interpretaría $( ), ` y "
				parts = append(parts, "-H "+shq(k+": "+Mask))
				continue
			}
			parts = append(parts, fmt.Sprintf(`-H "%s: $%s"`, k, envVar(k)))
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
	Label string `json:"label"`
	Cmd   string `json:"cmd"`
}

var (
	pwKey    = regexp.MustCompile(`^(.*?) > (.*?)(?: \[([^\]]+)\])?$`)
	javaKey  = regexp.MustCompile(`^([\w.$]+)#([\w$]+)`)
	reSyntax = regexp.MustCompile(`[.*+?^${}()|\[\]\\]`)
)

func reEsc(s string) string { return reSyntax.ReplaceAllString(s, `\$0`) }

// Commands runs the test locally according to the framework of the run and the test key
// (pytest nodeid, Playwright title path, Java Class#method, Go package/TestName). With the
// commit, each command first checks it out.
func Commands(framework, key, commit string) []Command {
	fw := strings.ToLower(framework)
	var out []Command
	if key == "" || strings.HasPrefix(key, "name:") {
		return nil
	}
	switch fw {
	case "pytest":
		out = append(out, Command{"pytest", "pytest " + shq(key)})
	case "playwright":
		if m := pwKey.FindStringSubmatch(key); m != nil {
			t := strings.Split(m[2], " > ")
			cmd := fmt.Sprintf("npx playwright test %s -g %s", shq(m[1]), shq("^"+reEsc(t[len(t)-1])+"$"))
			if m[3] != "" {
				cmd += " --project=" + shq(m[3])
			}
			out = append(out, Command{"Playwright", cmd})
		}
	case "junit5", "junit", "maven", "gradle", "testng":
		if m := javaKey.FindStringSubmatch(key); m != nil {
			cls := m[1][strings.LastIndex(m[1], ".")+1:]
			out = append(out, Command{"Maven", "mvn test -Dtest=" + shq(cls+"#"+m[2])},
				Command{"Gradle", "./gradlew test --tests " + shq(m[1]+"."+m[2])})
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
			out = append(out, Command{"Go", fmt.Sprintf("go test %s -run %s", shArg("./"+pkg+"..."), shq(strings.Join(run, "/")))})
			break
		}
	}
	// solo un commit de Git válido: también protege lo guardado por versiones anteriores
	if commit = NormalizeCommit(commit); commit != "" {
		if len(commit) > 12 {
			commit = commit[:12]
		}
		for i := range out {
			out[i].Cmd = "git checkout " + commit + " && " + out[i].Cmd
		}
	}
	return out
}
