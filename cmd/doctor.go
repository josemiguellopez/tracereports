package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/atrest"
	"github.com/josemiguellopez/tracereports/internal/egress"
	"github.com/josemiguellopez/tracereports/internal/env"
	"github.com/josemiguellopez/tracereports/internal/posture"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

const doctorUsage = `Usage: tracereports doctor --security [-json] [-url URL] [-data-dir DIR]

Checks the installation's security against the hardening guide: tokens, UI login, network exposure, AI
provider, Settings lock, retention, encryption at rest, process user, DATA_DIR permissions, outbound
destinations, redaction and the screenshot policy. Prints OK / WARN / FAIL with what to do, and exits
with code 1 when a check fails (usable as a CI gate).

  without -url  checks this machine's configuration (environment and .env), as the server would start
  -url URL      asks a running server (GET /api/v1/security/posture with TRACEREPORTS_TOKEN): it also
                sees the AI provider chosen in Settings
  -json         the report as JSON (stable contract, "version" field)
`

// errPostureFailed: a critical check failed (exit code 1).
var errPostureFailed = errors.New("security check: critical checks failed")

func runDoctor(args []string, out io.Writer) error {
	fl := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fl.Usage = func() { fmt.Fprint(fl.Output(), doctorUsage); fl.PrintDefaults() }
	security := fl.Bool("security", false, "run the security check")
	asJSON := fl.Bool("json", false, "print the report as JSON")
	url := fl.String("url", "", "a running server to ask (default: this machine's configuration)")
	dataDir := fl.String("data-dir", "", "data folder (default DATA_DIR or ./data)")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if !*security {
		fmt.Fprint(out, doctorUsage)
		return errors.New("missing --security (the only check available)")
	}
	if _, err := loadDotEnv(envFileName()); err != nil {
		return err
	}
	var r posture.Report
	if *url != "" {
		var err error
		if r, err = remotePosture(*url, env.Get("TOKEN")); err != nil {
			return err
		}
	} else {
		dir := *dataDir
		if dir == "" {
			dir = envOr("DATA_DIR", "./data")
		}
		r = posture.Evaluate(postureFromEnv(ai.ConfigFromEnv(), dir))
	}
	if *asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		if err := enc.Encode(r); err != nil {
			return err
		}
	} else {
		fmt.Fprint(out, posture.Table(r))
	}
	if r.Failed() {
		return errPostureFailed
	}
	return nil
}

// remotePosture asks a running server for its posture (the admin token reads it).
func remotePosture(base, token string) (posture.Report, error) {
	var r posture.Report
	req, err := http.NewRequest(http.MethodGet, strings.TrimRight(base, "/")+"/api/v1/security/posture", nil)
	if err != nil {
		return r, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return r, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return r, fmt.Errorf("%s answered %s (the security check needs TRACEREPORTS_TOKEN, the UI login or the same machine)", base, res.Status)
	}
	err = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&r)
	return r, err
}

// postureFromEnv gathers the facts of the configuration (never a credential value): what the
// server would run with, given the active AI configuration c.
func postureFromEnv(c ai.Config, dataDir string) posture.Config {
	e := exposureFromEnv()
	pc := posture.Config{
		Exposed:        onNetwork(e),
		Insecure:       e.insecure,
		Token:          env.Get("TOKEN") != "",
		IngestToken:    env.Get("INGEST_TOKEN") != "",
		UILogin:        e.uiLogin,
		AIProvider:     c.Provider,
		SettingsLocked: env.Bool("SETTINGS_LOCKED"),
		UID:            os.Getuid(),
		DataDir:        dataDir,
		DataDirPerms:   runtime.GOOS != "windows",
		Egress:         egress.Destinations(c),
		NotifyDetail:   strings.ToLower(strings.TrimSpace(os.Getenv("NOTIFY_DETAIL"))),
		Redact:         redact.FromEnv().Enabled(),
	}
	pc.SameTokens = pc.Token && env.Get("TOKEN") == env.Get("INGEST_TOKEN")
	for _, h := range strings.Split(env.Get("ALLOWED_HOSTS"), ",") {
		pc.AnyHost = pc.AnyHost || strings.TrimSpace(h) == "*"
	}
	if cidrs, err := api.ParseCIDRs(env.Get("ALLOWED_CIDRS")); err == nil {
		for _, p := range cidrs {
			pc.AllowedCIDRs = append(pc.AllowedCIDRs, p.String())
		}
	}
	// con AI_ALLOWED_PROVIDERS la decisión es explícita; un proveedor fuera de ella queda en FAIL
	if strings.TrimSpace(env.Get("AI_ALLOWED_PROVIDERS")) != "" {
		for _, p := range ai.AllowedProviders() {
			pc.AIAllowlist = append(pc.AIAllowlist, p.ID)
		}
	}
	for _, d := range pc.Egress {
		if d.Kind == "ai" {
			pc.AIScope = d.Scope
		}
	}
	if days, err := strconv.Atoi(strings.TrimSpace(env.Get("RETENTION_DAYS"))); err == nil && days > 0 {
		pc.RetentionDays = days
	}
	vault, err := atrest.FromEnv(dataDir)
	switch {
	case err != nil:
		pc.EncryptError = err.Error()
	default:
		pc.Encrypt = vault.Encrypting()
	}
	if st, err := os.Stat(dataDir); err == nil {
		pc.DataDirMode = st.Mode()
	}
	policy, err := api.ParseScreenshotPolicy(env.Get("SCREENSHOTS"))
	if err != nil {
		policy = "all"
	}
	pc.Screenshots = policy
	return pc
}

// onNetwork reports whether the server would listen beyond this machine (the same rule as
// checkExposure, without its log lines).
func onNetwork(e exposure) bool {
	if e.docker {
		return true
	}
	if isLoopback(e.bind) {
		return false
	}
	// sin bind y sin credenciales (ni INSECURE) se queda en este equipo
	return e.bind != "" || e.token || e.uiLogin || e.insecure
}
