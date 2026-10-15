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
	target := fs.String("url", "", "TraceReports to test (default: a temporary one on this machine)")
	only := fs.String("only", "", "scenario ids separated by commas (default: all)")
	list := fs.Bool("list", false, "list the scenarios and exit")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: tracereports selftest [flags]\nPlays the scenarios recorded with the real clients (pytest, Playwright JS, Selenium Java, Go, REST, JUnit)\nand checks what TraceReports shows. Exit code 1 if an expectation fails.")
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
	return selftest(ctx, selftestOptions{url: *target, only: *only, out: os.Stdout})
}

type selftestOptions struct {
	url, only string
	out       io.Writer
}

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
		bad := 0
		for _, c := range results {
			if !c.OK {
				bad++
			}
		}
		checks += len(results)
		mark := "✓"
		if bad > 0 {
			mark, failed = "✗", failed+1
		}
		fmt.Fprintf(o.out, "%s %-24s %-52s %d/%d  run #%d\n", mark, sc.ID, sc.Title, len(results)-bad, len(results), runID)
		for _, c := range results {
			if !c.OK {
				fmt.Fprintf(o.out, "    ✗ %s: %s\n", c.Name, c.Detail)
			}
		}
	}
	if failed > 0 {
		fmt.Fprintf(o.out, "\n%d of %d scenarios failed.\n", failed, scenarios)
		return fmt.Errorf("%w: %d of %d scenarios", errSelftest, failed, scenarios)
	}
	fmt.Fprintf(o.out, "\nAll %d scenarios pass (%d checks).\n", scenarios, checks)
	return nil
}
