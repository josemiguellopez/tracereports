package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"golang.org/x/term"

	"github.com/josemiguellopez/tracereports/internal/lab"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

// FixLab en la terminal: `tracereports repro <cápsula>` muestra cómo correr el test y reenvía
// cada llamada con una sesión nueva; `tracereports verify <cápsula>` corre los checks guardados y
// sale con código 1 si alguno no pasa (sirve en CI). La cápsula es un archivo o la URL que copia la
// interfaz. Los secretos se piden aparte: -var nombre=valor, TRACEREPORTS_LAB_<NOMBRE> o, en una
// terminal, sin mostrarlos.

// varList collects repeated -var name=value flags.
type varList map[string]string

func (v varList) String() string { return "" }

func (v varList) Set(s string) error {
	name, value, ok := strings.Cut(s, "=")
	if !ok || strings.TrimSpace(name) == "" {
		return fmt.Errorf("expected -var name=value, got %q", s)
	}
	v[strings.TrimSpace(name)] = value
	return nil
}

type labCLI struct {
	vars    varList
	base    string
	in      *os.File // de donde se piden los secretos (nil = no se piden)
	out     io.Writer
	errOut  io.Writer
	client  *http.Client
	getenv  func(string) string
	isTerm  func() bool
	readPwd func() ([]byte, error)
}

func newLabCLI(out io.Writer) *labCLI {
	return &labCLI{vars: varList{}, in: os.Stdin, out: out, errOut: os.Stderr, client: &http.Client{Timeout: 30 * time.Second},
		getenv:  os.Getenv,
		isTerm:  func() bool { return term.IsTerminal(int(os.Stdin.Fd())) },
		readPwd: func() ([]byte, error) { return term.ReadPassword(int(os.Stdin.Fd())) }}
}

func (c *labCLI) flags(name, usage string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.Var(c.vars, "var", "value of a {{variable}}, as name=value (repeatable; or TRACEREPORTS_LAB_<NAME>)")
	fs.StringVar(&c.base, "base", "", "send to another environment, e.g. http://localhost:3000 (default: the run's host)")
	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), "Usage: tracereports %s [flags] <capsule.json | capsule URL>\n%s\n", name, usage)
		fs.PrintDefaults()
	}
	return fs
}

// parseAll parses flags anywhere among the arguments (`repro <url> -var a=b` too: the command is
// pasted from the report and the values are added after it) and returns the positional ones.
func parseAll(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		if fs.NArg() == 0 {
			return pos, nil
		}
		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}

// load reads a capsule from a file or the URL the report copies.
func (c *labCLI) load(src string) (*lab.Capsule, error) {
	var data []byte
	var err error
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		req, rerr := http.NewRequest(http.MethodGet, src, nil)
		if rerr != nil {
			return nil, rerr
		}
		req.Header.Set("Accept", "application/json")
		// las credenciales van solo al servidor de TRACEREPORTS_URL: una URL de cápsula ajena
		// (pegada de otro lado) nunca recibe el token de CI ni el login
		trusted := c.trustedHost(req.URL)
		if trusted {
			readCredentials(req, c.getenv)
		}
		res, rerr := c.client.Do(req)
		if rerr != nil {
			return nil, rerr
		}
		defer res.Body.Close()
		data, err = io.ReadAll(io.LimitReader(res.Body, 32<<20))
		if err == nil && res.StatusCode != http.StatusOK {
			hint := ""
			if (res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden) && !trusted {
				hint = fmt.Sprintf(" (credentials are only sent to the server of TRACEREPORTS_URL: set TRACEREPORTS_URL=%s://%s to use them here)", req.URL.Scheme, req.URL.Host)
			}
			return nil, fmt.Errorf("GET %s: %s %s%s", src, res.Status, strings.TrimSpace(string(data)), hint)
		}
	} else {
		data, err = os.ReadFile(src)
	}
	if err != nil {
		return nil, err
	}
	return lab.ReadCapsule(data)
}

// trustedHost reports whether u is the server of TRACEREPORTS_URL (same scheme, host and port).
func (c *labCLI) trustedHost(u *url.URL) bool {
	t, err := url.Parse(strings.TrimSpace(c.getenv("TRACEREPORTS_URL")))
	if err != nil || t.Host == "" {
		return false
	}
	return strings.EqualFold(t.Scheme, u.Scheme) && strings.EqualFold(t.Host, u.Host)
}

// destinations says where each secret will be sent, before it is asked for or used: a capsule
// from someone else could point its login to another server.
func (c *labCLI) destinations(cap *lab.Capsule, needs []string, checks bool) {
	if len(needs) == 0 {
		return
	}
	need := map[string]bool{}
	for _, n := range needs {
		need[n] = true
	}
	where := map[string][]string{}
	note := func(q lab.Request) {
		text := q.URL + "\n" + q.Body
		for _, v := range q.Headers {
			text += "\n" + v
		}
		for _, n := range needs {
			if strings.Contains(text, "{{"+n+"}}") {
				dest := q.Method + " " + q.URL
				if !slices.Contains(where[n], dest) {
					where[n] = append(where[n], dest)
				}
			}
		}
	}
	for _, call := range cap.Calls {
		if call.Plan.Login != nil {
			note(call.Plan.Login.Request)
		}
		if !checks {
			note(call.Plan.Target)
			continue
		}
		for _, ch := range call.Checks {
			if ch.Request != nil {
				note(*ch.Request)
			} else {
				note(call.Plan.Target)
			}
		}
	}
	fmt.Fprintln(c.errOut, "Secrets will be sent to:")
	for _, n := range needs {
		for _, d := range where[n] {
			fmt.Fprintf(c.errOut, "  %s → %s\n", n, d)
		}
	}
	if c.base != "" {
		fmt.Fprintf(c.errOut, "  (the run's host replaced by %s)\n", c.base)
	}
}

var envNameRe = regexp.MustCompile(`[^A-Za-z0-9]+`)

// labEnv is the environment variable that may carry a {{variable}}: _token -> TRACEREPORTS_LAB_TOKEN.
func labEnv(name string) string {
	n := strings.ToUpper(strings.Trim(envNameRe.ReplaceAllString(name, "_"), "_"))
	if n == "" {
		n = "VALUE"
	}
	return "TRACEREPORTS_LAB_" + n
}

// values fills the secrets the capsule needs: -var, then the environment, then a hidden prompt.
func (c *labCLI) values(needs []string) (map[string]string, error) {
	vals := map[string]string{}
	for k, v := range c.vars {
		vals[k] = v
	}
	var missing []string
	for _, n := range needs {
		if vals[n] != "" {
			continue
		}
		if v := c.getenv(labEnv(n)); v != "" {
			vals[n] = v
			continue
		}
		if c.in != nil && c.isTerm() {
			fmt.Fprintf(c.errOut, "%s (hidden): ", n)
			b, err := c.readPwd()
			fmt.Fprintln(c.errOut)
			if err != nil {
				return nil, err
			}
			if v := strings.TrimRight(string(b), "\r\n"); v != "" {
				vals[n] = v
				continue
			}
		}
		missing = append(missing, n)
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		var env []string
		for _, n := range missing {
			env = append(env, labEnv(n)+"=…")
		}
		return nil, fmt.Errorf("missing values: %s (pass -var %s=… or set %s)", strings.Join(missing, ", "), missing[0], strings.Join(env, " "))
	}
	return vals, nil
}

func capsuleNeeds(cap *lab.Capsule, checks bool) []string {
	set := map[string]bool{}
	for _, call := range cap.Calls {
		if checks {
			for _, ch := range call.Checks {
				for _, n := range call.Plan.Needs(ch.Request) {
					set[n] = true
				}
			}
			continue
		}
		for _, n := range call.Plan.Needs(nil) {
			set[n] = true
		}
	}
	out := make([]string, 0, len(set))
	for n := range set {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (c *labCLI) header(cap *lab.Capsule) {
	r := cap.Run
	ctx := []string{}
	for _, s := range []string{r.Project, r.Environment, r.Branch} {
		if s != "" {
			ctx = append(ctx, s)
		}
	}
	if r.Commit != "" {
		ctx = append(ctx, "commit "+shortCommit(r.Commit))
	}
	fmt.Fprintf(c.out, "TraceReports capsule · run #%d %q", r.ID, r.Name)
	if len(ctx) > 0 {
		fmt.Fprintf(c.out, " (%s)", strings.Join(ctx, " · "))
	}
	fmt.Fprintln(c.out)
	if cap.Incident != "" {
		fmt.Fprintf(c.out, "Incident: %s\n", cap.Incident)
	}
	if cap.Source != "" {
		fmt.Fprintf(c.out, "Report:   %s\n", cap.Source)
	}
}

func shortCommit(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

var verdictText = map[string]string{
	lab.VerdictReproduced: "REPRODUCED: it fails just like in the run",
	lab.VerdictFixed:      "NO LONGER FAILS: the backend answers fine now",
	lab.VerdictSession:    "SESSION MISSING: the backend asked for a login (check the values)",
	lab.VerdictDifferent:  "DIFFERENT ERROR than in the run",
	lab.VerdictNetwork:    "NO CONNECTION to the backend",
	lab.VerdictOK:         "ANSWERS FINE (the original call had not failed either)",
}

func originalText(o lab.Original) string {
	if o.Failed || o.Status == 0 {
		if o.Error != "" {
			return "no response (" + o.Error + ")"
		}
		return "no response"
	}
	return fmt.Sprint(o.Status)
}

func (c *labCLI) steps(res *lab.Result) {
	for _, st := range res.Steps {
		line := fmt.Sprintf("  %-6s %s %s → ", st.Role, st.Method, st.URL)
		if st.Error != "" {
			line += st.Error
		} else {
			line += fmt.Sprint(st.Status)
		}
		if st.Location != "" {
			line += " → " + st.Location
		}
		if st.Note != "" {
			line += "  [" + st.Note + "]"
		}
		fmt.Fprintln(c.out, line)
	}
}

// excerpt is the start of an answer, masked like the evidence and without the typed values.
func excerpt(body string, vals map[string]string) string {
	body = redact.Default().Text(body)
	for _, v := range vals {
		if len(v) >= 4 {
			body = strings.ReplaceAll(body, v, redact.Mask)
		}
	}
	body = strings.Join(strings.Fields(body), " ")
	if len(body) > 200 {
		body = body[:200] + "…"
	}
	return body
}

// runRepro shows how to run the test and resends each call of the capsule.
func runRepro(args []string) error { return newLabCLI(os.Stdout).repro(args) }

func (c *labCLI) repro(args []string) error {
	fs := c.flags("repro", "Shows how to run the failed test locally and resends each call with a fresh session.")
	pos, err := parseAll(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return flag.ErrHelp
	}
	cap, err := c.load(pos[0])
	if err != nil {
		return err
	}
	needs := capsuleNeeds(cap, false)
	c.destinations(cap, needs, false)
	vals, err := c.values(needs)
	if err != nil {
		return err
	}
	c.header(cap)
	tests := map[int64]bool{}
	for _, call := range cap.Calls {
		if !tests[call.Test.ID] {
			tests[call.Test.ID] = true
			fmt.Fprintf(c.out, "\nTest: %s\n", call.Test.Name)
			if call.Test.Error != "" {
				fmt.Fprintf(c.out, "  error: %s\n", call.Test.Error)
			}
			for _, cmd := range call.Test.Commands {
				fmt.Fprintf(c.out, "  run it (%s): %s\n", cmd.Label, cmd.Cmd)
			}
		}
		p := call.Plan
		fmt.Fprintf(c.out, "\nCall: %s %s (in the run: %s)\n", p.Target.Method, p.Target.URL, originalText(p.Original))
		if p.Original.Simulated {
			fmt.Fprintln(c.out, "  note: the original answer has no date nor server headers: the test probably made it up (a mock)")
		}
		res, err := lab.Run(context.Background(), p, lab.Options{Values: vals, BaseURL: c.base, Allow: c.allow(p)})
		if err != nil {
			return err
		}
		c.steps(res)
		fmt.Fprintf(c.out, "  => %s\n", verdictText[res.Verdict])
		if b := excerpt(res.Response.Body, vals); b != "" {
			fmt.Fprintf(c.out, "  answer: %s\n", b)
		}
	}
	return nil
}

// errChecksFailed makes `verify` exit with code 1.
var errChecksFailed = errors.New("checks failed")

// runVerify runs the saved checks of the capsule; it fails when one does not pass.
func runVerify(args []string) error { return newLabCLI(os.Stdout).verify(args) }

func (c *labCLI) verify(args []string) error {
	fs := c.flags("verify", "Runs the saved checks of the capsule and exits with code 1 if one does not pass (usable in CI).")
	pos, err := parseAll(fs, args)
	if err != nil {
		return err
	}
	if len(pos) != 1 {
		fs.Usage()
		return flag.ErrHelp
	}
	cap, err := c.load(pos[0])
	if err != nil {
		return err
	}
	total := 0
	for _, call := range cap.Calls {
		total += len(call.Checks)
	}
	if total == 0 {
		return errors.New("the capsule has no checks: save one from the Lab first (Save as a check)")
	}
	needs := capsuleNeeds(cap, true)
	c.destinations(cap, needs, true)
	vals, err := c.values(needs)
	if err != nil {
		return err
	}
	c.header(cap)
	fmt.Fprintln(c.out)
	failed := 0
	for _, call := range cap.Calls {
		for _, ch := range call.Checks {
			base := c.base
			if base == "" {
				base = ch.BaseURL
			}
			res, err := lab.Run(context.Background(), call.Plan, lab.Options{Values: vals, BaseURL: base, Target: ch.Request, Allow: c.allow(call.Plan)})
			var fails []lab.Failure
			now := "no response"
			if err != nil {
				fails = []lab.Failure{{Kind: "network", Got: err.Error()}}
			} else {
				fails = ch.Expect.Eval(res.Response)
				if res.Response.Status != 0 {
					now = fmt.Sprint(res.Response.Status)
				}
			}
			mark := "✓"
			if len(fails) > 0 {
				mark, failed = "✗", failed+1
			}
			fmt.Fprintf(c.out, "%s %s  (%s → %s)\n", mark, ch.Name, originalText(call.Plan.Original), now)
			for _, f := range fails {
				switch f.Kind {
				case "status":
					fmt.Fprintf(c.out, "    status %s, expected %s\n", f.Got, f.Want)
				case "contains":
					fmt.Fprintf(c.out, "    does not contain %q\n", f.Want)
				default:
					fmt.Fprintf(c.out, "    no response: %s\n", f.Got)
				}
			}
		}
	}
	if failed > 0 {
		fmt.Fprintf(c.out, "\nStill failing: %d of %d checks do not pass.\n", failed, total)
		return fmt.Errorf("%w: %d of %d", errChecksFailed, failed, total)
	}
	fmt.Fprintf(c.out, "\nFixed and verified: all %d checks pass.\n", total)
	return nil
}

// readCredentials signs a read to the TraceReports server with the least powerful credential
// available: the ingest token before the admin one, the read-only login before the admin login.
// Reading a capsule or a plan never needs to administer.
func readCredentials(req *http.Request, getenv func(string) string) {
	tok := getenv("TRACEREPORTS_INGEST_TOKEN")
	if tok == "" {
		tok = getenv("TRACEREPORTS_TOKEN")
	}
	u, p := getenv("TRACEREPORTS_UI_VIEWER_USER"), getenv("TRACEREPORTS_UI_VIEWER_PASSWORD")
	if u == "" || p == "" {
		u, p = getenv("TRACEREPORTS_UI_USER"), getenv("TRACEREPORTS_UI_PASSWORD")
	}
	switch {
	case u != "" && p != "":
		req.SetBasicAuth(u, p)
		if tok != "" {
			req.Header.Set("X-TraceReports-Token", tok) // Authorization ya lleva el login
		}
	case tok != "":
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// allow is the host rule of a resend from this machine: the hosts the test called, the ones of
// TRACEREPORTS_LAB_HOSTS and the base URL this person passed with -base.
func (c *labCLI) allow(p *lab.Plan) func(*url.URL) error {
	extra := lab.ParseHosts(c.getenv("TRACEREPORTS_LAB_HOSTS"))
	if h := lab.HostOf(c.base); h != "" {
		extra = append(extra, h)
	}
	return lab.HostRule(p, extra)
}
