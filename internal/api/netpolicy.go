package api

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
)

// Allowlist por IP de origen (TRACEREPORTS_ALLOWED_CIDRS). Una organización que solo acepta su red
// interna lo impone en la aplicación, además del firewall: toda petición cuya IP de origen no esté
// en la lista recibe 403, también con un token válido, en la UI, la API y /screenshots/*. Este
// equipo (loopback) siempre entra.
//
// La IP de origen es la de la conexión. X-Forwarded-For y X-Real-IP solo se creen cuando la
// conexión viene de un proxy de TRACEREPORTS_TRUSTED_PROXIES; entonces el origen es el último salto
// que no es un proxy confiable. Un cliente que manda su propio X-Forwarded-For no evade el filtro.

// NetPolicy filters requests by source IP. The zero value (or nil) lets everything through.
type NetPolicy struct {
	allowed []netip.Prefix
	trusted []netip.Prefix

	warned  sync.Map     // IPs rechazadas ya avisadas en el log (una vez cada una)
	nwarned atomic.Int64 // cuántas: el mapa no crece sin límite
}

// maxWarned caps the distinct IPs remembered for the rejection log.
const maxWarned = 1000

// ParseCIDRs reads a comma separated list of CIDRs or single IPs (a single IP is a /32 or /128).
func ParseCIDRs(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, raw := range strings.Split(s, ",") {
		v := strings.TrimSpace(raw)
		if v == "" {
			continue
		}
		if strings.Contains(v, "/") {
			p, err := netip.ParsePrefix(v)
			if err != nil {
				return nil, fmt.Errorf("%q is not a CIDR (e.g. 10.0.0.0/8 or fd00::/8)", v)
			}
			out = append(out, p.Masked())
			continue
		}
		a, err := netip.ParseAddr(strings.Trim(v, "[]"))
		if err != nil {
			return nil, fmt.Errorf("%q is not an IP or a CIDR", v)
		}
		a = a.Unmap()
		out = append(out, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// NewNetPolicy builds the policy from TRACEREPORTS_ALLOWED_CIDRS (empty = every IP) and
// TRACEREPORTS_TRUSTED_PROXIES. An invalid value is an error: the server must not start with a
// filter it did not understand.
func NewNetPolicy(allowed, trusted string) (*NetPolicy, error) {
	a, err := ParseCIDRs(allowed)
	if err != nil {
		return nil, fmt.Errorf("TRACEREPORTS_ALLOWED_CIDRS: %w", err)
	}
	t, err := ParseCIDRs(trusted)
	if err != nil {
		return nil, fmt.Errorf("TRACEREPORTS_TRUSTED_PROXIES: %w", err)
	}
	return &NetPolicy{allowed: a, trusted: t}, nil
}

// Enabled reports whether requests are filtered by source IP.
func (p *NetPolicy) Enabled() bool { return p != nil && len(p.allowed) > 0 }

// Allowed and Trusted return the configured lists (for the startup log and doctor).
func (p *NetPolicy) Allowed() []netip.Prefix { return p.allowed }
func (p *NetPolicy) Trusted() []netip.Prefix { return p.trusted }

func contains(list []netip.Prefix, a netip.Addr) bool {
	for _, p := range list {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func parseIP(s string) netip.Addr {
	s = strings.TrimSpace(s)
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}

// ClientIP is the source IP of a request that arrived from peer (RemoteAddr of the connection): the
// peer itself, unless it is a trusted proxy; then the last hop of X-Forwarded-For (or X-Real-IP)
// that is not a trusted proxy. An unreadable hop gives an invalid address (it is rejected).
func (p *NetPolicy) ClientIP(peer string, h http.Header) netip.Addr {
	ip := parseIP(peer)
	if !ip.IsValid() || p == nil || !contains(p.trusted, ip) {
		return ip
	}
	var hops []string
	for _, v := range h.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(v, ",")...)
	}
	if len(hops) == 0 {
		if v := strings.TrimSpace(h.Get("X-Real-IP")); v != "" {
			hops = []string{v}
		}
	}
	// de derecha a izquierda: el primero que no es un proxy confiable es el cliente
	for i := len(hops) - 1; i >= 0; i-- {
		hop := parseIP(hops[i])
		if !hop.IsValid() {
			return netip.Addr{}
		}
		if !contains(p.trusted, hop) {
			return hop
		}
		ip = hop
	}
	return ip // toda la cadena son proxies confiables
}

// permits reports whether a source IP may enter: loopback always, otherwise it must be listed.
func (p *NetPolicy) permits(ip netip.Addr) bool {
	if !p.Enabled() {
		return true
	}
	return ip.IsValid() && (ip.IsLoopback() || contains(p.allowed, ip))
}

// middleware rejects with 403 the requests whose source IP is not allowed, before any credential
// is looked at. It must run with the real connection address (peerAddr), before RealIP rewrites it.
func (p *NetPolicy) middleware(next http.Handler) http.Handler {
	if !p.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := p.ClientIP(r.RemoteAddr, r.Header)
		if !p.permits(ip) {
			p.warn(ip, r.RemoteAddr)
			writeError(w, http.StatusForbidden, "your network is not allowed to reach this server (TRACEREPORTS_ALLOWED_CIDRS)")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (p *NetPolicy) warn(ip netip.Addr, peer string) {
	key := ip.String()
	if p.nwarned.Load() >= maxWarned {
		return
	}
	if _, seen := p.warned.LoadOrStore(key, true); seen {
		return
	}
	p.nwarned.Add(1)
	slog.Warn("request refused: source IP not in TRACEREPORTS_ALLOWED_CIDRS (logged once per IP)", "ip", key, "peer", peer)
}
