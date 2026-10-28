package posture

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/egress"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// Hardened es el perfil de producción de la guía de hardening: todo OK.
func Hardened() Config {
	return Config{
		Exposed: true, Token: true, IngestToken: true, UILogin: true,
		AllowedCIDRs: []string{"10.20.0.0/16"}, AIProvider: "builtin", SettingsLocked: true,
		RetentionDays: 90, Encrypt: true, UID: 65532, DataDir: "/data", DataDirMode: 0o700, DataDirPerms: true,
		NotifyDetail: "minimal", Redact: true, Screenshots: "failures",
	}
}

// defaultExposed: la instalación por defecto abierta a la red con TRACEREPORTS_INSECURE=1.
func defaultExposed() Config {
	return Config{Exposed: true, Insecure: true, AIProvider: "builtin", UID: 0, DataDir: "/data",
		DataDirMode: 0o755, DataDirPerms: true, Redact: true, Screenshots: "all"}
}

func level(r Report, id string) Check {
	for _, ch := range r.Checks {
		if ch.ID == id {
			return ch
		}
	}
	panic("no check " + id)
}

func TestHardenedProfileIsAllOK(t *testing.T) {
	r := Evaluate(Hardened())
	if r.Warn != 0 || r.Fail != 0 || r.Failed() {
		t.Fatalf("perfil endurecido:\n%s", Table(r))
	}
	if r.OK != len(r.Checks) || len(r.Checks) != 12 {
		t.Fatalf("%d checks", len(r.Checks))
	}
}

func TestDefaultExposedFailsWithFixes(t *testing.T) {
	r := Evaluate(defaultExposed())
	if !r.Failed() {
		t.Fatal("expuesta sin credenciales debe fallar")
	}
	for _, id := range []string{"auth.tokens", "network.exposure"} {
		if ch := level(r, id); ch.Level != Fail || ch.Fix == "" {
			t.Errorf("%s: %+v", id, ch)
		}
	}
	for _, ch := range r.Checks {
		if ch.Level != OK && ch.Fix == "" {
			t.Errorf("%s sin instrucción", ch.ID)
		}
		if ch.Level == OK && ch.Fix != "" {
			t.Errorf("%s OK con instrucción", ch.ID)
		}
	}
}

// Un caso por chequeo: qué lo hace pasar de OK a WARN o FAIL.
func TestEachCheck(t *testing.T) {
	cases := []struct {
		id    string
		want  string
		tweak func(*Config)
	}{
		{"auth.tokens", Fail, func(c *Config) { c.SameTokens = true }},
		{"auth.tokens", Fail, func(c *Config) { c.Token, c.IngestToken = false, false }},
		{"auth.tokens", Warn, func(c *Config) { c.Token, c.IngestToken, c.Exposed = false, false, false }},
		{"auth.tokens", Warn, func(c *Config) { c.IngestToken = false }},
		{"auth.ui_login", Warn, func(c *Config) { c.UILogin = false }},
		{"auth.ui_login", Fail, func(c *Config) { c.UILogin, c.AnyHost = false, true }},
		{"auth.ui_login", OK, func(c *Config) { c.UILogin, c.Exposed = false, false }},
		{"network.exposure", Warn, func(c *Config) { c.AllowedCIDRs = nil }},
		{"network.exposure", Fail, func(c *Config) { c.Insecure = true }},
		{"network.exposure", OK, func(c *Config) { c.AllowedCIDRs, c.Exposed = nil, false }},
		{"ai.provider", Warn, func(c *Config) { c.AIProvider, c.AIScope = "openai", egress.ScopeName }},
		{"ai.provider", OK, func(c *Config) {
			c.AIProvider, c.AIScope, c.AIAllowlist = "openai", egress.ScopeName, []string{"builtin", "openai"}
		}},
		{"ai.provider", Fail, func(c *Config) { c.AIProvider, c.AIAllowlist = "gemini", []string{"builtin"} }},
		{"ai.provider", OK, func(c *Config) { c.AIProvider, c.AIScope = "ollama", egress.ScopePrivate }},
		{"settings.locked", Warn, func(c *Config) { c.SettingsLocked = false }},
		{"data.retention", Warn, func(c *Config) { c.RetentionDays = 0 }},
		{"data.encryption", Warn, func(c *Config) { c.Encrypt = false }},
		{"data.encryption", Fail, func(c *Config) { c.EncryptError = "TRACEREPORTS_DB_KEY_FILE is inside DATA_DIR" }},
		{"process.user", Warn, func(c *Config) { c.UID = 0 }},
		{"process.user", OK, func(c *Config) { c.UID = -1 }},
		{"data.permissions", Warn, func(c *Config) { c.DataDirMode = 0o755 }},
		{"data.permissions", OK, func(c *Config) { c.DataDirMode, c.DataDirPerms = 0o777, false }},
		{"egress.destinations", Warn, func(c *Config) {
			c.NotifyDetail = ""
			c.Egress = []egress.Destination{{Kind: "notify", Name: "Slack", Host: "hooks.slack.com", Scope: egress.ScopeName}}
		}},
		{"egress.destinations", OK, func(c *Config) {
			c.Egress = []egress.Destination{{Kind: "notify", Name: "Slack", Host: "hooks.slack.com", Scope: egress.ScopeName}}
		}},
		{"evidence.redaction", Fail, func(c *Config) { c.Redact = false }},
		{"evidence.screenshots", Warn, func(c *Config) { c.Screenshots = "all" }},
		{"evidence.screenshots", OK, func(c *Config) { c.Screenshots = "off" }},
	}
	for _, tc := range cases {
		c := Hardened()
		tc.tweak(&c)
		if ch := level(Evaluate(c), tc.id); ch.Level != tc.want {
			t.Errorf("%s: %s, quería %s (%s)", tc.id, ch.Level, tc.want, ch.Detail)
		}
	}
}

// El JSON es un contrato (doctor --json y la API): cambia solo a propósito (go test -update).
func TestJSONGolden(t *testing.T) {
	for name, c := range map[string]Config{"hardened": Hardened(), "default_exposed": defaultExposed()} {
		got, _ := json.MarshalIndent(Evaluate(c), "", "  ")
		path := filepath.Join("testdata", name+".json")
		if *update {
			os.MkdirAll("testdata", 0o755)
			os.WriteFile(path, append(got, '\n'), 0o644)
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(strings.ReplaceAll(string(want), "\r\n", "\n")) != string(got) {
			t.Errorf("%s cambió:\n%s", name, got)
		}
	}
}

// Ni los valores de credenciales llegan a Config: el reporte solo dice si están.
func TestTableShowsFixes(t *testing.T) {
	out := Table(Evaluate(defaultExposed()))
	for _, want := range []string{"FAIL  API tokens", "→ set TRACEREPORTS_TOKEN", "fail\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q:\n%s", want, out)
		}
	}
}
