package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"syscall"
	"time"
)

// Verdicts of a resend.
const (
	VerdictReproduced = "reproduced" // falla igual que en la ejecución
	VerdictFixed      = "fixed"      // el original falló y ahora responde bien
	VerdictSession    = "session"    // la sesión no alcanzó: 401/403 o redirección al login
	VerdictDifferent  = "different"  // otro error
	VerdictNetwork    = "network"    // no se pudo conectar
	VerdictOK         = "ok"         // el original no había fallado y ahora tampoco
)

const (
	stepTimeout  = 15 * time.Second
	maxRespBytes = 256 << 10
)

// Options of a resend.
type Options struct {
	// Values typed by the person, by variable name. A value given for a session or CSRF variable
	// overrides the automatic one.
	Values map[string]string
	// BaseURL ("http://localhost:3000") replaces scheme and host of every call that went to the
	// target's original host: the same flow against another environment.
	BaseURL string
	// Target replaces the captured request (edited in the Lab); its {{variables}} are filled the same.
	Target *Request
	// Allow decides whether a URL may be called (nil allows any http/https URL).
	Allow func(*url.URL) error
	// Transport is the HTTP transport (tests); nil uses a safe default (see dialer).
	Transport http.RoundTripper
}

// Step is one request the Lab sent.
type Step struct {
	Role       string `json:"role"` // csrf | login | target
	Method     string `json:"method"`
	URL        string `json:"url"`
	Status     int    `json:"status"`
	Location   string `json:"location,omitempty"`
	DurationMs int64  `json:"duration_ms"`
	Error      string `json:"error,omitempty"`
	Note       string `json:"note,omitempty"`
}

// Response is what the target call got now.
type Response struct {
	Status     int               `json:"status"`
	Headers    map[string]string `json:"headers"`
	Body       string            `json:"body"`
	Truncated  bool              `json:"truncated,omitempty"`
	DurationMs int64             `json:"duration_ms"`
	Error      string            `json:"error,omitempty"`
}

// Result of a resend.
type Result struct {
	Steps    []Step   `json:"steps"`
	Response Response `json:"response"`
	Verdict  string   `json:"verdict"`
	// Sent is the target request as sent, with the {{variables}} still in place (never the values).
	Sent Request `json:"sent"`
}

// ErrMissing is returned when a required variable has no value.
type ErrMissing struct{ Names []string }

func (e *ErrMissing) Error() string { return "missing values: " + strings.Join(e.Names, ", ") }

// ErrBlocked is returned when a URL is not allowed.
var ErrBlocked = errors.New("blocked")

type runner struct {
	client  *http.Client
	plan    *Plan
	opt     Options
	from    *url.URL // host original de la llamada (lo que BaseURL reemplaza)
	base    *url.URL
	vals    map[string]string
	token   string
	result  *Result
	context context.Context
}

// Run resends the plan's target call (after the CSRF page and the login, when needed).
func Run(ctx context.Context, p *Plan, opt Options) (*Result, error) {
	r := &runner{plan: p, opt: opt, vals: map[string]string{}, result: &Result{}, context: ctx}
	target := p.Target
	if opt.Target != nil {
		target = *opt.Target
		if target.Headers == nil {
			target.Headers = map[string]string{}
		}
		target.Method = strings.ToUpper(strings.TrimSpace(target.Method))
		if target.Method == "" {
			target.Method = "GET"
		}
	}
	r.from, _ = url.Parse(p.Target.URL)
	if opt.BaseURL != "" {
		b, err := url.Parse(strings.TrimSpace(opt.BaseURL))
		if err != nil || (b.Scheme != "http" && b.Scheme != "https") || b.Host == "" {
			return nil, fmt.Errorf("invalid base URL %q (expected http://host[:port])", opt.BaseURL)
		}
		r.base = b
	}
	for k, v := range opt.Values {
		if v != "" {
			r.vals[k] = v
		}
	}
	if missing := r.missing(target); len(missing) > 0 {
		return nil, &ErrMissing{Names: missing}
	}
	// cada URL se valida antes de enviar nada
	for _, raw := range r.urls(target) {
		if err := r.allowed(raw); err != nil {
			return nil, err
		}
	}

	jar, _ := cookiejar.New(nil)
	tr := opt.Transport
	if tr == nil {
		tr = safeTransport()
	}
	r.client = &http.Client{Jar: jar, Transport: tr, Timeout: stepTimeout,
		// las redirecciones se muestran, no se siguen (y no salen del host permitido)
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	if p.CSRF != nil {
		r.csrf()
	}
	if p.Login != nil {
		r.login()
	}
	sent := target
	resp, step := r.send("target", target)
	r.result.Steps = append(r.result.Steps, step)
	r.result.Response = resp
	sent.URL = r.rebase(sent.URL)
	r.result.Sent = sent
	r.result.Verdict = verdict(p.Original, resp)
	return r.result, nil
}

// missing lists the secret variables the requests need and nobody filled.
func (r *runner) missing(target Request) []string {
	need := map[string]bool{}
	reqs := []Request{target}
	if r.plan.Login != nil {
		reqs = append(reqs, r.plan.Login.Request)
	}
	for _, q := range reqs {
		for _, m := range varRe.FindAllStringSubmatch(q.URL+"\n"+q.Body+"\n"+headerText(q.Headers), -1) {
			need[m[1]] = true
		}
	}
	var out []string
	for _, v := range r.plan.Vars {
		if need[v.Name] && v.Kind == KindSecret && !v.Optional && r.vals[v.Name] == "" {
			out = append(out, v.Name)
		}
	}
	return out
}

func headerText(h map[string]string) string {
	var b strings.Builder
	for k, v := range h {
		b.WriteString(k + ": " + v + "\n")
	}
	return b.String()
}

func (r *runner) urls(target Request) []string {
	out := []string{r.rebase(target.URL)}
	if r.plan.Login != nil {
		out = append(out, r.rebase(r.plan.Login.Request.URL))
	}
	if r.plan.CSRF != nil {
		out = append(out, r.rebase(r.plan.CSRF.PageURL))
	}
	return out
}

func (r *runner) allowed(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: %q is not an http(s) URL", ErrBlocked, raw)
	}
	if r.opt.Allow != nil {
		if err := r.opt.Allow(u); err != nil {
			return fmt.Errorf("%w: %v", ErrBlocked, err)
		}
	}
	return nil
}

// rebase moves a URL of the original host to BaseURL.
func (r *runner) rebase(raw string) string {
	if r.base == nil || r.from == nil {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || !strings.EqualFold(u.Host, r.from.Host) {
		return raw
	}
	u.Scheme, u.Host = r.base.Scheme, r.base.Host
	if p := strings.TrimSuffix(r.base.Path, "/"); p != "" {
		u.Path = p + u.Path
	}
	return u.String()
}

// csrf loads the page the flow starts from: it leaves its cookies in the jar and reads the fresh
// anti-CSRF values.
func (r *runner) csrf() {
	c := r.plan.CSRF
	resp, step := r.send("csrf", Request{Method: "GET", URL: c.PageURL, Headers: map[string]string{}})
	var found []string
	for _, f := range c.Fields {
		if r.vals[f] != "" {
			continue // la persona lo dio a mano
		}
		if v := csrfValue(resp.Body, f); v != "" {
			r.vals[f] = v
			found = append(found, f)
		}
	}
	if len(found) > 0 {
		step.Note = "csrf: " + strings.Join(found, ", ")
	} else if len(c.Fields) > 0 && step.Error == "" {
		step.Note = "csrf not found in the page"
	}
	r.result.Steps = append(r.result.Steps, step)
}

// login repeats the recorded login: its cookies stay in the jar and, with TokenPath, the token of
// its JSON response becomes the Bearer of the next calls.
func (r *runner) login() {
	l := r.plan.Login
	resp, step := r.send("login", l.Request)
	if l.TokenPath != nil && r.vals["authorization"] == "" {
		if tok := jsonPath(resp.Body, l.TokenPath); tok != "" {
			r.token = tok
			step.Note = "token: " + strings.Join(l.TokenPath, ".")
		} else if step.Error == "" {
			step.Note = "token not found in the response"
		}
	}
	r.result.Steps = append(r.result.Steps, step)
}

// fill replaces the {{variables}} of one part; esc encodes each value for where it goes.
func (r *runner) fill(s string, esc func(string) string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := varRe.FindStringSubmatch(m)[1]
		if v, ok := r.vals[name]; ok {
			return esc(v)
		}
		return ""
	})
}

func (r *runner) send(role string, q Request) (Response, Step) {
	raw := r.rebase(q.URL)
	step := Step{Role: role, Method: q.Method, URL: raw}
	if err := r.allowed(raw); err != nil {
		step.Error = err.Error()
		return Response{Error: step.Error}, step
	}
	target := r.fill(raw, url.QueryEscape)
	body := q.Body
	ctype := strings.ToLower(q.Headers["content-type"])
	switch {
	case strings.Contains(ctype, "json") || json.Valid([]byte(strings.NewReplacer("{{", "", "}}", "").Replace(body))):
		body = r.fill(body, jsonEscape)
	case strings.Contains(ctype, "x-www-form-urlencoded") || strings.Contains(body, "="):
		// la misma regla que el plan (templater.call): un formulario sin Content-Type también
		body = r.fill(body, url.QueryEscape)
	default:
		body = r.fill(body, func(v string) string { return v })
	}
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	ctx, cancel := context.WithTimeout(r.context, stepTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, q.Method, target, rd)
	if err != nil {
		step.Error = err.Error()
		return Response{Error: step.Error}, step
	}
	for k, v := range q.Headers {
		lk := strings.ToLower(k)
		if skipHeader[lk] || strings.HasPrefix(lk, ":") {
			continue
		}
		switch {
		case lk == "cookie" && isPlaceholderOnly(v, "cookie") && r.vals["cookie"] == "":
			continue // la sesión va en el jar
		case lk == "authorization" && isPlaceholderOnly(v, "authorization") && r.vals["authorization"] == "":
			if r.token == "" {
				continue
			}
			v = "Bearer " + r.token
		default:
			v = r.fill(v, func(v string) string { return v })
		}
		if v == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "TraceReports-FixLab")
	}
	start := time.Now()
	res, err := r.client.Do(req)
	step.DurationMs = time.Since(start).Milliseconds()
	if err != nil {
		step.Error = cleanErr(err)
		return Response{Error: step.Error, DurationMs: step.DurationMs}, step
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, maxRespBytes+1))
	out := Response{Status: res.StatusCode, Headers: map[string]string{}, DurationMs: step.DurationMs}
	if len(data) > maxRespBytes {
		data, out.Truncated = data[:maxRespBytes], true
	}
	out.Body = string(data)
	for k := range res.Header {
		out.Headers[strings.ToLower(k)] = strings.Join(res.Header.Values(k), ", ")
	}
	step.Status = res.StatusCode
	step.Location = res.Header.Get("Location")
	return out, step
}

// cabeceras que arma el cliente HTTP o que no se reenvían
var skipHeader = map[string]bool{"host": true, "content-length": true, "connection": true, "accept-encoding": true,
	"transfer-encoding": true, "keep-alive": true, "upgrade": true, "te": true, "trailer": true, "proxy-authorization": true}

func isPlaceholderOnly(v, name string) bool { return strings.TrimSpace(v) == "{{"+name+"}}" }

// jsonEscape encodes v for inside a JSON string, as is (no & for &, < or >).
func jsonEscape(v string) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	s := strings.TrimSuffix(b.String(), "\n")
	return s[1 : len(s)-1]
}

func cleanErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		err = ue.Err
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "timeout"
	}
	return err.Error()
}

// jsonPath reads a string at path in a JSON document ("" if absent).
func jsonPath(body string, path []string) string {
	var v any
	if json.Unmarshal([]byte(body), &v) != nil {
		return ""
	}
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return ""
		}
		v = m[k]
	}
	s, _ := v.(string)
	return s
}

// csrfValue finds an anti-CSRF value in a page: a hidden input, a <meta name="csrf-token">, or the
// field passed as a component property (Vue/React: token="…", :token="&quot;…&quot;").
func csrfValue(page, field string) string {
	f := regexp.QuoteMeta(field)
	pats := []string{
		`<input[^>]*name=["']` + f + `["'][^>]*value=["']([^"']+)["']`,
		`<input[^>]*value=["']([^"']+)["'][^>]*name=["']` + f + `["']`,
		`<meta[^>]*name=["'](?:csrf-token|_token|` + f + `)["'][^>]*content=["']([^"']+)["']`,
		`[:@]?` + regexp.QuoteMeta(strings.TrimPrefix(field, "_")) + `\s*=\s*["'](?:&quot;)?([A-Za-z0-9._~+/=-]{16,})(?:&quot;)?["']`,
	}
	for _, p := range pats {
		if m := regexp.MustCompile(`(?i)` + p).FindStringSubmatch(page); m != nil {
			return html.UnescapeString(m[1])
		}
	}
	return ""
}

var loginURLRe = regexp.MustCompile(`(?i)log-?in|sign-?in|/auth\b|/sso\b|session`)

func verdict(o Original, now Response) string {
	origFailed := o.Failed || o.Status >= 400
	switch {
	case now.Status == 0:
		if o.Failed {
			return VerdictReproduced
		}
		return VerdictNetwork
	case (now.Status == 401 || now.Status == 403) && o.Status != now.Status:
		return VerdictSession
	case now.Status >= 300 && now.Status < 400 && loginURLRe.MatchString(now.Headers["location"]) && o.Status != now.Status:
		return VerdictSession
	case !o.Failed && now.Status == o.Status:
		if origFailed {
			return VerdictReproduced
		}
		return VerdictOK
	case origFailed && now.Status < 400:
		return VerdictFixed
	case !origFailed && now.Status < 400:
		return VerdictOK
	}
	return VerdictDifferent
}

// safeTransport is the default transport: it never dials link-local addresses (cloud metadata
// at 169.254.169.254, fe80::/10) nor unspecified ones, whatever name resolved to them.
func safeTransport() *http.Transport {
	d := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified()) {
			return fmt.Errorf("%w: link-local address %s", ErrBlocked, ip)
		}
		return nil
	}}
	return &http.Transport{DialContext: d.DialContext, Proxy: nil, TLSHandshakeTimeout: 10 * time.Second,
		ResponseHeaderTimeout: stepTimeout, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second}
}
