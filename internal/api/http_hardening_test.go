package api

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/josemiguellopez/tracereports/internal/live"
)

// La UI no se puede enmarcar desde otro sitio y solo corre scripts propios (los inline, por hash).
func TestSecurityHeadersAndCSP(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	inline := `try{document.body.dataset.theme="trace"}catch(e){}`
	srv.Web = fstest.MapFS{"index.html": {Data: []byte(`<html><script src="app.js"></script><script>` + inline + `</script></html>`)}, "app.js": {}}
	rec := call(t, srv, "GET", "/", "")
	csp := rec.Header().Get("Content-Security-Policy")
	sum := sha256.Sum256([]byte(inline))
	for _, want := range []string{"script-src 'self' 'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'", "frame-ancestors 'none'",
		"object-src 'none'", "connect-src 'self'"} {
		if !strings.Contains(csp, want) {
			t.Errorf("CSP sin %q: %s", want, csp)
		}
	}
	if strings.Contains(csp, "unsafe-eval") || strings.Contains(csp, "script-src 'self' 'unsafe-inline'") {
		t.Fatalf("CSP débil: %s", csp)
	}
	for k, v := range map[string]string{"X-Frame-Options": "DENY", "X-Content-Type-Options": "nosniff", "Referrer-Policy": "same-origin"} {
		if rec.Header().Get(k) != v {
			t.Errorf("%s = %q", k, rec.Header().Get(k))
		}
	}
	// la API también lleva los headers
	if call(t, srv, "GET", "/api/v1/runs", "").Header().Get("X-Frame-Options") != "DENY" {
		t.Error("API sin X-Frame-Options")
	}
}

// El index.html real: cada <script> inline tiene su hash en la CSP (si alguien agrega uno, sigue andando).
func TestCSPCoversRealIndex(t *testing.T) {
	csp := contentSecurityPolicy(fstest.MapFS{"index.html": {Data: []byte("<script>a()</script>\n<script>b()</script>")}})
	if strings.Count(csp, "'sha256-") != 2 {
		t.Fatal(csp)
	}
}

// Una página de otro sitio no puede crear ejecuciones en el servidor local (CSRF), ni con form
// ni con fetch; los clientes de tests (sin Origin) y la propia UI sí.
func TestCrossSiteWritesRejected(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	body := `{"name":"falsa"}`
	for name, h := range map[string]map[string]string{
		"Origin de otro sitio":   {"Origin": "https://evil.example"},
		"Origin null":            {"Origin": "null"},
		"Sec-Fetch-Site":         {"Sec-Fetch-Site": "cross-site"},
		"otro puerto del equipo": {"Origin": "http://localhost:9999"},
	} {
		var opts []reqOpt
		for k, v := range h {
			opts = append(opts, header(k, v))
		}
		if rec := call(t, srv, "POST", "/api/v1/runs", body, opts...); rec.Code != http.StatusForbidden {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// form multipart desde otro sitio a la subida de capturas
	if rec := call(t, srv, "POST", "/api/v1/tests/1/screenshot", "x", header("Origin", "https://evil.example"),
		header("Content-Type", "multipart/form-data; boundary=x")); rec.Code != http.StatusForbidden {
		t.Errorf("multipart: %d", rec.Code)
	}
	if rec := call(t, srv, "POST", "/api/v1/runs", body); rec.Code != http.StatusCreated {
		t.Fatalf("cliente de tests: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, srv, "POST", "/api/v1/runs", body, header("Origin", "http://example.com"), header("Sec-Fetch-Site", "same-origin")); rec.Code != http.StatusCreated {
		t.Fatalf("la UI del mismo origen: %d %s", rec.Code, rec.Body)
	}
}

// Diez intentos fallidos bloquean la IP 15 minutos, incluso si después acierta; otra IP no se ve
// afectada, y un acierto antes del límite reinicia la cuenta.
func TestLoginBruteForceThrottle(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Auth = Auth{UIUser: "qa", UIPass: "clave-correcta"}
	h := srv.Router()
	try := func(ip, pass string) int {
		req := httptest.NewRequest("GET", "/api/v1/runs", nil)
		req.RemoteAddr = ip + ":40000"
		req.SetBasicAuth("qa", pass)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	for i := 0; i < 9; i++ {
		if c := try("10.0.0.5", "mala"); c != 401 {
			t.Fatalf("intento %d: %d", i, c)
		}
	}
	if try("10.0.0.5", "clave-correcta") != 200 {
		t.Fatal("acierto antes del límite")
	}
	for i := 0; i < 10; i++ {
		try("10.0.0.5", "mala")
	}
	if c := try("10.0.0.5", "clave-correcta"); c != http.StatusTooManyRequests {
		t.Fatalf("bloqueada debe dar 429 aunque acierte: %d", c)
	}
	if c := try("10.0.0.6", "clave-correcta"); c != 200 {
		t.Fatalf("otra IP: %d", c)
	}
	// un token equivocado cuenta igual
	srv.Auth = Auth{Token: "tok"}
	h = srv.Router()
	for i := 0; i < 10; i++ {
		req := httptest.NewRequest("POST", "/api/v1/runs", strings.NewReader("{}"))
		req.RemoteAddr = "10.0.0.7:1"
		req.Header.Set("Authorization", "Bearer adivinando")
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
	req := httptest.NewRequest("POST", "/api/v1/runs", strings.NewReader(`{"name":"x"}`))
	req.RemoteAddr = "10.0.0.7:1"
	req.Header.Set("Authorization", "Bearer tok")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("token: %d", rec.Code)
	}
}

func TestAuthFailuresWindowExpires(t *testing.T) {
	a := newAuthFailures()
	now := time.Now()
	a.now = func() time.Time { return now }
	ip := netip.MustParseAddr("192.0.2.1")
	for i := 0; i < authMaxFailures; i++ {
		a.fail(ip)
	}
	if a.blocked(ip) == 0 {
		t.Fatal("debe estar bloqueada")
	}
	now = now.Add(authWindow + time.Second)
	if a.blocked(ip) != 0 {
		t.Fatal("la ventana venció")
	}
}

// El stream en vivo no se corta por el ReadTimeout del servidor (que protege las subidas lentas):
// cuida que los timeouts de cmd/main.go no rompan el SSE.
func TestLiveStreamSurvivesReadTimeout(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Live = live.NewHub()
	ts := httptest.NewUnstartedServer(srv.Router())
	ts.Config.ReadTimeout = 300 * time.Millisecond
	ts.Start()
	defer ts.Close()

	conn, err := net.Dial("tcp", ts.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("GET /api/v1/stream HTTP/1.1\r\nHost: localhost\r\n\r\n"))
	br := bufio.NewReader(conn)
	time.Sleep(900 * time.Millisecond) // pasó el ReadTimeout
	srv.Live.Publish(live.Event{Type: "run", RunID: 1})
	conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("el stream se cortó: %v", err)
		}
		if strings.HasPrefix(line, "event: run") {
			return
		}
	}
}

func TestLiveSubscriberLimit(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Live = live.NewHub()
	srv.Live.Max = 1
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()
	first, err := http.Get(ts.URL + "/api/v1/stream")
	if err != nil || first.StatusCode != 200 {
		t.Fatal(err)
	}
	defer first.Body.Close()
	second, err := http.Get(ts.URL + "/api/v1/stream")
	if err != nil {
		t.Fatal(err)
	}
	second.Body.Close()
	if second.StatusCode != http.StatusServiceUnavailable || second.Header.Get("Retry-After") == "" {
		t.Fatalf("segundo suscriptor: %d", second.StatusCode)
	}
}
