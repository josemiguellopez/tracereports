// Package lab is FixLab's Request Lab: it resends a captured call to see whether a failure still
// happens. The evidence was masked before it was stored, so a call cannot be resent as is: Prepare
// turns every masked value into a {{variable}} and works out which ones it can get by itself (a
// fresh session from the recorded login, a CSRF token read live from the page) and which ones a
// person must type (passwords, API keys). Run executes the plan and compares the new response with
// the original one. See docs/rfc/fixlab.md.
package lab

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Variable kinds.
const (
	KindSecret  = "secret"  // la escribe la persona (contraseña, API key)
	KindSession = "session" // la resuelve el paso de sesión (cookie, Bearer)
	KindCSRF    = "csrf"    // se lee en vivo de la página que origina la petición
)

// Request is an HTTP request; masked values appear as {{name}} placeholders.
type Request struct {
	Method  string            `json:"method"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`
}

// Var is a value that was masked in the evidence and must be filled before sending.
type Var struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Where lists the places it fills: "header:cookie", "body:password", "query:token"; the
	// session step's ones are prefixed with "login ".
	Where []string `json:"where"`
	// Optional: it may be left empty (the header or field is then sent without it).
	Optional bool `json:"optional,omitempty"`
}

// Login is the recorded call that opens a session: repeating it gives fresh cookies or a token.
type Login struct {
	ConnID  int64   `json:"conn_id"`
	Request Request `json:"request"`
	// TokenPath is where its JSON response carries the token used as "Bearer <token>" (empty: the
	// session lives in the cookies).
	TokenPath []string `json:"token_path,omitempty"`
}

// CSRF is the page read live to get fresh anti-CSRF values before sending.
type CSRF struct {
	PageURL string   `json:"page_url"`
	Fields  []string `json:"fields"`
}

// Original is what the captured call got.
type Original struct {
	Status int    `json:"status"`
	Failed bool   `json:"failed"`
	Error  string `json:"error,omitempty"`
	Body   string `json:"body,omitempty"`
	// Simulated: the response lacks the date and server headers every real HTTP server sends, so it
	// was probably made up by the test itself (route.fulfill, a mock). Only a hint.
	Simulated bool `json:"simulated,omitempty"`
}

// Plan is everything needed to resend one captured call.
type Plan struct {
	ConnID   int64    `json:"conn_id"`
	Target   Request  `json:"target"`
	Login    *Login   `json:"login,omitempty"`
	CSRF     *CSRF    `json:"csrf,omitempty"`
	Vars     []Var    `json:"vars"`
	Original Original `json:"original"`
	// Hosts are the hosts the test talked to (host:port): on a shared server the Lab may only
	// send to these (plus TRACEREPORTS_LAB_HOSTS).
	Hosts []string `json:"hosts"`
}

var (
	// valores enmascarados: por el servidor (redact.Mask), por los clientes, o ya codificados en URL
	maskRe = regexp.MustCompile(`(?i)<masked>|%3Cmasked%3E|\*\*\*`)
	// placeholders de variables en una petición
	varRe = regexp.MustCompile(`\{\{([A-Za-z0-9_.\-\[\]]+)\}\}`)
)

func isMasked(s string) bool { return maskRe.MatchString(s) }

func normKey(k string) string {
	return strings.NewReplacer("-", "", "_", "", ".", "", "[", "", "]", "").Replace(strings.ToLower(strings.TrimSpace(k)))
}

// csrfKey reports whether a body/query field or header carries an anti-CSRF token.
func csrfKey(k string) bool {
	n := normKey(k)
	return k == "_token" || strings.Contains(n, "csrf") || strings.Contains(n, "xsrf") || n == "authenticitytoken"
}

// passwordKey reports whether a field is a password (a call that sends one is a login).
func passwordKey(k string) bool {
	n := normKey(k)
	for _, p := range []string{"password", "passwd", "pwd", "pass", "clave", "contrasena", "contraseña"} {
		if n == p || strings.HasSuffix(n, p) {
			return true
		}
	}
	return false
}

// tokenKey reports whether a JSON response field holds a session token.
func tokenKey(k string) bool {
	switch normKey(k) {
	case "accesstoken", "token", "idtoken", "jwt", "authtoken", "sessiontoken", "bearer":
		return true
	}
	return false
}

func lowerHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = v
	}
	return out
}

// templater turns the masked values of recorded calls into {{name}} placeholders.
type templater struct {
	prefix string              // "login " marca los lugares del paso de sesión
	found  map[string][]string // nombre -> dónde aparece
	// stale are anti-CSRF values recorded without masking: they belonged to the recorded session,
	// so they are read live too; Prepare puts them back when there is no page to read them from.
	stale map[string]string
}

func newTemplater() *templater {
	return &templater{found: map[string][]string{}, stale: map[string]string{}}
}

// pick reports whether a value becomes a variable: masked, or an anti-CSRF token (always stale).
func pick(name, value string) bool { return isMasked(value) || (csrfKey(name) && value != "") }

func (t *templater) note(name, where, value string) string {
	if !isMasked(value) {
		t.stale[name] = value
	}
	t.found[name] = append(t.found[name], t.prefix+where)
	return "{{" + name + "}}"
}

// call templates one recorded call.
func (t *templater) call(c *db.NetConn) Request {
	r := Request{Method: c.Method, URL: c.URL, Headers: map[string]string{}, Body: c.PostData}
	if r.Method == "" {
		r.Method = "GET"
	}
	for k, v := range lowerHeaders(c.RequestHeaders) {
		if strings.HasPrefix(k, ":") {
			continue // pseudo-headers de HTTP/2
		}
		if pick(k, v) {
			v = t.note(k, "header:"+k, v)
		}
		r.Headers[k] = v
	}
	if u, err := url.Parse(c.URL); err == nil && u.RawQuery != "" {
		if q := t.form(u.RawQuery, "query:"); q != u.RawQuery {
			r.URL = strings.Replace(c.URL, u.RawQuery, q, 1)
		}
	}
	if r.Body != "" {
		ctype := strings.ToLower(r.Headers["content-type"])
		switch {
		case strings.Contains(ctype, "json") || json.Valid([]byte(r.Body)):
			r.Body = t.json(r.Body)
		case strings.Contains(ctype, "x-www-form-urlencoded") || strings.Contains(r.Body, "="):
			r.Body = t.form(r.Body, "body:")
		case isMasked(r.Body):
			r.Body = maskRe.ReplaceAllString(r.Body, t.note("value", "body", "<masked>"))
		}
	}
	return r
}

// form templates key=value pairs (query string or form body) keeping their order.
func (t *templater) form(s, where string) string {
	parts := strings.Split(s, "&")
	for i, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		name := k
		if dk, err := url.QueryUnescape(k); err == nil {
			name = dk
		}
		val := v
		if dv, err := url.QueryUnescape(v); err == nil {
			val = dv
		}
		if pick(name, val) {
			parts[i] = k + "=" + t.note(name, where+name, v)
		}
	}
	return strings.Join(parts, "&")
}

// jsonStringRe is a JSON "key": "value" pair (the value may be a masked string).
var jsonStringRe = regexp.MustCompile(`"((?:[^"\\]|\\.)+)"(\s*:\s*)"((?:[^"\\]|\\.)*)"`)

// json replaces masked JSON string values by placeholders, without re-encoding the body (keys
// keep their order and the rest stays byte for byte).
func (t *templater) json(s string) string {
	return jsonStringRe.ReplaceAllStringFunc(s, func(m string) string {
		g := jsonStringRe.FindStringSubmatch(m)
		if !pick(g[1], g[3]) {
			return m
		}
		return `"` + g[1] + `"` + g[2] + `"` + t.note(g[1], "body:"+g[1], g[3]) + `"`
	})
}

// restore puts a stale value back where its placeholder is.
func (r *Request) restore(name, value string) {
	ph := "{{" + name + "}}"
	r.URL = strings.ReplaceAll(r.URL, ph, value)
	r.Body = strings.ReplaceAll(r.Body, ph, value)
	for k, v := range r.Headers {
		r.Headers[k] = strings.ReplaceAll(v, ph, value)
	}
}

// tokenPath finds the masked session token in a recorded JSON response (up to 3 levels deep).
func tokenPath(body string) []string {
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		return nil
	}
	var walk func(v any, path []string) []string
	walk = func(v any, path []string) []string {
		m, ok := v.(map[string]any)
		if !ok || len(path) > 3 {
			return nil
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if s, ok := m[k].(string); ok && tokenKey(k) && isMasked(s) {
				return append(append([]string{}, path...), k)
			}
		}
		for _, k := range keys {
			if p := walk(m[k], append(path, k)); p != nil {
				return p
			}
		}
		return nil
	}
	return walk(v, nil)
}

// sendsPassword reports whether a call sends a masked password (a login form or JSON login).
func sendsPassword(c *db.NetConn) bool {
	t := newTemplater()
	t.call(c)
	for name := range t.found {
		if passwordKey(name) {
			return true
		}
	}
	return false
}

// Prepare builds the plan to resend conns[i] where conns[i].ID == connID; conns is the test's
// network in capture order.
func Prepare(conns []db.NetConn, connID int64) (*Plan, error) {
	idx := -1
	for i := range conns {
		if conns[i].ID == connID {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil, fmt.Errorf("call %d is not in this test", connID)
	}
	target := &conns[idx]
	tpl := newTemplater()
	found := tpl.found
	p := &Plan{ConnID: connID, Target: tpl.call(target), Original: original(target)}
	p.Hosts = hostsOf(conns)

	needsAuth := found["authorization"] != nil
	needsCookie := found["cookie"] != nil
	isLogin := sendsPassword(target)

	// el paso de sesión: la última llamada anterior que abre una sesión (un login que devolvió un
	// token, si hace falta Bearer; si no, cualquier login, cuyas cookies quedan en el jar)
	if !isLogin && (needsAuth || needsCookie) {
		for i := idx - 1; i >= 0; i-- {
			c := &conns[i]
			if c.Status >= 400 || c.Failed {
				continue
			}
			tp := tokenPath(c.ResponseBody)
			if (needsAuth && tp != nil) || (!needsAuth && (tp != nil || sendsPassword(c))) {
				tpl.prefix = "login "
				p.Login = &Login{ConnID: c.ID, Request: tpl.call(c), TokenPath: tp}
				break
			}
		}
	}

	// CSRF: los campos anti-CSRF se leen de la página que origina el login (o la llamada)
	var csrfFields []string
	for name := range found {
		if csrfKey(name) {
			csrfFields = append(csrfFields, name)
		}
	}
	sort.Strings(csrfFields)
	origin := target
	if p.Login != nil {
		for i := range conns {
			if conns[i].ID == p.Login.ConnID {
				origin = &conns[i]
			}
		}
	}
	page := pageOf(conns, origin)
	if page != "" && (len(csrfFields) > 0 || (isLogin && needsCookie)) {
		p.CSRF = &CSRF{PageURL: page, Fields: csrfFields}
	}
	if p.CSRF == nil {
		// sin página de dónde leerlos, los tokens grabados a la vista vuelven a su lugar
		for name, value := range tpl.stale {
			p.Target.restore(name, value)
			if p.Login != nil {
				p.Login.Request.restore(name, value)
			}
			delete(found, name)
		}
	}

	names := make([]string, 0, len(found))
	for name := range found {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		v := Var{Name: name, Kind: KindSecret, Where: found[name]}
		switch {
		case name == "cookie" && (p.Login != nil || p.CSRF != nil):
			v.Kind = KindSession
		case name == "cookie":
			v.Optional = true
		case name == "authorization" && p.Login != nil && p.Login.TokenPath != nil:
			v.Kind = KindSession
		case csrfKey(name) && p.CSRF != nil:
			v.Kind = KindCSRF
		}
		p.Vars = append(p.Vars, v)
	}
	return p, nil
}

// pageOf is the page a call was made from: its Referer, or the last page (document) loaded before
// it on the same host. "" when unknown.
func pageOf(conns []db.NetConn, c *db.NetConn) string {
	if ref := lowerHeaders(c.RequestHeaders)["referer"]; ref != "" && !isMasked(ref) {
		return ref
	}
	cu, err := url.Parse(c.URL)
	if err != nil {
		return ""
	}
	for i := len(conns) - 1; i >= 0; i-- {
		d := &conns[i]
		if d.Seq >= c.Seq || d.ResourceType != "document" || d.Method != "GET" || isMasked(d.URL) {
			continue
		}
		if du, err := url.Parse(d.URL); err == nil && du.Host == cu.Host {
			return d.URL
		}
	}
	return ""
}

func original(c *db.NetConn) Original {
	o := Original{Status: c.Status, Failed: c.Failed || c.Status == 0, Error: c.ErrorText}
	o.Body = c.ResponseBody
	if len(o.Body) > 4096 {
		o.Body = o.Body[:4096]
	}
	if c.Status > 0 {
		h := lowerHeaders(c.ResponseHeaders)
		_, date := h["date"]
		_, server := h["server"]
		o.Simulated = !date && !server
	}
	return o
}

func hostsOf(conns []db.NetConn) []string {
	seen := map[string]bool{}
	var out []string
	for i := range conns {
		u, err := url.Parse(conns[i].URL)
		if err != nil || u.Host == "" || seen[u.Host] {
			continue
		}
		seen[u.Host] = true
		out = append(out, u.Host)
	}
	sort.Strings(out)
	return out
}
