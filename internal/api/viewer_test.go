package api

import (
	"net/http"
	"strings"
	"testing"
)

// El login de solo lectura ve todo pero no cambia nada: ni resultados, ni Ajustes, ni acciones de
// la UI, tampoco desde el mismo equipo. El de administración sigue igual.
func TestViewerLoginIsReadOnly(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	srv.Auth = Auth{UIUser: "admin", UIPass: "clave-admin", ViewerUser: "equipo", ViewerPass: "clave-lectura"}
	viewer := basicAuth("equipo", "clave-lectura")
	admin := basicAuth("admin", "clave-admin")

	if rec := call(t, srv, "GET", "/api/v1/runs", "", viewer); rec.Code != 200 {
		t.Fatalf("lectura: %d", rec.Code)
	}
	if rec := call(t, srv, "GET", "/api/v1/tests/"+itoa(testID)+"/network", "", viewer); rec.Code != 200 {
		t.Fatalf("lectura de evidencia: %d", rec.Code)
	}
	for _, w := range []struct{ method, path, body string }{
		{"POST", "/api/v1/runs", `{"name":"x"}`},
		{"PATCH", "/api/v1/tests/" + itoa(testID) + "/finish", `{"status":"PASS"}`},
		{"PUT", "/api/v1/settings", `{"ai":{"provider":"off"}}`},
		{"POST", "/api/v1/ui/lab/send", `{}`},
	} {
		if rec := call(t, srv, w.method, w.path, w.body, viewer); rec.Code != http.StatusForbidden {
			t.Errorf("%s %s con solo lectura: %d %s", w.method, w.path, rec.Code, rec.Body)
		}
	}
	// Ajustes visibles, no editables, con el motivo
	rec := call(t, srv, "GET", "/api/v1/settings", "", viewer)
	if !strings.Contains(rec.Body.String(), `"editable":false`) || !strings.Contains(rec.Body.String(), `"edit_reason":"viewer"`) {
		t.Fatalf("ajustes: %s", rec.Body)
	}
	if rec := call(t, srv, "GET", "/api/v1/security/posture", "", viewer); rec.Code == 200 {
		t.Fatal("la postura de seguridad es solo para administración")
	}
	// el de administración escribe y administra
	if rec := call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`, admin); rec.Code != http.StatusCreated {
		t.Fatalf("admin: %d", rec.Code)
	}
	if rec := call(t, srv, "GET", "/api/v1/settings", "", admin); !strings.Contains(rec.Body.String(), `"editable":true`) {
		t.Fatalf("admin ajustes: %s", rec.Body)
	}
	// una clave equivocada no entra
	if rec := call(t, srv, "GET", "/api/v1/runs", "", basicAuth("equipo", "mala")); rec.Code != http.StatusUnauthorized {
		t.Fatalf("clave mala: %d", rec.Code)
	}
}

// Solo con el login de lectura (sin el de administración): las lecturas piden login, y administrar
// queda para el token.
func TestViewerLoginAlone(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "tok", ViewerUser: "equipo", ViewerPass: "clave-lectura"}
	if rec := call(t, srv, "GET", "/api/v1/runs", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("sin login: %d", rec.Code)
	}
	if rec := call(t, srv, "GET", "/api/v1/runs", "", basicAuth("equipo", "clave-lectura")); rec.Code != 200 {
		t.Fatalf("lectura: %d", rec.Code)
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"off"}}`, basicAuth("equipo", "clave-lectura")); rec.Code != http.StatusForbidden {
		t.Fatalf("ajustes: %d", rec.Code)
	}
}
