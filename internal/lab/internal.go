package lab

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"syscall"
	"time"
)

// Hosts internos en un servidor compartido. Los hosts de un reenvío vienen de los datos que sube
// quien tiene el token de ingesta: una llamada "plantada" podría apuntar a un sistema de la red
// interna y un administrador reenviarla sin notarlo. Un host interno (IP privada o de este equipo)
// que no está en TRACEREPORTS_LAB_HOSTS necesita que la persona lo confirme (o, en modo estricto,
// no se llama). Sin confirmar, la conexión también se corta al conectar, así un nombre que resuelve
// distinto entre la revisión y el envío (DNS rebinding) no se salta el aviso.

// Guard decides what a send may do with internal hosts. A nil Guard (the developer's own machine,
// the CLI) changes nothing.
type Guard struct {
	// Trusted is TRACEREPORTS_LAB_HOSTS: internal hosts the operator authorized.
	Trusted []string
	// Confirmed: the person saw the internal hosts and chose to send anyway.
	Confirmed bool
	// Strict (TRACEREPORTS_LAB_STRICT): an internal host not in Trusted is never called.
	Strict bool
	// Lookup resolves a name (tests); nil uses the system resolver.
	Lookup func(ctx context.Context, host string) ([]net.IPAddr, error)
}

// ErrInternal: the send would reach internal hosts not in TRACEREPORTS_LAB_HOSTS. Without Strict,
// sending it again with Confirmed goes ahead.
type ErrInternal struct {
	Hosts  []string
	Strict bool
}

func (e *ErrInternal) Error() string {
	what := "confirm to send it"
	if e.Strict {
		what = "add it to TRACEREPORTS_LAB_HOSTS (TRACEREPORTS_LAB_STRICT is on)"
	}
	return fmt.Sprintf("%s is on the internal network and not in TRACEREPORTS_LAB_HOSTS: %s", strings.Join(e.Hosts, ", "), what)
}

// errInternalDial is the connection cut at dial time (a name that resolved to an internal address
// only when connecting).
var errInternalDial = errors.New("internal address not confirmed")

// Internal reports whether ip is on the internal network: private (RFC 1918, ULA) or this machine.
func Internal(ip net.IP) bool {
	return ip.IsPrivate() || ip.IsLoopback() || ip.IsUnspecified()
}

// internalHosts lists the hosts of urls that resolve to an internal address and are not trusted.
func (g *Guard) internalHosts(ctx context.Context, urls []string) []string {
	lookup := g.Lookup
	if lookup == nil {
		lookup = net.DefaultResolver.LookupIPAddr
	}
	seen := map[string]bool{}
	var out []string
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" || seen[u.Host] || Listed(g.Trusted, u.Host) {
			continue
		}
		seen[u.Host] = true
		name := u.Hostname()
		var ips []net.IP
		if ip := net.ParseIP(name); ip != nil {
			ips = []net.IP{ip}
		} else {
			lctx, cancel := context.WithTimeout(ctx, 3*time.Second)
			addrs, err := lookup(lctx, name)
			cancel()
			if err != nil {
				continue // no resuelve: la conexión fallará igual (y el transporte la revisa al conectar)
			}
			for _, a := range addrs {
				ips = append(ips, a.IP)
			}
		}
		for _, ip := range ips {
			if Internal(ip) {
				out = append(out, strings.ToLower(u.Host))
				break
			}
		}
	}
	sort.Strings(out)
	return out
}

// guardedTransport is safeTransport that also refuses internal addresses for the hosts not in
// trusted, checked on the address actually dialed.
func guardedTransport(trusted []string) *http.Transport {
	tr := safeTransport()
	open := tr.DialContext
	strict := &net.Dialer{Timeout: 10 * time.Second, Control: func(_, address string, _ syscall.RawConn) error {
		host, _, err := net.SplitHostPort(address)
		if err != nil {
			return err
		}
		if ip := net.ParseIP(host); ip != nil && (Internal(ip) || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
			return fmt.Errorf("%w: %s", errInternalDial, ip)
		}
		return nil
	}}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		if Listed(trusted, addr) {
			return open(ctx, network, addr)
		}
		return strict.DialContext(ctx, network, addr)
	}
	return tr
}
