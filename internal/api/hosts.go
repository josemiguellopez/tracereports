package api

import (
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

// Protección contra DNS rebinding. Una página maliciosa abierta en el navegador puede hacer que su
// dominio resuelva a 127.0.0.1 y, como el navegador la considera del mismo origen, leer lo que
// sirve un TraceReports local sin login (reportes, capturas). La defensa es mirar el header Host:
// esa página llega con su propio dominio, no con "localhost".
//
// Solo se aplica a las peticiones que no traen credenciales. Con login de la UI todas las
// peticiones van autenticadas (el middleware de Auth ya rechazó las demás), y un token válido no
// lo puede tener una página ajena. Sin login, el Host debe ser local (localhost, 127.x, ::1), el
// de PUBLIC_URL o uno de TRACEREPORTS_ALLOWED_HOSTS.

// HostPolicy decides which Host headers are accepted on requests without credentials. The zero
// value checks nothing (tests); the server builds it with NewHostPolicy.
type HostPolicy struct {
	enabled bool
	any     bool
	names   map[string]bool

	warned sync.Map // hosts rechazados ya avisados en el log (uno por host)
}

// NewHostPolicy accepts the local names plus the hosts in list (comma separated, from
// TRACEREPORTS_ALLOWED_HOSTS; a port is ignored, "*" accepts any host) and the host of publicURL.
func NewHostPolicy(list, publicURL string) *HostPolicy {
	p := &HostPolicy{enabled: true, names: map[string]bool{}}
	for _, h := range strings.Split(list, ",") {
		h = strings.TrimSpace(h)
		if h == "*" {
			p.any = true
		} else if h != "" {
			p.names[hostName(h)] = true
		}
	}
	if u, err := url.Parse(strings.TrimSpace(publicURL)); err == nil && u.Host != "" {
		p.names[hostName(u.Host)] = true
	}
	return p
}

// Names returns the configured hosts besides the local ones (for the startup log).
func (p *HostPolicy) Names() []string {
	if p == nil || p.any {
		return []string{"*"}
	}
	out := make([]string, 0, len(p.names))
	for h := range p.names {
		out = append(out, h)
	}
	return out
}

// hostName lowercases a Host value and drops its port ("Localhost:8080" -> "localhost",
// "[::1]:8080" -> "::1").
func hostName(h string) string {
	h = strings.ToLower(strings.TrimSpace(h))
	if host, _, err := net.SplitHostPort(h); err == nil {
		return host
	}
	return strings.Trim(h, "[]")
}

func (p *HostPolicy) allowed(host string) bool {
	if p == nil || !p.enabled || p.any {
		return true
	}
	h := hostName(host)
	if h == "localhost" || strings.HasSuffix(h, ".localhost") {
		return true
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		return true
	}
	return p.names[h]
}

// hostGuard rejects requests without credentials whose Host is not allowed (DNS rebinding).
func (s *Server) hostGuard(next http.Handler) http.Handler {
	uiAuth := s.Auth.UIUser != "" && s.Auth.UIPass != ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if uiAuth || s.Hosts.allowed(r.Host) || s.Auth.tokenOK(r) {
			next.ServeHTTP(w, r)
			return
		}
		h := hostName(r.Host)
		if _, seen := s.Hosts.warned.LoadOrStore(h, true); !seen {
			slog.Warn("request rejected: Host not allowed (add it to TRACEREPORTS_ALLOWED_HOSTS or configure a UI login)", "host", h)
		}
		writeError(w, http.StatusForbidden, "host "+h+" is not allowed: add it to TRACEREPORTS_ALLOWED_HOSTS, or configure TRACEREPORTS_UI_USER/TRACEREPORTS_UI_PASSWORD")
	})
}
