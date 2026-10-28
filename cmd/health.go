package main

import (
	"flag"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/env"
)

// `tracereports health`: pregunta a GET /healthz del servidor de este equipo si está arriba. Es el
// healthcheck de Docker (la imagen distroless no tiene shell ni curl). Sale con código 1 si no
// responde 200.

func runHealth(args []string) error {
	fs := flag.NewFlagSet("health", flag.ContinueOnError)
	target := fs.String("url", "", "server to check (default: this machine, on PORT and TRACEREPORTS_BIND)")
	fs.Usage = func() {
		fmt.Fprintln(fs.Output(), "Usage: tracereports health [-url URL]\nChecks that the server answers GET /healthz (Docker healthcheck). Exit code 1 if it does not.")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	url := *target
	if url == "" {
		url = localHealthURL(strings.TrimSpace(env.Get("BIND")), envOr("PORT", "8080"))
	}
	client := &http.Client{Timeout: 3 * time.Second}
	res, err := client.Get(strings.TrimRight(url, "/") + "/healthz")
	if err != nil {
		return fmt.Errorf("health: %w", err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("health: HTTP %d", res.StatusCode)
	}
	return nil
}

// localHealthURL is where this machine reaches its own server: the TRACEREPORTS_BIND address when it
// is a specific one, otherwise loopback.
func localHealthURL(bind, port string) string {
	host := "127.0.0.1"
	if b := strings.Trim(bind, "[]"); b != "" && b != "0.0.0.0" && b != "::" {
		host = b
	}
	return "http://" + net.JoinHostPort(host, port)
}
