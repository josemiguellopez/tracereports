package api

import (
	"encoding/json"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// El cliente informa al cerrar el run cómo llegó su evidencia (creado tarde, reintentos): queda en el
// run (para el aviso del reporte) y en Mantenimiento > Conexiones; un cierre sin problemas no guarda nada.
func TestRunDeliveryReportedOnFinish(t *testing.T) {
	srv, runID := newTestServer(t)
	body := `{"interrupted":false,"delivery":{"late":true,"late_at":1700,"reason":"the server did not answer within 30 s",
		"detail":"connected to 10.0.0.25:8080 in 3 ms, no answer in 10 s","retried":-5,"client":"python 0.5.2","host":"qa-laptop"}}`
	if rec := call(t, srv, "PATCH", "/api/v1/runs/"+itoa(runID)+"/finish", body); rec.Code != 200 {
		t.Fatalf("finish: %d %s", rec.Code, rec.Body)
	}
	var d struct {
		Delivery map[string]any `json:"delivery"`
	}
	rec := call(t, srv, "GET", "/api/v1/runs/"+itoa(runID), "")
	json.Unmarshal(rec.Body.Bytes(), &d)
	if d.Delivery["late"] != true || d.Delivery["reason"] != "the server did not answer within 30 s" || d.Delivery["host"] != "qa-laptop" {
		t.Fatalf("delivery in the run: %v", d.Delivery)
	}
	if _, neg := d.Delivery["retried"]; neg {
		t.Fatalf("a negative count is not stored: %v", d.Delivery)
	}
	if d.Delivery["reported_at"] == nil {
		t.Fatal("server time of the report")
	}
	// otro run que llegó bien: nada guardado ni listado
	other, _ := srv.Store.CreateRun("bien", "")
	call(t, srv, "PATCH", "/api/v1/runs/"+itoa(other)+"/finish", `{"delivery":{"client":"python 0.5.2"}}`)
	var c struct {
		Days int `json:"days"`
		Runs []struct {
			ID       int64          `json:"id"`
			Delivery map[string]any `json:"delivery"`
		} `json:"runs"`
	}
	rec = call(t, srv, "GET", "/api/v1/maintenance/connections", "")
	if rec.Code != 200 {
		t.Fatalf("connections: %d", rec.Code)
	}
	json.Unmarshal(rec.Body.Bytes(), &c)
	if c.Days != 7 || len(c.Runs) != 1 || c.Runs[0].ID != runID || c.Runs[0].Delivery["detail"] == nil {
		t.Fatalf("only the run with problems: %s", rec.Body)
	}
	// texto largo: se corta
	long := strings.Repeat("x", 900)
	call(t, srv, "PATCH", "/api/v1/runs/"+itoa(other)+"/finish", `{"delivery":{"late":true,"detail":"`+long+`"}}`)
	got, _ := srv.Store.RunDeliveryOf(other)
	if got == nil || len(got.Detail) > 300 {
		t.Fatalf("detail bounded: %d", len(got.Detail))
	}
}

// Los pedidos lentos se ven en Mantenimiento (el más reciente primero), y las direcciones con
// credenciales rechazadas solo las ve quien administra.
func TestConnectionsSlowAndAuth(t *testing.T) {
	srv, _ := newTestServer(t)
	rememberSlow(slowEntry{At: 1, Method: "POST", Path: "/api/v1/runs", Status: 201, Ms: 12000})
	rememberSlow(slowEntry{At: 2, Method: "GET", Path: "/api/v1/live", Status: 200, Ms: 2500})
	srv.authFails = newAuthFailures()
	ip := netip.MustParseAddr("10.0.0.9")
	for range authMaxFailures {
		srv.authFails.fail(ip)
	}
	var c struct {
		Slow []slowEntry   `json:"slow"`
		Auth []authAddress `json:"auth"`
	}
	rec := call(t, srv, "GET", "/api/v1/maintenance/connections", "")
	json.Unmarshal(rec.Body.Bytes(), &c)
	if len(c.Slow) < 2 || c.Slow[0].Path != "/api/v1/live" || c.Slow[1].Ms != 12000 {
		t.Fatalf("slow, newest first: %+v", c.Slow)
	}
	if len(c.Auth) != 1 || c.Auth[0].IP != "10.0.0.9" || c.Auth[0].Failures != authMaxFailures || c.Auth[0].BlockedMs <= 0 || c.Auth[0].BlockedMs > authWindow.Milliseconds() {
		t.Fatalf("auth for the admin: %+v", c.Auth)
	}
	srv.SettingsLocked = true // sin permiso de administrar: no ve direcciones
	rec = call(t, srv, "GET", "/api/v1/maintenance/connections", "")
	if strings.Contains(rec.Body.String(), `"auth"`) || strings.Contains(rec.Body.String(), "10.0.0.9") {
		t.Fatalf("addresses shown without admin access: %s", rec.Body)
	}
	for range slowKeep + 5 {
		rememberSlow(slowEntry{At: time.Now().UnixMilli(), Path: "/x"})
	}
	if n := len(recentSlow()); n != slowKeep {
		t.Fatalf("keeps the last %d, got %d", slowKeep, n)
	}
}
