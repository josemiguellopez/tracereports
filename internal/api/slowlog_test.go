package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// Un pedido lento queda en el log con su ruta, estado y duración (sin la query): así un timeout del
// cliente sin esta línea dice que el pedido nunca llegó al servidor.
func TestSlowRequestIsLogged(t *testing.T) {
	logs := captureLogs(t)
	old := slowRequestAfter
	slowRequestAfter = 20 * time.Millisecond
	t.Cleanup(func() { slowRequestAfter = old })
	h := slowRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/runs" {
			time.Sleep(40 * time.Millisecond)
		}
		w.WriteHeader(http.StatusCreated)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("POST", "/api/v1/runs?key=secreto", nil))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/live", nil))
	out := logs()
	if !strings.Contains(out, `msg="slow request" method=POST path=/api/v1/runs status=201 ms=`) {
		t.Fatalf("the slow request is not logged: %q", out)
	}
	if strings.Contains(out, "secreto") || strings.Contains(out, "/api/v1/live") {
		t.Fatalf("logged the query or a fast request: %q", out)
	}
}

// El stream de eventos queda abierto por diseño: nunca es "lento".
func TestSlowRequestSkipsTheStream(t *testing.T) {
	logs := captureLogs(t)
	old := slowRequestAfter
	slowRequestAfter = time.Millisecond
	t.Cleanup(func() { slowRequestAfter = old })
	h := slowRequests(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { time.Sleep(10 * time.Millisecond) }))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/stream", nil))
	if strings.Contains(logs(), "slow request") {
		t.Fatal("the stream was logged as slow")
	}
}

// En el router: es el primer middleware (mide también los filtros y las credenciales).
func TestRouterLogsSlowRequests(t *testing.T) {
	src, err := os.ReadFile("api.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), "r.Use(slowRequests, peerAddr,") {
		t.Fatal("slowRequests is not the first middleware of the router")
	}
}
