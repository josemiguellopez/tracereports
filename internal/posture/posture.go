// Package posture evaluates the security posture of a TraceReports installation: the checklist of
// the hardening guide turned into checks with a level (ok, warn, fail), what was found and how to
// fix it. `tracereports doctor --security` prints it (exit code 1 when a check fails) and the server
// exposes it at GET /api/v1/security/posture and in Settings.
//
// It works on a Config of facts (whether a credential is set, never its value): building that
// Config from the environment or from a running server is the caller's job.
package posture

import (
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/egress"
)

// Version of the JSON contract of Report (doctor --security --json and the API endpoint).
// Adding checks or fields keeps it; renaming or removing them bumps it.
const Version = 1

// Levels of a check.
const (
	OK   = "ok"
	Warn = "warn"
	Fail = "fail"
)

// Check is one item of the checklist.
type Check struct {
	ID     string `json:"id"`    // estable: el contrato de --json
	Level  string `json:"level"` // ok | warn | fail
	Title  string `json:"title"`
	Detail string `json:"detail"`        // lo que se encontró
	Fix    string `json:"fix,omitempty"` // qué hacer (warn y fail)
}

// Report is the result of every check.
type Report struct {
	Version int     `json:"version"`
	Checks  []Check `json:"checks"`
	OK      int     `json:"ok"`
	Warn    int     `json:"warn"`
	Fail    int     `json:"fail"`
}

// Failed reports whether a critical check failed (doctor exits with code 1).
func (r Report) Failed() bool { return r.Fail > 0 }

// Config are the facts the checks look at. Credentials are booleans: a value never gets here.
type Config struct {
	Exposed  bool // escucha más allá de este equipo (bind en la red, o dentro de Docker)
	Docker   bool // la imagen oficial (TRACEREPORTS_RUNTIME=docker): no ve cómo se publicó el puerto
	Insecure bool // TRACEREPORTS_INSECURE=1

	Token, IngestToken, SameTokens bool
	UILogin                        bool
	AnyHost                        bool // TRACEREPORTS_ALLOWED_HOSTS=*
	AllowedCIDRs                   []string

	AIProvider     string   // builtin, off, ollama, openai...
	AIScope        string   // alcance del host del proveedor (egress.Scope*); "" sin red
	AIAllowlist    []string // TRACEREPORTS_AI_ALLOWED_PROVIDERS
	SettingsLocked bool

	RetentionDays int
	Encrypt       bool   // TRACEREPORTS_ENCRYPT_AT_REST=1
	EncryptError  string // por qué la clave no sirve ("" = bien)

	UID          int         // -1: no aplica (Windows)
	DataDir      string      // DATA_DIR
	DataDirMode  fs.FileMode // 0: no existe todavía o no aplica
	DataDirPerms bool        // los permisos POSIX significan algo (no en Windows)

	Egress       []egress.Destination
	NotifyDetail string // NOTIFY_DETAIL: full | minimal

	Redact      bool
	Screenshots string // all | failures | off
}

// Evaluate runs every check, in a fixed order.
func Evaluate(c Config) Report {
	r := Report{Version: Version}
	for _, check := range []func(Config) Check{
		checkTokens, checkUILogin, checkNetwork, checkAI, checkSettings, checkRetention,
		checkEncryption, checkUser, checkPermissions, checkEgress, checkRedaction, checkScreenshots,
	} {
		ch := check(c)
		switch ch.Level {
		case OK:
			r.OK++
			ch.Fix = ""
		case Warn:
			r.Warn++
		default:
			r.Fail++
		}
		r.Checks = append(r.Checks, ch)
	}
	return r
}

func checkTokens(c Config) Check {
	ch := Check{ID: "auth.tokens", Title: "API tokens"}
	switch {
	case c.SameTokens:
		ch.Level, ch.Detail = Fail, "TRACEREPORTS_INGEST_TOKEN is the same as TRACEREPORTS_TOKEN"
		ch.Fix = "generate a different ingest token: openssl rand -hex 32"
	case !c.Token && !c.IngestToken && c.Exposed:
		ch.Level, ch.Detail = Fail, "listening on the network without any token: anyone who reaches the port writes and reads"
		ch.Fix = "set TRACEREPORTS_TOKEN and TRACEREPORTS_INGEST_TOKEN (openssl rand -hex 32), or listen only on 127.0.0.1"
	case !c.Token && !c.IngestToken:
		ch.Level, ch.Detail = Warn, "no token: fine on a laptop (only this machine), not on a shared server"
		ch.Fix = "set TRACEREPORTS_TOKEN and TRACEREPORTS_INGEST_TOKEN before sharing the server"
	case c.Token && !c.IngestToken:
		ch.Level, ch.Detail = Warn, "only the admin token: test clients and CI use a credential that can change Settings"
		ch.Fix = "set TRACEREPORTS_INGEST_TOKEN for clients and CI, and keep TRACEREPORTS_TOKEN for admins"
	default:
		ch.Level, ch.Detail = OK, "admin and ingest tokens set and different"
	}
	return ch
}

func checkUILogin(c Config) Check {
	ch := Check{ID: "auth.ui_login", Title: "UI login"}
	switch {
	case c.UILogin:
		ch.Level, ch.Detail = OK, "reads require the UI login or a token"
	case c.Exposed && c.AnyHost:
		ch.Level, ch.Detail = Fail, "no UI login and TRACEREPORTS_ALLOWED_HOSTS=*: anyone who reaches the port reads every report"
		ch.Fix = "set TRACEREPORTS_UI_USER and TRACEREPORTS_UI_PASSWORD"
	case c.Exposed:
		ch.Level, ch.Detail = Warn, "no UI login: a token does not protect reads, and the Host check is not access control"
		ch.Fix = "set TRACEREPORTS_UI_USER and TRACEREPORTS_UI_PASSWORD (and HTTPS on the proxy)"
	default:
		ch.Level, ch.Detail = OK, "no UI login, but the server only listens on this machine"
	}
	return ch
}

func checkNetwork(c Config) Check {
	ch := Check{ID: "network.exposure", Title: "Network exposure"}
	switch {
	case c.Insecure:
		ch.Level, ch.Detail = Fail, "TRACEREPORTS_INSECURE=1: the server listens on the network even without credentials"
		ch.Fix = "remove TRACEREPORTS_INSECURE and set the tokens and the UI login"
	case !c.Exposed:
		ch.Level, ch.Detail = OK, "listening only on this machine"
	case len(c.AllowedCIDRs) > 0:
		ch.Level, ch.Detail = OK, "only from "+strings.Join(c.AllowedCIDRs, ", ")
	case c.Docker:
		// dentro del contenedor siempre se escucha en su red: si el puerto se publicó solo en 127.0.0.1
		// (el docker-compose.yml oficial) ya está protegido, pero eso no se ve desde adentro
		ch.Level, ch.Detail = Warn, "inside Docker it listens on the container network; if the port is published only on 127.0.0.1 (the official docker-compose.yml), only this machine reaches it"
		ch.Fix = "to filter inside Docker too, set TRACEREPORTS_ALLOWED_CIDRS to the container network (docker network inspect <project>_default) or the networks that need it"
	default:
		ch.Level, ch.Detail = Warn, "listening on the network for any source address"
		ch.Fix = "limit TRACEREPORTS_ALLOWED_CIDRS to the networks that need it (or a firewall)"
	}
	return ch
}

func checkAI(c Config) Check {
	ch := Check{ID: "ai.provider", Title: "AI provider"}
	p := c.AIProvider
	allowed := false
	for _, a := range c.AIAllowlist {
		allowed = allowed || a == p
	}
	switch {
	case p == "" || p == "builtin" || p == "off":
		ch.Level, ch.Detail = OK, "local rules engine or off: no evidence leaves the server"
	case len(c.AIAllowlist) > 0 && !allowed:
		ch.Level, ch.Detail = Fail, fmt.Sprintf("provider %q is not in TRACEREPORTS_AI_ALLOWED_PROVIDERS", p)
		ch.Fix = "use an allowed provider, or add it to TRACEREPORTS_AI_ALLOWED_PROVIDERS if the policy permits"
	case c.AIScope == egress.ScopeLoopback || c.AIScope == egress.ScopePrivate:
		ch.Level, ch.Detail = OK, fmt.Sprintf("%s inside the network (%s)", p, c.AIScope)
	case allowed:
		ch.Level, ch.Detail = OK, fmt.Sprintf("%s, explicitly allowed in TRACEREPORTS_AI_ALLOWED_PROVIDERS: redacted prompts leave the network", p)
	default:
		ch.Level, ch.Detail = Warn, fmt.Sprintf("%s: redacted prompts leave the network, and no TRACEREPORTS_AI_ALLOWED_PROVIDERS limits it", p)
		ch.Fix = "use AI_PROVIDER=builtin or a local LLM, or set TRACEREPORTS_AI_ALLOWED_PROVIDERS to make the decision explicit"
	}
	return ch
}

func checkSettings(c Config) Check {
	ch := Check{ID: "settings.locked", Title: "Settings locked"}
	switch {
	case c.SettingsLocked:
		ch.Level, ch.Detail = OK, "Settings is read-only: the configuration comes only from the environment"
	case c.Exposed:
		ch.Level, ch.Detail = Warn, "any admin (and every UI user) can change the AI provider, its URL and key from the screen"
		ch.Fix = "set TRACEREPORTS_SETTINGS_LOCKED=1"
	default:
		ch.Level, ch.Detail = OK, "Settings editable, only from this machine"
	}
	return ch
}

func checkRetention(c Config) Check {
	ch := Check{ID: "data.retention", Title: "Retention"}
	if c.RetentionDays > 0 {
		ch.Level, ch.Detail = OK, fmt.Sprintf("runs older than %d days are deleted (secure_delete, VACUUM)", c.RetentionDays)
	} else {
		ch.Level, ch.Detail = Warn, "evidence is kept forever"
		ch.Fix = "set TRACEREPORTS_RETENTION_DAYS to what your data policy allows"
	}
	return ch
}

func checkEncryption(c Config) Check {
	ch := Check{ID: "data.encryption", Title: "Evidence encryption at rest"}
	switch {
	case c.EncryptError != "":
		ch.Level, ch.Detail = Fail, c.EncryptError
		ch.Fix = "give a valid TRACEREPORTS_DB_KEY or TRACEREPORTS_DB_KEY_FILE outside DATA_DIR (the server will not start)"
	case c.Encrypt:
		ch.Level, ch.Detail = OK, "screenshots, traces, videos and DOM encrypted (AES-256-GCM), key outside DATA_DIR"
	default:
		ch.Level, ch.Detail = Warn, "off: the evidence is as safe as the disk of DATA_DIR"
		ch.Fix = "set TRACEREPORTS_ENCRYPT_AT_REST=1 with TRACEREPORTS_DB_KEY_FILE, and keep DATA_DIR on an encrypted volume"
	}
	return ch
}

func checkUser(c Config) Check {
	ch := Check{ID: "process.user", Title: "Process user"}
	switch {
	case c.UID < 0:
		ch.Level, ch.Detail = OK, "not applicable on this system"
	case c.UID == 0:
		ch.Level, ch.Detail = Warn, "running as root"
		ch.Fix = "run as an unprivileged user (the official image already is nonroot)"
	default:
		ch.Level, ch.Detail = OK, fmt.Sprintf("unprivileged user (uid %d)", c.UID)
	}
	return ch
}

func checkPermissions(c Config) Check {
	ch := Check{ID: "data.permissions", Title: "DATA_DIR permissions"}
	switch {
	case !c.DataDirPerms:
		ch.Level, ch.Detail = OK, "not applicable on this system (ACLs)"
	case c.DataDirMode == 0:
		ch.Level, ch.Detail = OK, "created with 0700 when the server starts"
	case c.DataDirMode.Perm()&0o077 != 0:
		ch.Level, ch.Detail = Warn, fmt.Sprintf("%s is %#o: other users of the host can read the evidence", c.DataDir, c.DataDirMode.Perm())
		ch.Fix = "chmod 700 " + c.DataDir
	default:
		ch.Level, ch.Detail = OK, fmt.Sprintf("%#o: only the server user", c.DataDirMode.Perm())
	}
	return ch
}

func checkEgress(c Config) Check {
	ch := Check{ID: "egress.destinations", Title: "Outbound destinations"}
	if len(c.Egress) == 0 {
		ch.Level, ch.Detail = OK, "none: no data leaves the server"
		return ch
	}
	var names []string
	notify := false
	for _, d := range c.Egress {
		names = append(names, fmt.Sprintf("%s %s (%s)", d.Name, d.Host, d.Scope))
		notify = notify || d.Kind == "notify"
	}
	sort.Strings(names)
	ch.Detail = strings.Join(names, "; ")
	if notify && c.NotifyDetail != "minimal" {
		ch.Level, ch.Detail = Warn, ch.Detail+" — chat notifications carry test names and errors"
		ch.Fix = "set NOTIFY_DETAIL=minimal (counts and a link only)"
		return ch
	}
	ch.Level = OK
	return ch
}

func checkRedaction(c Config) Check {
	ch := Check{ID: "evidence.redaction", Title: "Secret redaction"}
	if c.Redact {
		ch.Level, ch.Detail = OK, "secrets in headers, bodies and logs are masked before storing"
	} else {
		ch.Level, ch.Detail = Fail, "TRACEREPORTS_REDACT=off: secrets are stored as received"
		ch.Fix = "remove TRACEREPORTS_REDACT=off"
	}
	return ch
}

func checkScreenshots(c Config) Check {
	ch := Check{ID: "evidence.screenshots", Title: "Screenshot policy"}
	switch c.Screenshots {
	case "failures", "off":
		ch.Level, ch.Detail = OK, "TRACEREPORTS_SCREENSHOTS="+c.Screenshots
	default:
		ch.Level, ch.Detail = Warn, "every screenshot, trace and video is kept"
		ch.Fix = "set TRACEREPORTS_SCREENSHOTS=failures (or off) and mask sensitive fields in the clients"
	}
	return ch
}

// Table renders the report as a text table (doctor --security without --json).
func Table(r Report) string {
	var b strings.Builder
	label := map[string]string{OK: "OK", Warn: "WARN", Fail: "FAIL"}
	for _, ch := range r.Checks {
		fmt.Fprintf(&b, "%-5s %-28s %s\n", label[ch.Level], ch.Title, ch.Detail)
		if ch.Fix != "" {
			fmt.Fprintf(&b, "      %-28s → %s\n", "", ch.Fix)
		}
	}
	fmt.Fprintf(&b, "\n%d ok, %d warn, %d fail\n", r.OK, r.Warn, r.Fail)
	return b.String()
}
