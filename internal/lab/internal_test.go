package lab

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Un host interno sin autorizar se confirma antes de enviar nada; uno autorizado o la URL base que
// eligió la persona, no. Un nombre resuelve a lo que diga el DNS (aquí, un resolvedor de prueba).
func TestGuardInternalHosts(t *testing.T) {
	ips := map[string]string{"preprod.interno": "10.1.2.3", "app.example": "93.184.216.34"}
	g := &Guard{Lookup: func(_ context.Context, host string) ([]net.IPAddr, error) {
		if ip, ok := ips[host]; ok {
			return []net.IPAddr{{IP: net.ParseIP(ip)}}, nil
		}
		return nil, errors.New("no such host")
	}}
	urls := []string{"http://preprod.interno:8080/api", "https://app.example/x", "http://127.0.0.1:9/a", "http://[::1]:9/b",
		"http://192.168.1.10/c", "http://nadie.example/d", "http://preprod.interno:8080/otra"}
	got := g.internalHosts(context.Background(), urls)
	want := []string{"127.0.0.1:9", "192.168.1.10", "[::1]:9", "preprod.interno:8080"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("internal hosts: %v, want %v", got, want)
	}
	g.Trusted = ParseHosts("*.interno, 127.0.0.1")
	if got := g.internalHosts(context.Background(), urls); strings.Join(got, ",") != "192.168.1.10,[::1]:9" {
		t.Fatalf("with TRACEREPORTS_LAB_HOSTS: %v", got)
	}
}

func TestRunAsksBeforeAnInternalHost(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(500) }))
	defer srv.Close()
	p := &Plan{Target: Request{Method: "GET", URL: srv.URL + "/api/orders"}}
	_, err := Run(context.Background(), p, Options{Guard: &Guard{}})
	var internal *ErrInternal
	if !errors.As(err, &internal) || internal.Strict || hits != 0 {
		t.Fatalf("unconfirmed internal host: %v (hits %d)", err, hits)
	}
	if res, err := Run(context.Background(), p, Options{Guard: &Guard{Confirmed: true}}); err != nil || res.Response.Status != 500 || hits != 1 {
		t.Fatalf("confirmed: %v %+v (hits %d)", err, res, hits)
	}
	if _, err := Run(context.Background(), p, Options{Guard: &Guard{Confirmed: true, Strict: true}}); !errors.As(err, &internal) || !internal.Strict || hits != 1 {
		t.Fatalf("strict: %v (hits %d)", err, hits)
	}
	// la URL base la eligió la persona: no se pregunta por ella
	far := &Plan{Target: Request{Method: "GET", URL: "https://app.example/api/orders"}}
	if res, err := Run(context.Background(), far, Options{Guard: &Guard{}, BaseURL: srv.URL}); err != nil || res.Response.Status != 500 {
		t.Fatalf("base URL chosen by the person: %v %+v", err, res)
	}
	// sin Guard (la máquina del dev, la CLI): como siempre
	if _, err := Run(context.Background(), p, Options{}); err != nil {
		t.Fatalf("no guard: %v", err)
	}
}

// Sin confirmar, el transporte también corta al conectar: un nombre que resolvió afuera en la
// revisión y adentro al conectar (DNS rebinding) no llega.
func TestGuardedTransportRefusesInternalAtDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	client := &http.Client{Transport: guardedTransport(nil)}
	if _, err := client.Get(srv.URL); err == nil || !strings.Contains(err.Error(), "internal address not confirmed") {
		t.Fatalf("internal address at dial: %v", err)
	}
	trusted := &http.Client{Transport: guardedTransport(ParseHosts("127.0.0.1"))}
	if res, err := trusted.Get(srv.URL); err != nil {
		t.Fatalf("trusted host: %v", err)
	} else {
		res.Body.Close()
	}
}
