// Command tracereports runs the TraceReports server.
//
// Environment variables:
//
//	PORT            HTTP port (default 8080)
//	TRACEREPORTS_BIND  address to listen on (default: all interfaces; 127.0.0.1 = only this machine)
//	DATA_DIR        data directory for SQLite + screenshots (default ./data)
//	AI_PROVIDER     builtin | gemini | anthropic | openai | openai_compatible | ollama | off (optional;
//	                inferred from GEMINI_API_KEY / ANTHROPIC_API_KEY / OPENAI_API_KEY / OLLAMA_HOST;
//	                without any of them: builtin, the local rules engine with no LLM and no network)
//	AI_MODEL / AI_API_KEY / AI_BASE_URL  model, key and API URL (each provider has defaults)
//	GEMINI_API_KEY / GEMINI_MODEL / GEMINI_BASE_URL  still supported
//	TRACEREPORTS_SETTINGS_LOCKED  1 = the Settings screen is read-only (config only from the env)
//	TRACEREPORTS_ENV_FILE  file with KEY=VALUE lines read at startup (default .env; real env vars win)
//	TRACEREPORTS_TOKEN   token required to write to the API (optional, recommended); it may also change settings
//	TRACEREPORTS_INGEST_TOKEN  restricted token for test clients/CI: writes results and reads, never administers
//	TRACEREPORTS_UI_USER / TRACEREPORTS_UI_PASSWORD  HTTP Basic login for the UI (optional)
//	TRACEREPORTS_ALLOWED_HOSTS  hosts served without login besides localhost (comma separated; * = any)
//	TRACEREPORTS_LOCAL_ADMIN  1 = with a token and no UI login, the same machine (no proxy) may change settings
//	TEAMS_WEBHOOK_URL / SLACK_WEBHOOK_URL  run summary notifications (optional)
//	PUBLIC_URL      public base URL, used for links in notifications
//	NOTIFY_ON       always (default) | failures
//	NETWORK_MAX_BODY_KB  max stored size of each captured response body (default 256; 0 = no bodies)
//	TRACEREPORTS_REDACT  off = store secrets as received (default: masked before storing)
//	TRACEREPORTS_REDACT_HEADERS / TRACEREPORTS_REDACT_KEYS / TRACEREPORTS_REDACT_PATTERNS  extra masking rules
//	TRACEREPORTS_RETENTION_DAYS  delete runs (and screenshots) older than N days (default: keep all)
//	TRACEREPORTS_STALE_RUN_HOURS  close as incomplete the runs without activity for N hours (default 24; 0 = never)
//	TRACEREPORTS_AI_MAX_PER_RUN  automatic per-test AI analyses per run (default 50; 0 = no limit; not for builtin)
//	TRACEREPORTS_AI_BUILTIN_HINT  0 = LLM prompts without the local engine's conclusion (default: included)
//	TRACEREPORTS_SECRET_KEY  master key (32 bytes, base64/hex) to encrypt the AI key saved from Settings
//	TRACEREPORTS_SECRET_KEY_PREVIOUS  the previous master key, only while rotating it
//	TRACEREPORTS_LAB      off = disable FixLab's Request Lab (resending captured calls from the report)
//	TRACEREPORTS_LAB_HOSTS  extra hosts a shared server's Lab may call (host, host:port or *.domain)
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/josemiguellopez/tracereports"
	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
	"github.com/josemiguellopez/tracereports/internal/live"
	"github.com/josemiguellopez/tracereports/internal/notify"
	"github.com/josemiguellopez/tracereports/internal/owners"
	"github.com/josemiguellopez/tracereports/internal/redact"
	"github.com/josemiguellopez/tracereports/internal/release"
	"github.com/josemiguellopez/tracereports/internal/secret"
	"github.com/josemiguellopez/tracereports/internal/tracker"
)

func main() {
	// subcomandos sin servidor: tracereports report ... / tracereports push ...
	if len(os.Args) > 1 {
		var err error
		switch os.Args[1] {
		case "report":
			err = runReport(os.Args[2:])
		case "push":
			err = runPush(os.Args[2:])
		case "pr-comment":
			err = runPRComment(os.Args[2:])
		case "secrets":
			err = runSecrets(os.Args[2:], os.Stdout)
		case "repro":
			err = runRepro(os.Args[2:])
		case "verify":
			err = runVerify(os.Args[2:])
		case "mcp":
			err = runMCP(os.Args[2:])
		case "demo":
			err = runDemo(os.Args[2:])
		case "selftest":
			err = runSelftest(os.Args[2:])
		case "version", "-version", "--version":
			fmt.Println("tracereports " + buildVersion())
			return
		case "-h", "-help", "--help", "help":
			fmt.Println("Usage: tracereports              start the server (configuration: environment variables, see the docs)\n" +
				"       tracereports report ...   build a static HTML report without a server (recording, JUnit XML, Allure)\n" +
				"       tracereports push ...     upload a recording, allure-results or JUnit XML to a server\n" +
				"       tracereports pr-comment   comment the run summary on the pull request\n" +
				"       tracereports secrets ...  status / migrate of the credentials saved from Settings\n" +
				"       tracereports repro ...    resend the calls of a FixLab capsule with a fresh session\n" +
				"       tracereports verify ...   run the saved checks of a capsule (exit code 1 if one fails)\n" +
				"       tracereports mcp          MCP server (stdio) for agents such as Claude Code\n" +
				"       tracereports demo         try it in a minute: sample data and a demo shop, only on this machine\n" +
				"       tracereports selftest     play the scenarios recorded with the real clients and check the results\n" +
				"       tracereports version      print the version\n" +
				"Each command explains its flags with -h. Guide: docs/en/cli.md")
			return
		default:
			fmt.Fprintf(os.Stderr, "unknown command %q (commands: report, push, pr-comment, secrets, repro, verify, mcp, demo, selftest, version; no command starts the server)\n", os.Args[1])
			os.Exit(2)
		}
		if err != nil {
			if !errors.Is(err, flag.ErrHelp) {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		}
		return
	}
	if err := run(); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run() error { return runServer(context.Background()) }

// runServer starts the server and serves until parent is cancelled, Ctrl+C or SIGTERM.
func runServer(parent context.Context) error {
	// el binario lee el mismo .env que usa Docker Compose (las variables ya definidas mandan)
	envFile := env.Get("ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if n, err := loadDotEnv(envFile); err != nil {
		return err
	} else if n > 0 {
		slog.Info("configuration loaded from file", "file", envFile, "variables", n)
	}
	port := envOr("PORT", "8080")
	dataDir := envOr("DATA_DIR", "./data")
	// en la imagen el volumen es /data: otra ruta (p. ej. el ./data de un .env pensado para go run)
	// guarda la base dentro del contenedor y se pierde al recrearlo
	if env.Get("RUNTIME") == "docker" && filepath.Clean(dataDir) != "/data" {
		slog.Warn("DATA_DIR is not the /data volume: the data will be lost when the container is recreated (remove DATA_DIR from the .env)", "data_dir", dataDir)
	}
	shotsDir := filepath.Join(dataDir, "screenshots")
	if err := os.MkdirAll(shotsDir, 0o755); err != nil {
		return err
	}

	if err := checkLegacyDB(filepath.Join(dataDir, "tracereports.db")); err != nil {
		return err
	}
	store, err := db.Open(filepath.Join(dataDir, "tracereports.db"))
	if err != nil {
		return err
	}
	defer store.Close()
	if err := settleAIBudget(store); err != nil {
		return err
	}

	webRoot, err := fs.Sub(tracereports.WebFS, "web")
	if err != nil {
		return err
	}

	notifier := notify.New(store)
	if notifier.Enabled() {
		slog.Info("run notifications enabled (Teams/Slack)")
	}
	// los envíos que fallaron por algo pasajero (o que un reinicio dejó pendientes) se reintentan
	notifyCtx, stopNotify := context.WithCancel(context.Background())
	defer stopNotify()
	notifier.Start(notifyCtx)
	auth := api.Auth{
		Token: env.Get("TOKEN"),
		// opcional: credencial de CI que envía resultados y lee, pero no administra
		IngestToken: env.Get("INGEST_TOKEN"),
		UIUser:      env.Get("UI_USER"),
		UIPass:      env.Get("UI_PASSWORD"),
		// con token = despliegue: el mismo equipo también necesita credenciales, salvo que se diga
		LocalAdmin: env.Bool("LOCAL_ADMIN"),
	}
	if auth.Token == "" && auth.IngestToken == "" {
		slog.Warn("TRACEREPORTS_TOKEN not set: anyone who reaches this port can write to the API")
	}
	if auth.Token != "" && auth.Token == auth.IngestToken {
		return errors.New("TRACEREPORTS_INGEST_TOKEN must be different from TRACEREPORTS_TOKEN (it has fewer permissions)")
	}
	if auth.UIUser == "" || auth.UIPass == "" {
		slog.Warn("TRACEREPORTS_UI_USER/TRACEREPORTS_UI_PASSWORD not set: the reports are readable without login")
	}

	ownerRules, err := owners.FromEnv()
	if err != nil {
		return err
	}
	if ownerRules.Len() > 0 {
		slog.Info("test owners enabled", "rules", ownerRules.Len())
	}

	gate, err := release.Parse(env.Get("RELEASE_GATE"))
	if err != nil {
		return err
	}

	hub := live.NewHub()
	redaction := redact.FromEnv()
	if !redaction.Enabled() {
		slog.Warn("TRACEREPORTS_REDACT=off: secrets in the evidence are stored as received")
	}
	secrets, err := secret.FromEnv()
	if err != nil {
		return err // una clave maestra mal escrita no se ignora: las credenciales no se podrían leer
	}
	if secrets.Enabled() {
		slog.Info("credentials saved from Settings are encrypted", "key_id", secrets.KeyID())
	}
	analyzer := ai.New(store)
	analyzer.Redact = redaction
	// los análisis de IA terminan en segundo plano: la UI se entera en vivo por SSE
	analyzer.OnChange = func(kind string, runID, testID int64) {
		hub.Publish(live.Event{Type: kind, RunID: runID, TestID: testID})
	}
	apiServer := &api.Server{
		Store:          store,
		AI:             analyzer,
		Notify:         notifier,
		Live:           hub,
		Auth:           auth,
		ScreenshotsDir: shotsDir,
		Web:            webRoot,
		SettingsLocked: env.Bool("SETTINGS_LOCKED"),
		Redact:         redaction,
		Secrets:        secrets,
		// sin login, solo se atiende a Host locales o permitidos (protección contra DNS rebinding)
		Hosts: api.NewHostPolicy(env.Get("ALLOWED_HOSTS"), os.Getenv("PUBLIC_URL")),
		// GitHub, Jira o Azure DevOps para crear tickets desde un fallo (TRACEREPORTS_GITHUB_* ...)
		Trackers: tracker.FromEnv(),
		// links a los logs y a la traza de cada llamada al backend (Grafana, Kibana, Datadog...)
		LogsURL: env.Get("LOGS_URL"), TraceURL: env.Get("TRACE_URL"),
		// dueño de cada test, como CODEOWNERS (TRACEREPORTS_OWNERS / TRACEREPORTS_OWNERS_FILE)
		Owners: ownerRules,
		// criterios de "¿podemos salir a producción?" (TRACEREPORTS_RELEASE_GATE)
		ReleaseGate: &gate,
		// FixLab: reenviar llamadas capturadas desde el reporte (TRACEREPORTS_LAB=off lo apaga)
		Lab: api.LabConfig{Disabled: labOff(env.Get("LAB")), Hosts: api.ParseLabHosts(env.Get("LAB_HOSTS"))},
		// tracereports demo (TRACEREPORTS_DEMO lo pone la demo)
		Demo: env.Bool("DEMO"),
	}
	if auth.UIUser == "" || auth.UIPass == "" {
		slog.Info("without UI login only these hosts are served (plus localhost)", "allowed_hosts", apiServer.Hosts.Names())
	}
	// lo guardado desde la pantalla de Ajustes manda sobre el .env
	if err := apiServer.LoadSettings(); err != nil {
		return err
	}
	for _, t := range apiServer.Trackers {
		slog.Info("tickets enabled", "tracker", t.Name())
	}
	switch {
	case analyzer.UsesLLM():
		slog.Info("AI triage enabled", "provider", analyzer.Provider(), "model", analyzer.Model())
	case analyzer.Enabled():
		slog.Info("failure diagnosis: local rules engine (no LLM, no network); set AI_PROVIDER/AI_API_KEY or use Settings for an LLM", "rules", analyzer.Model())
	default:
		slog.Info("failure diagnosis disabled (AI_PROVIDER=off)")
	}
	// lo que un reinicio dejó a medias (diagnósticos PENDING) se retoma, sin quedar colgado
	analyzer.Recover(func(runID int64) { notifier.RunFinished(runID) })

	srv := &http.Server{
		// TRACEREPORTS_BIND=127.0.0.1 atiende solo a este equipo (vacío: todas las interfaces)
		Addr:              net.JoinHostPort(strings.TrimSpace(env.Get("BIND")), port),
		Handler:           apiServer.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
	defer stop()
	startRetention(ctx, store, shotsDir)
	startStaleRuns(ctx, apiServer)
	if err := startWeekly(ctx, env.Get("WEEKLY_SUMMARY"), apiServer, notifier); err != nil {
		return err
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("TraceReports listening", "url", "http://localhost:"+port)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		slog.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdownCtx)
	analyzer.Wait(shutdownCtx)
	return nil
}

// labOff reports whether TRACEREPORTS_LAB turns the Lab off (off, 0, false or no).
func labOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
