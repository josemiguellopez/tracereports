package api

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseCIDRs(t *testing.T) {
	got, err := ParseCIDRs(" 10.0.0.0/8, 172.16.5.4/16 ,, 192.168.1.10, fd00::/8, ::1, [2001:db8::5] ")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"10.0.0.0/8", "172.16.0.0/16", "192.168.1.10/32", "fd00::/8", "::1/128", "2001:db8::5/128"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("%d: %s, quería %s", i, got[i], want[i])
		}
	}
	if l, err := ParseCIDRs("  "); err != nil || len(l) != 0 {
		t.Fatalf("vacía: %v %v", l, err)
	}
	for _, bad := range []string{"10.0.0.0/33", "10.0.0", "intranet", "10.0.0.0/8,nope", "fd00::/129"} {
		if _, err := ParseCIDRs(bad); err == nil {
			t.Errorf("%q debe ser un error", bad)
		}
	}
	if _, err := NewNetPolicy("10.0.0.0/8", "proxy.local"); err == nil || !strings.Contains(err.Error(), "TRUSTED_PROXIES") {
		t.Fatalf("proxy inválido: %v", err)
	}
}

func TestClientIP(t *testing.T) {
	p, err := NewNetPolicy("10.0.0.0/8", "127.0.0.1, 10.9.9.9")
	if err != nil {
		t.Fatal(err)
	}
	h := func(kv ...string) http.Header {
		out := http.Header{}
		for i := 0; i < len(kv); i += 2 {
			out.Add(kv[i], kv[i+1])
		}
		return out
	}
	for _, c := range []struct {
		name, peer string
		h          http.Header
		want       string
	}{
		{"sin proxy: la conexión", "203.0.113.7:5000", h(), "203.0.113.7"},
		{"cliente no confiable que miente en XFF: se ignora", "203.0.113.7:5000", h("X-Forwarded-For", "10.0.0.1"), "203.0.113.7"},
		{"cliente no confiable que miente en X-Real-IP", "203.0.113.7:5000", h("X-Real-IP", "10.0.0.1"), "203.0.113.7"},
		{"proxy confiable: el último salto no confiable", "127.0.0.1:5000", h("X-Forwarded-For", "10.0.0.1, 203.0.113.9"), "203.0.113.9"},
		{"dos proxies confiables", "127.0.0.1:5000", h("X-Forwarded-For", "10.4.4.4, 10.9.9.9"), "10.4.4.4"},
		{"XFF en varios headers", "127.0.0.1:5000", h("X-Forwarded-For", "10.4.4.4", "X-Forwarded-For", "10.9.9.9"), "10.4.4.4"},
		{"proxy confiable con X-Real-IP", "127.0.0.1:5000", h("X-Real-IP", "10.2.2.2"), "10.2.2.2"},
		{"proxy confiable sin headers: el proxy", "127.0.0.1:5000", h(), "127.0.0.1"},
		{"salto ilegible: inválida", "127.0.0.1:5000", h("X-Forwarded-For", "evil, 10.9.9.9"), "invalid IP"},
		{"IPv6", "[2001:db8::7]:443", h(), "2001:db8::7"},
		{"IPv4 mapeada en IPv6", "[::ffff:10.1.1.1]:80", h(), "10.1.1.1"},
	} {
		if got := p.ClientIP(c.peer, c.h); got.String() != c.want {
			t.Errorf("%s: %s, quería %s", c.name, got, c.want)
		}
	}
}

func cidrServer(t *testing.T, allowed, trusted string) (*Server, int64) {
	t.Helper()
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	p, err := NewNetPolicy(allowed, trusted)
	if err != nil {
		t.Fatal(err)
	}
	srv.Net = p
	srv.Auth = Auth{Token: "tok"}
	os.WriteFile(filepath.Join(srv.ScreenshotsDir, "x.png"), []byte("\x89PNG\r\n\x1a\n"), 0o600)
	return srv, testID
}

func cidrCall(srv *Server, method, path, peer string, header ...string) int {
	req := httptest.NewRequest(method, path, strings.NewReader(`{"name":"r"}`))
	req.RemoteAddr = peer
	req.Host = "localhost"
	req.Header.Set("Authorization", "Bearer tok")
	req.Header.Set("Content-Type", "application/json")
	for i := 0; i+1 < len(header); i += 2 {
		req.Header.Set(header[i], header[i+1])
	}
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, req)
	return rec.Code
}

// Fuera de la lista → 403 en UI, API y capturas, también con un token válido; dentro, normal.
func TestCIDRsBlockOutsideEvenWithAToken(t *testing.T) {
	srv, _ := cidrServer(t, "10.0.0.0/8, fd00::/8", "")
	for _, path := range []string{"/", "/api/v1/runs", "/screenshots/x.png"} {
		if code := cidrCall(srv, "GET", path, "203.0.113.7:4000"); code != 403 {
			t.Errorf("GET %s desde afuera: %d", path, code)
		}
		if code := cidrCall(srv, "GET", path, "10.1.2.3:4000"); code == 403 {
			t.Errorf("GET %s desde la red interna: %d", path, code)
		}
		if code := cidrCall(srv, "GET", path, "[fd00::5]:4000"); code == 403 {
			t.Errorf("GET %s desde IPv6 interna: %d", path, code)
		}
	}
	if code := cidrCall(srv, "POST", "/api/v1/runs", "198.51.100.1:4000"); code != 403 {
		t.Fatalf("escritura con token desde afuera: %d", code)
	}
	// este equipo siempre entra
	for _, peer := range []string{"127.0.0.1:4000", "[::1]:4000"} {
		if code := cidrCall(srv, "GET", "/api/v1/runs", peer); code != 200 {
			t.Errorf("loopback %s: %d", peer, code)
		}
	}
}

// Un cliente que no es un proxy confiable no evade el filtro mintiendo en X-Forwarded-For.
func TestCIDRsCannotBeSpoofed(t *testing.T) {
	srv, _ := cidrServer(t, "10.0.0.0/8", "")
	for _, hdr := range [][]string{{"X-Forwarded-For", "10.0.0.1"}, {"X-Real-IP", "10.0.0.1"}, {"Forwarded", "for=10.0.0.1"}} {
		if code := cidrCall(srv, "GET", "/api/v1/runs", "203.0.113.7:4000", hdr...); code != 403 {
			t.Errorf("%s falsificado: %d", hdr[0], code)
		}
	}
	// detrás de un proxy confiable se mira el cliente real que informa el proxy
	srv, _ = cidrServer(t, "10.0.0.0/8", "192.0.2.10")
	if code := cidrCall(srv, "GET", "/api/v1/runs", "192.0.2.10:4000", "X-Forwarded-For", "203.0.113.7"); code != 403 {
		t.Fatalf("cliente externo detrás del proxy: %d", code)
	}
	if code := cidrCall(srv, "GET", "/api/v1/runs", "192.0.2.10:4000", "X-Forwarded-For", "10.3.3.3"); code != 200 {
		t.Fatalf("cliente interno detrás del proxy: %d", code)
	}
	// el proxy mismo, sin decir a quién atiende, no está en la lista
	if code := cidrCall(srv, "GET", "/api/v1/runs", "192.0.2.10:4000"); code != 403 {
		t.Fatalf("proxy sin XFF: %d", code)
	}
}

func TestCIDRRejectionIsLoggedOncePerIP(t *testing.T) {
	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
	srv, _ := cidrServer(t, "10.0.0.0/8", "")
	for i := 0; i < 5; i++ {
		cidrCall(srv, "GET", "/api/v1/runs", "203.0.113.7:4000")
	}
	cidrCall(srv, "GET", "/api/v1/runs", "203.0.113.8:4000")
	if n := strings.Count(buf.String(), "ip=203.0.113.7"); n != 1 {
		t.Fatalf("%d avisos para la misma IP:\n%s", n, buf.String())
	}
	if !strings.Contains(buf.String(), "ip=203.0.113.8") {
		t.Fatal("otra IP sí se avisa")
	}
}

// Sin TRACEREPORTS_ALLOWED_CIDRS no se filtra nada (comportamiento anterior).
func TestNoCIDRsNoFilter(t *testing.T) {
	srv, _ := cidrServer(t, "", "")
	if srv.Net.Enabled() {
		t.Fatal("vacía = sin filtro")
	}
	if code := cidrCall(srv, "GET", "/api/v1/runs", "203.0.113.7:4000"); code != 200 {
		t.Fatalf("%d", code)
	}
	var nilPolicy *NetPolicy
	if !nilPolicy.permits(netip.MustParseAddr("8.8.8.8")) {
		t.Fatal("nil no filtra")
	}
}

func FuzzParseCIDRs(f *testing.F) {
	for _, s := range []string{"10.0.0.0/8", "::1,fd00::/8", "1.2.3.4/", "[::1]", ",,,", "10.0.0.0/8, x", "\x00"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		list, err := ParseCIDRs(s)
		if err == nil {
			for _, p := range list {
				if !p.IsValid() {
					t.Fatalf("prefijo inválido aceptado: %q", s)
				}
			}
		}
	})
}

func FuzzClientIP(f *testing.F) {
	p, _ := NewNetPolicy("10.0.0.0/8", "127.0.0.1")
	for _, s := range []string{"10.0.0.1", "a, b, c", "[::1]:80", "", ", ,", "10.0.0.1:99999"} {
		f.Add("127.0.0.1:1", s)
		f.Add("203.0.113.1:1", s)
	}
	f.Fuzz(func(t *testing.T, peer, xff string) {
		h := http.Header{}
		h.Set("X-Forwarded-For", xff)
		ip := p.ClientIP(peer, h)
		// sin un proxy confiable, nunca sale una IP distinta de la conexión
		if peerIP := parseIP(peer); peerIP.IsValid() && !contains(p.trusted, peerIP) && ip != peerIP {
			t.Fatalf("%q/%q: %s", peer, xff, ip)
		}
	})
}
