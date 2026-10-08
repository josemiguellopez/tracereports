// Package redact masks secrets in the evidence TraceReports stores: headers, URLs, request and
// response bodies, step messages, errors and DOM snapshots. It runs on the server before
// anything is persisted, so the database, AI prompts, exported ZIPs and Teams/Slack messages
// never see the original values, whatever client (Python, Go, REST) sent them. The clients may
// mask too; this is the layer that does not depend on them.
//
// Limits: it recognizes secrets by their key (header, JSON field, query or form parameter) and
// by a few unambiguous shapes (Bearer tokens, JWTs, credentials in URLs). A secret written as
// free text without a key, or visible inside a screenshot, is not detected.
package redact

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/env"
)

// Mask replaces every redacted value.
const Mask = "<masked>"

// Policy is a set of masking rules. The zero value masks nothing; use Default or FromEnv.
type Policy struct {
	enabled  bool
	headers  map[string]bool
	keys     map[string]bool
	suffixes []string
	patterns []*regexp.Regexp
}

var (
	defaultHeaders = []string{"authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key", "api-key",
		"x-auth-token", "x-access-token", "x-csrf-token", "x-xsrf-token", "x-amz-security-token", "x-goog-api-key"}
	// keys are compared lowercased and without '-', '_' and '.'
	defaultKeys = []string{"password", "passwd", "pwd", "pass", "secret", "token", "auth", "authorization", "apikey",
		"accesstoken", "refreshtoken", "idtoken", "clientsecret", "session", "sessionid", "sid", "cookie", "sig",
		"signature", "privatekey", "otp", "pin", "cvv", "cvc", "cardnumber", "creditcard", "clave", "contrasena", "contraseña"}
	// a key ending in one of these is sensitive too (userPassword, csrf_token, x_api_key...)
	defaultSuffixes = []string{"password", "passwd", "secret", "token", "apikey", "sessionid"}

	bearerRe   = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9\-._~+/]{8,}=*`)
	jwtRe      = regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}`)
	userinfoRe = regexp.MustCompile(`(?i)\b([a-z][a-z0-9+.-]*://)[^/\s:@"']+:[^/\s@"']+@`)
	// "key": "value" | number | true/false   (JSON, also inside logs or traces); the key may carry
	// JSON escapes ("\u0074oken"). Keys of any length: a sensitive key is whatever the policy says
	// (TRACEREPORTS_REDACT_KEYS has no length limit), and Go's regexp stays linear in the text
	jsonKVRe = regexp.MustCompile(`"((?:[^"\\]|\\.)+)"(\s*:\s*)("(?:[^"\\]|\\.)*"|-?\d[\d.eE+-]*|true|false)`)
	// "key": "value…   a string cut at the end of the text (a truncated body or field: the closing
	// quote was cut off, the value may still be the whole secret). Reference definition: Text uses
	// openTail, which finds the same without scanning the whole text (tests compare both)
	jsonOpenTailRe = regexp.MustCompile(`"((?:[^"\\]|\\.)+)"(\s*:\s*)"(?:[^"\\]|\\.)*\\?$`)
	// the key part of jsonOpenTailRe, ending at the value's opening quote (openTail's rare path)
	jsonOpenKeyRe = regexp.MustCompile(`"((?:[^"\\]|\\.)+)"(\s*:\s*)"$`)
	// "key": { ... } | [ ... ]   (a whole object or array under a sensitive key)
	jsonContainerRe = regexp.MustCompile(`"((?:[^"\\]|\\.)+)"\s*:\s*[\[{]`)
	// key=value   (query strings, form bodies, logs); the key may be percent-encoded (%74oken)
	formKVRe = regexp.MustCompile(`([A-Za-z0-9_.\-\[\]%+]+)=([^&\s"'<>,;]*)`)
	// key: value  (headers or YAML-ish lines copied into logs)
	colonKVRe = regexp.MustCompile(`(?im)^(\s*[A-Za-z0-9_.\-]+)(\s*:\s+)(\S.*)$`)
)

// Default is the built-in policy.
func Default() *Policy {
	p := &Policy{enabled: true, headers: map[string]bool{}, keys: map[string]bool{}, suffixes: defaultSuffixes}
	for _, h := range defaultHeaders {
		p.headers[h] = true
	}
	for _, k := range defaultKeys {
		p.keys[normKey(k)] = true
	}
	return p
}

// FromEnv builds the policy from the environment:
//
//	TRACEREPORTS_REDACT=off            disables masking (not recommended)
//	TRACEREPORTS_REDACT_HEADERS=a,b    more header names to mask
//	TRACEREPORTS_REDACT_KEYS=a,b       more JSON/query/form keys to mask
//	TRACEREPORTS_REDACT_PATTERNS=re;re more regular expressions whose matches are masked
//	                              (e.g. a national id: \b\d{7,8}-[\dkK]\b)
func FromEnv() *Policy {
	p := Default()
	if v := strings.ToLower(strings.TrimSpace(env.Get("REDACT"))); v == "off" || v == "0" || v == "false" {
		p.enabled = false
		return p
	}
	for _, h := range splitList(env.Get("REDACT_HEADERS"), ",") {
		p.headers[strings.ToLower(h)] = true
	}
	for _, k := range splitList(env.Get("REDACT_KEYS"), ",") {
		p.keys[normKey(k)] = true
	}
	for _, expr := range splitList(env.Get("REDACT_PATTERNS"), ";") {
		if re, err := regexp.Compile(expr); err == nil {
			p.patterns = append(p.patterns, re)
		}
	}
	return p
}

// Enabled reports whether the policy masks anything.
func (p *Policy) Enabled() bool { return p != nil && p.enabled }

func splitList(s, sep string) []string {
	var out []string
	for _, part := range strings.Split(s, sep) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func normKey(k string) string {
	k = strings.ToLower(strings.TrimSpace(k))
	return strings.NewReplacer("-", "", "_", "", ".", "", "[", "", "]", "").Replace(k)
}

// SensitiveKey reports whether a JSON field, query/form parameter or similar key holds secrets.
func (p *Policy) SensitiveKey(k string) bool {
	if !p.Enabled() {
		return false
	}
	n := normKey(k)
	if n == "" {
		return false
	}
	if p.keys[n] {
		return true
	}
	for _, suf := range p.suffixes {
		if strings.HasSuffix(n, suf) {
			return true
		}
	}
	return false
}

// SensitiveHeader reports whether a header carries credentials.
func (p *Policy) SensitiveHeader(name string) bool {
	return p.Enabled() && (p.headers[strings.ToLower(strings.TrimSpace(name))] || p.SensitiveKey(name))
}

// jsonKey decodes a JSON key captured by the regexes (it may contain escapes like \u0074).
func jsonKey(raw string) string {
	if !strings.Contains(raw, `\`) {
		return raw
	}
	if k, err := strconv.Unquote(`"` + raw + `"`); err == nil {
		return k
	}
	return raw
}

// formKey decodes a percent-encoded query/form key ("%74oken" -> "token", "api+key" -> "api key").
func formKey(raw string) string {
	if k, err := url.QueryUnescape(raw); err == nil {
		return k
	}
	return raw
}

// closeOf returns the index just after the bracket that closes the one at s[open], skipping JSON
// strings; -1 if it is not closed (truncated body).
func closeOf(s string, open int) int {
	depth, inStr := 0, false
	for i := open; i < len(s); i++ {
		c := s[i]
		if inStr {
			switch c {
			case '\\':
				i++
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i + 1
			}
		}
	}
	return -1
}

// maskContainers replaces a whole object or array under a sensitive key: {"token": ["x"]} or
// {"password": {"value": "x"}}. A truncated container is masked up to the end of the text.
func (p *Policy) maskContainers(s string) string {
	var b strings.Builder
	last := 0
	for _, m := range jsonContainerRe.FindAllStringSubmatchIndex(s, -1) {
		if m[0] < last || !p.SensitiveKey(jsonKey(s[m[2]:m[3]])) {
			continue
		}
		open := m[1] - 1
		end := closeOf(s, open)
		if end < 0 {
			end = len(s)
		}
		b.WriteString(s[last:open])
		b.WriteString(`"` + Mask + `"`)
		last = end
	}
	if last == 0 {
		return s
	}
	b.WriteString(s[last:])
	return b.String()
}

// Text masks secrets in free text: bodies (JSON, form or plain), URLs, messages and traces.
func (p *Policy) Text(s string) string {
	if !p.Enabled() || s == "" {
		return s
	}
	s = userinfoRe.ReplaceAllString(s, "${1}"+Mask+"@")
	s = jwtRe.ReplaceAllString(s, Mask)
	s = bearerRe.ReplaceAllStringFunc(s, func(m string) string {
		return strings.Fields(m)[0] + " " + Mask
	})
	s = p.maskContainers(s)
	s = jsonKVRe.ReplaceAllStringFunc(s, func(m string) string {
		g := jsonKVRe.FindStringSubmatch(m)
		if !p.SensitiveKey(jsonKey(g[1])) || g[3] == `"`+Mask+`"` || g[3] == `""` {
			return m
		}
		return `"` + g[1] + `"` + g[2] + `"` + Mask + `"`
	})
	if m := openTail(s); m != nil && p.SensitiveKey(jsonKey(s[m[2]:m[3]])) {
		s = s[:m[0]] + `"` + s[m[2]:m[3]] + `"` + s[m[4]:m[5]] + `"` + Mask + `"`
	}
	s = formKVRe.ReplaceAllStringFunc(s, func(m string) string {
		g := formKVRe.FindStringSubmatch(m)
		if !p.SensitiveKey(formKey(g[1])) || g[2] == "" || g[2] == Mask {
			return m
		}
		return g[1] + "=" + Mask
	})
	s = colonKVRe.ReplaceAllStringFunc(s, func(m string) string {
		g := colonKVRe.FindStringSubmatch(m)
		if !p.SensitiveHeader(strings.TrimSpace(g[1])) || strings.HasPrefix(g[3], Mask) {
			return m
		}
		return g[1] + g[2] + Mask
	})
	for _, re := range p.patterns {
		s = re.ReplaceAllString(s, Mask)
	}
	return s
}

// Headers masks a header map in place (credentials by name, the rest as text) and returns it.
func (p *Policy) Headers(h map[string]string) map[string]string {
	if !p.Enabled() {
		return h
	}
	for k, v := range h {
		if p.SensitiveHeader(k) {
			if v != "" {
				h[k] = Mask
			}
			continue
		}
		h[k] = p.Text(v)
	}
	return h
}

func openTail(s string) []int {
	q := lastUnescapedQuote(s, len(s)-1) // abre el valor cortado
	if q < 0 {
		return nil
	}
	// hacia atrás: espacios, ":", espacios, la comilla que cierra la clave y la que la abre
	j := q - 1
	for j >= 0 && isJSONSpace(s[j]) {
		j--
	}
	if j < 0 || s[j] != ':' {
		return nil
	}
	j--
	for j >= 0 && isJSONSpace(s[j]) {
		j--
	}
	if j < 0 || s[j] != '"' {
		return nil
	}
	keyEnd := j
	if !escaped(s, keyEnd) {
		// la comilla anterior sin escapar abre la clave: ninguna anterior puede (pasaría por esta)
		if keyStart := lastUnescapedQuote(s, keyEnd-1); keyStart >= 0 {
			if keyStart+1 == keyEnd {
				return nil // clave vacía
			}
			return []int{keyStart, len(s), keyStart + 1, keyEnd, keyEnd + 1, q}
		}
	}
	// fragmento raro (la clave empieza o termina en una comilla precedida por barras): la
	// expresión de referencia decide, sobre el texto hasta el valor
	m := jsonOpenKeyRe.FindStringSubmatchIndex(s[:q+1])
	if m == nil {
		return nil
	}
	m[1] = len(s)
	return m
}

// lastUnescapedQuote is the last '"' at or before i that is not escaped by a backslash (-1).
func lastUnescapedQuote(s string, i int) int {
	for ; i >= 0; i-- {
		if s[i] == '"' && !escaped(s, i) {
			return i
		}
	}
	return -1
}

// escaped reports whether s[i] is preceded by an odd number of backslashes.
func escaped(s string, i int) bool {
	bs := 0
	for j := i - 1; j >= 0 && s[j] == '\\'; j-- {
		bs++
	}
	return bs%2 == 1
}

// isJSONSpace is \s of Go's regexp (the separator of the reference expression).
func isJSONSpace(c byte) bool { return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' }
