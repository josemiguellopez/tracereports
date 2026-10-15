package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports"
	"github.com/josemiguellopez/tracereports/internal/demo"
)

// `tracereports demo`: el servidor de siempre con datos de ejemplo, en un minuto y sin instalar
// nada más. Una base temporal, solo en este equipo, aislada de la configuración de quien lo corre
// (sin tokens, ni notificaciones, ni tickets, ni IA salvo -ai), y una tienda de prueba cuyo backend
// se puede romper y arreglar para probar FixLab de punta a punta.

// demoKeep are the variables kept with -ai: the AI provider (it spends tokens, so it is opt-in).
var demoAIVars = []string{"AI_PROVIDER", "AI_MODEL", "AI_API_KEY", "AI_BASE_URL", "GEMINI_API_KEY", "GEMINI_MODEL", "GEMINI_BASE_URL",
	"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "OLLAMA_HOST", "TRACEREPORTS_AI_MAX_PER_RUN", "TRACEREPORTS_AI_STATUS_URL"}

// demoSideEffects are variables that would make the demo talk to the outside world.
var demoSideEffects = []string{"TEAMS_WEBHOOK_URL", "SLACK_WEBHOOK_URL", "PUBLIC_URL", "NOTIFY_ON", "NETWORK_MAX_BODY_KB", "PORT", "DATA_DIR"}

// demoEnv isolates the demo: every TRACEREPORTS_* setting and the side effects are cleared (the
// AI ones too, unless keepAI), then the demo's own are set.
func demoEnv(dir string, port int, keepAI bool) {
	keep := map[string]bool{}
	if keepAI {
		for _, k := range demoAIVars {
			keep[strings.ToUpper(k)] = true
		}
	}
	drop := map[string]bool{}
	for _, k := range demoSideEffects {
		drop[k] = true
	}
	if !keepAI {
		for _, k := range demoAIVars {
			drop[k] = true
		}
	}
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		up := strings.ToUpper(k)
		if keep[up] {
			continue
		}
		if strings.HasPrefix(up, "TRACEREPORTS_") || drop[up] {
			os.Unsetenv(k)
		}
	}
	os.Setenv("PORT", strconv.Itoa(port))
	os.Setenv("DATA_DIR", dir)
	os.Setenv("TRACEREPORTS_BIND", "127.0.0.1")                        // solo este equipo: sin aviso del firewall
	os.Setenv("TRACEREPORTS_ENV_FILE", filepath.Join(dir, "none.env")) // el .env del proyecto no aplica
}

// freePort returns want, or the first free port of this machine from start.
func freePort(want, start int) (int, error) {
	if want > 0 {
		return want, nil
	}
	for p := start; p < start+100; p++ {
		l, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(p)))
		if err == nil {
			l.Close()
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port from %d", start)
}

func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	case "darwin":
		cmd = exec.Command("open", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	_ = cmd.Start()
}

// waitReady waits until the server answers (or failed to start).
func waitReady(base string, errCh <-chan error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			if err == nil {
				err = errors.New("the server stopped")
			}
			return err
		default:
		}
		if res, err := http.Get(base + "/api/v1/runs"); err == nil {
			res.Body.Close()
			if res.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(150 * time.Millisecond)
	}
	return errors.New("the server did not start in time")
}

func demoGuide(w io.Writer, base, shopURL string, sum *demo.Summary, dir string, keep bool) {
	fmt.Fprintf(w, `
  TraceReports demo · sample data ready (%d runs, %d tests, %d failed)

  ▸ Report:      %s
  ▸ Playground:  %s   (run each client's scenarios, break or fix the shop backend)

  A team tests the shop with pytest + Playwright, Playwright Test (JS), Selenium + JUnit 5 (Java),
  go test, the REST API and a Maven JUnit XML import. Every run is a real recording of those
  clients. Tonight the orders backend broke. Suggested tour:

   1. The latest run opens by itself: 2 of 3 tests failed. Open "test_checkout_pays_the_order":
      steps, the browser's screenshot, network (password masked), console and the command to run it.
   2. "test_add_to_cart_with_the_old_selector" failed because a selector changed: TraceReports
      suggests the new one.
   3. Network tab → the POST /api/orders 500 → Lab → password %q → Send: "Reproduced".
   4. In the playground switch the orders backend to working, send again: "No longer fails".
      Save as a check, then on the incident (AI view) press Verify: "Verified: fixed".
   5. Search, Metrics and Release: %d nights of history, a flaky test and failures already fixed.
   6. The playground runs each client's scenarios one by one, or all of them with their checks.
   7. In a terminal: tracereports repro / verify with the command the Lab copies, or
      tracereports mcp for Claude Code (TRACEREPORTS_URL=%s).

`, sum.Runs, sum.Tests, sum.Failed, base+fmt.Sprintf("/#run=%d&view=tests", sum.Showcase), shopURL, demo.ShopPassword, sum.Nights, base)
	if keep {
		fmt.Fprintf(w, "  Data kept in %s (DATA_DIR=%s to open it again).\n", dir, dir)
	} else {
		fmt.Fprintln(w, "  The sample data is deleted when you stop it (-keep to keep it).")
	}
	fmt.Fprintln(w, "  Ctrl+C to stop.")
}

// runDemo starts the server with sample data and the demo shop.
func runDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	port := fs.Int("port", 0, "port of the report (default: the first free one from 8080)")
	shopPort := fs.Int("shop-port", 0, "port of the demo shop (default: the first free one from 8180)")
	keep := fs.Bool("keep", false, "keep the sample data when the demo stops")
	noOpen := fs.Bool("no-open", false, "do not open the browser")
	withAI := fs.Bool("ai", false, "use the AI provider of your environment (AI_PROVIDER, ANTHROPIC_API_KEY...): it spends tokens")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: tracereports demo [flags]\nStarts TraceReports with sample data (temporary, only on this machine) and a demo shop to try FixLab.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return demoMain(ctx, demoOptions{port: *port, shopPort: *shopPort, keep: *keep, open: !*noOpen, ai: *withAI, out: os.Stdout})
}

type demoOptions struct {
	port, shopPort int
	keep, open, ai bool
	out            io.Writer
	ready          func(base, shop string, sum *demo.Summary) // tests: avisa cuando está lista
}

// demoKept marks the data of a demo started with -keep: cleanOldDemos leaves it alone.
const demoKept = "KEPT"

// cleanOldDemos removes the data of demos stopped without Ctrl+C (closed terminal, killed
// process). One still running keeps its database open and is left alone, and so is one kept.
func cleanOldDemos() {
	old, _ := filepath.Glob(filepath.Join(os.TempDir(), "tracereports-demo-*"))
	for _, d := range old {
		if _, err := os.Stat(filepath.Join(d, demoKept)); err == nil {
			continue
		}
		if fi, err := os.Stat(d); err == nil && fi.IsDir() && time.Since(fi.ModTime()) > 12*time.Hour {
			_ = os.RemoveAll(d)
		}
	}
}

func demoMain(ctx context.Context, o demoOptions) error {
	cleanOldDemos()
	dir, err := os.MkdirTemp("", "tracereports-demo-")
	if err != nil {
		return err
	}
	if o.keep {
		_ = os.WriteFile(filepath.Join(dir, demoKept), []byte("tracereports demo -keep: delete this folder to discard the data\n"), 0o644)
	} else {
		defer os.RemoveAll(dir)
	}
	port, err := freePort(o.port, 8080)
	if err != nil {
		return err
	}
	shopPort, err := freePort(o.shopPort, 8180)
	if err != nil {
		return err
	}
	demoEnv(dir, port, o.ai)
	// los avisos de un servidor de producción (sin token, sin login) no aplican a la demo
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	defer slog.SetDefault(prev)

	shopLn, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(shopPort)))
	if err != nil {
		return fmt.Errorf("demo shop: %w (choose another one with -shop-port)", err)
	}
	base := fmt.Sprintf("http://localhost:%d", port)
	shopURL := fmt.Sprintf("http://localhost:%d", shopPort)
	// la tienda y, en su página principal, el playground de escenarios
	pg := &demo.Playground{Shop: demo.NewShop(), Base: fmt.Sprintf("http://127.0.0.1:%d", port), Public: base, ShopURL: shopURL}
	// las mismas fuentes locales del reporte (Inter, JetBrains Mono, Press Start 2P): sin internet
	if fonts, err := fs.Sub(tracereports.WebFS, "web/vendor/fonts"); err == nil {
		pg.Fonts = fonts
	}
	shopSrv := &http.Server{Handler: pg, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = shopSrv.Serve(shopLn) }()
	defer shopSrv.Close()

	srvCtx, stop := context.WithCancel(ctx)
	defer stop()
	errCh := make(chan error, 1)
	go func() { errCh <- runServer(srvCtx) }()
	if err := waitReady(fmt.Sprintf("http://127.0.0.1:%d", port), errCh, 60*time.Second); err != nil {
		return fmt.Errorf("start the report on port %d: %w (choose another one with -port)", port, err)
	}
	fmt.Fprintln(o.out, "Loading the sample data…")
	sum, err := demo.Seed(srvCtx, fmt.Sprintf("http://127.0.0.1:%d", port), shopURL, time.Now())
	if err != nil {
		stop()
		<-errCh
		return fmt.Errorf("load the sample data: %w", err)
	}
	demoGuide(o.out, base, shopURL, sum, dir, o.keep)
	if o.open {
		openBrowser(base + fmt.Sprintf("/#run=%d&view=tests", sum.Showcase))
	}
	if o.ready != nil {
		o.ready(base, shopURL, sum)
	}
	err = <-errCh
	if errors.Is(err, http.ErrServerClosed) {
		err = nil
	}
	return err
}
