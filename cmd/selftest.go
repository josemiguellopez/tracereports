package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/demo"
)

// `tracereports selftest`: reproduce los escenarios grabados con los clientes reales (pytest,
// Playwright JS, Selenium Java, Go, REST, JUnit) contra un servidor y comprueba que muestre lo
// esperado. Sin -url levanta uno temporal, solo en este equipo; con -url prueba uno ya desplegado
// (deja ahí las ejecuciones de los escenarios). Sale con código 1 si algo no se cumple.

var errSelftest = errors.New("selftest failed")

func runSelftest(args []string) error {
	fs := flag.NewFlagSet("selftest", flag.ContinueOnError)
	target := fs.String("url", "", "TraceReports to test (default: a temporary one on this machine; TRACEREPORTS_TOKEN if it is protected)")
	only := fs.String("only", "", "scenario ids separated by commas (default: all)")
	list := fs.Bool("list", false, "list the scenarios and exit")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: tracereports selftest [flags]\nPlays the scenarios recorded with the real clients (pytest, Playwright JS, Selenium Java, Go, REST, JUnit, Allure)\nand checks what TraceReports shows. Exit code 1 if an expectation fails.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *list {
		for _, sc := range demo.Scenarios {
			fmt.Printf("%-24s %-6s %s\n", sc.ID, sc.Kind, sc.Title)
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	o := selftestOptions{url: *target, only: *only, out: os.Stdout}
	if o.url != "" {
		o.token = os.Getenv("TRACEREPORTS_TOKEN") // un servidor protegido
	}
	return selftest(ctx, o)
}

type selftestOptions struct {
	url, only, token string
	out              io.Writer
	// stats (opcional) recibe los aciertos del motor local de reglas
	stats *builtinStats
}

// builtinStats counts the scenarios the local rules engine diagnosed as expected.
type builtinStats struct{ Hits, Total int }

func selftest(ctx context.Context, o selftestOptions) error {
	want := map[string]bool{}
	for _, id := range strings.Split(o.only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			if _, ok := demo.ScenarioByID(id); !ok {
				return fmt.Errorf("unknown scenario %q (tracereports selftest -list)", id)
			}
			want[id] = true
		}
	}
	base := strings.TrimRight(o.url, "/")
	if base == "" {
		dir, err := os.MkdirTemp("", "tracereports-selftest-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(dir)
		port, err := freePort(0, 8090)
		if err != nil {
			return err
		}
		demoEnv(dir, port, false)
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
		defer slog.SetDefault(prev)
		srvCtx, stop := context.WithCancel(ctx)
		errCh := make(chan error, 1)
		go func() { errCh <- runServer(srvCtx) }()
		defer func() { stop(); <-errCh }()
		base = fmt.Sprintf("http://127.0.0.1:%d", port)
		if err := waitReady(base, errCh, 60*time.Second); err != nil {
			return fmt.Errorf("start a temporary TraceReports: %w", err)
		}
	}
	ctx = demo.WithToken(ctx, o.token)
	shop := demo.NewShop()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	shopSrv := &http.Server{Handler: shop, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = shopSrv.Serve(ln) }()
	defer shopSrv.Close()
	shopURL := "http://" + ln.Addr().String()

	fmt.Fprintf(o.out, "TraceReports selftest against %s\n\n", base)
	scenarios, checks, failed := 0, 0, 0
	var local builtinStats
	for _, sc := range demo.Scenarios {
		if len(want) > 0 && !want[sc.ID] {
			continue
		}
		scenarios++
		shop.Reset()
		for k, v := range sc.Bugs {
			shop.SetBug(k, v)
		}
		runID, err := demo.Play(ctx, base, shopURL, sc, time.Now())
		if err != nil {
			failed++
			fmt.Fprintf(o.out, "✗ %-24s %s\n    could not send it: %v\n", sc.ID, sc.Title, err)
			continue
		}
		results := demo.Verify(ctx, base, sc, runID)
		bad, counted := 0, 0
		for _, c := range results {
			switch {
			case c.Builtin: // cuenta para la tasa de aciertos del motor local, no para el escenario
				local.Total++
				if c.OK {
					local.Hits++
				}
			case !c.OK:
				bad++
				counted++
			default:
				counted++
			}
		}
		checks += counted
		mark := "✓"
		if bad > 0 {
			mark, failed = "✗", failed+1
		}
		fmt.Fprintf(o.out, "%s %-24s %-52s %d/%d  run #%d\n", mark, sc.ID, sc.Title, counted-bad, counted, runID)
		for _, c := range results {
			switch {
			case !c.OK && c.Builtin:
				fmt.Fprintf(o.out, "    · %s: %s\n", c.Name, c.Detail)
			case !c.OK:
				fmt.Fprintf(o.out, "    ✗ %s: %s\n", c.Name, c.Detail)
			}
		}
	}
	if o.stats != nil {
		*o.stats = local
	}
	if local.Total > 0 {
		rate := float64(local.Hits) / float64(local.Total)
		fmt.Fprintf(o.out, "\nLocal rules engine: %d/%d scenarios diagnosed as expected (%.0f %%, minimum %.0f %%).\n",
			local.Hits, local.Total, rate*100, demo.BuiltinMinRate*100)
		if rate < demo.BuiltinMinRate {
			return fmt.Errorf("%w: the local rules engine got %d of %d scenarios right", errSelftest, local.Hits, local.Total)
		}
	}
	if failed > 0 {
		fmt.Fprintf(o.out, "\n%d of %d scenarios failed.\n", failed, scenarios)
		return fmt.Errorf("%w: %d of %d scenarios", errSelftest, failed, scenarios)
	}
	fmt.Fprintf(o.out, "\nAll %d scenarios pass (%d checks).\n", scenarios, checks)
	return nil
}
