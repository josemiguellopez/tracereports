package main

import (
	"fmt"
	"log/slog"
	"net"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/env"
)

// Fail-closed: el servidor no queda escuchando en la red sin ninguna credencial.
//   - Con credenciales (cualquier token o el login de la UI) escucha donde diga TRACEREPORTS_BIND.
//   - Sin credenciales y sin TRACEREPORTS_BIND escucha solo en este equipo (127.0.0.1 y ::1): el
//     uso local (go run, el binario en un laptop) sigue igual y nada queda expuesto.
//   - Sin credenciales y con un TRACEREPORTS_BIND de red (0.0.0.0, ::, una IP), no arranca.
//   - En Docker el servidor escucha en todas las interfaces del contenedor aunque el puerto se
//     publique solo en 127.0.0.1 (eso no se ve desde adentro), y 127.0.0.1 dentro del contenedor no
//     es alcanzable desde el host: sin credenciales no arranca.
//   - TRACEREPORTS_INSECURE=1 acepta el riesgo a sabiendas: escucha como antes, con un aviso.

// exposure is where the server listens and with what protection.
type exposure struct {
	bind     string // TRACEREPORTS_BIND tal cual ("" = todas las interfaces)
	docker   bool   // TRACEREPORTS_RUNTIME=docker (la imagen oficial)
	token    bool   // TRACEREPORTS_TOKEN o TRACEREPORTS_INGEST_TOKEN
	uiLogin  bool   // TRACEREPORTS_UI_USER y TRACEREPORTS_UI_PASSWORD
	insecure bool   // TRACEREPORTS_INSECURE=1
}

func exposureFromEnv() exposure {
	return exposure{
		bind:   strings.TrimSpace(env.Get("BIND")),
		docker: env.Get("RUNTIME") == "docker",
		token:  env.Get("TOKEN") != "" || env.Get("INGEST_TOKEN") != "",
		uiLogin: (env.Get("UI_USER") != "" && env.Get("UI_PASSWORD") != "") ||
			(env.Get("UI_VIEWER_USER") != "" && env.Get("UI_VIEWER_PASSWORD") != ""),
		insecure: env.Bool("INSECURE"),
	}
}

// isLoopback reports whether bind only accepts connections from this machine: 127.0.0.0/8, ::1
// or localhost. "" (every interface), 0.0.0.0, :: and any other address are reachable from the
// network.
func isLoopback(bind string) bool {
	bind = strings.Trim(strings.TrimSpace(bind), "[]")
	if strings.EqualFold(bind, "localhost") {
		return true
	}
	ip := net.ParseIP(bind)
	return ip != nil && ip.IsLoopback()
}

// localBinds is where a server without credentials listens when TRACEREPORTS_BIND is empty: IPv4
// and IPv6 loopback (without ::1, on Windows each connection to "localhost" waits ~2 s before
// falling back to IPv4).
var localBinds = []string{"127.0.0.1", "::1"}

// errExposed explains how to fix a server that would listen on the network unprotected.
const errExposed = `refusing to start: TraceReports would listen on the network (%s) without credentials, so anyone who reaches this port could read and change the reports.

Set at least one of these (in the .env or the environment) and start again:

  TRACEREPORTS_TOKEN=$(openssl rand -hex 32)       # API token for writes (clients send it)
  TRACEREPORTS_UI_USER=admin                      # login to see the reports
  TRACEREPORTS_UI_PASSWORD=<a strong password>
%s
To accept the risk anyway (a closed lab network): TRACEREPORTS_INSECURE=1.
See docs/en/configuration.md#security.`

// checkExposure decides where the server listens (the addresses, without the port) or returns an
// error when it would listen on the network without any credential.
func checkExposure(e exposure) ([]string, error) {
	switch {
	case isLoopback(e.bind) || e.token || e.uiLogin:
		return []string{e.bind}, nil
	case e.insecure:
		slog.Warn("TRACEREPORTS_INSECURE=1: listening on the network without a token or login; anyone who reaches this port can read and change the reports",
			"bind", e.bind)
		return []string{e.bind}, nil
	case e.bind == "" && !e.docker:
		slog.Warn("no TRACEREPORTS_TOKEN nor UI login: listening only on this machine (127.0.0.1 and ::1); to open it to the network set credentials (see docs: configuration → Security)")
		return localBinds, nil
	}
	where := "all interfaces"
	if e.bind != "" {
		where = e.bind
	}
	local := "\nOr listen only on this machine: remove TRACEREPORTS_BIND or set TRACEREPORTS_BIND=127.0.0.1.\n"
	if e.docker {
		where += ", inside Docker"
		local = "\nIn Docker the server listens on every interface of the container, even when the port is published\nonly on 127.0.0.1: pass the credentials with the .env (docker compose) or --env-file / -e (docker run).\n"
	}
	return nil, fmt.Errorf(errExposed, where, local)
}

// listen opens the server's listeners. A loopback address that does not exist on this machine (::1
// without IPv6) is skipped when another one opened.
func listen(binds []string, port string) ([]net.Listener, error) {
	var lns []net.Listener
	var firstErr error
	for _, b := range binds {
		ln, err := net.Listen("tcp", net.JoinHostPort(b, port))
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if b == "::1" && len(binds) > 1 {
				continue
			}
			for _, l := range lns {
				l.Close()
			}
			return nil, err
		}
		lns = append(lns, ln)
	}
	if len(lns) == 0 {
		return nil, firstErr
	}
	return lns, nil
}
