package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Mantenimiento: listar con filtros, cerrar y borrar en lote (solo administración) y el umbral del
// cierre automático, que la variable de entorno fija por encima de la interfaz.
func TestMaintenanceListBulkAndStaleSetting(t *testing.T) {
	clearAIEnv(t)
	t.Setenv("TRACEREPORTS_STALE_RUN_HOURS", "")
	srv, _ := newTestServer(t)
	a, _ := newRunWithTest(t, srv)
	b, _ := newRunWithTest(t, srv)

	var res struct {
		Runs  []struct{ ID int64 } `json:"runs"`
		Stale struct {
			Hours    float64
			Source   string
			Editable bool
		} `json:"stale"`
	}
	get := func(q string) {
		t.Helper()
		rec := call(t, srv, "GET", "/api/v1/maintenance"+q, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("list %s: %d %s", q, rec.Code, rec.Body)
		}
		json.Unmarshal(rec.Body.Bytes(), &res)
	}
	get("?state=running")
	if len(res.Runs) < 2 || res.Stale.Hours != 24 || res.Stale.Source != "default" || !res.Stale.Editable {
		t.Fatalf("running runs and the default threshold: %+v", res)
	}
	if rec := call(t, srv, "GET", "/api/v1/maintenance?state=nope", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("unknown state: %d", rec.Code)
	}

	srv.Auth = Auth{UIUser: "admin", UIPass: "clave-admin", ViewerUser: "equipo", ViewerPass: "clave-lectura"}
	admin, viewer := basicAuth("admin", "clave-admin"), basicAuth("equipo", "clave-lectura")
	body := fmt.Sprintf(`{"run_ids":[%d,%d,99999],"reason":"limpieza"}`, a, b)
	if rec := call(t, srv, "POST", "/api/v1/ui/runs/close", body, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only close: %d", rec.Code)
	}
	rec := call(t, srv, "POST", "/api/v1/ui/runs/close", body, admin)
	var counts map[string]int
	json.Unmarshal(rec.Body.Bytes(), &counts)
	if rec.Code != http.StatusOK || counts["closed"] != 2 || counts["skipped"] != 1 {
		t.Fatalf("bulk close: %d %s", rec.Code, rec.Body)
	}
	if r, _ := srv.Store.GetRun(a); r.Status == "RUNNING" || r.ClosedBy != "admin" || r.CloseReason != "limpieza" {
		t.Fatalf("closed by hand: %+v", r)
	}
	if rec := call(t, srv, "POST", "/api/v1/ui/runs/close", `{"run_ids":[]}`, admin); rec.Code != http.StatusBadRequest {
		t.Fatalf("no ids: %d", rec.Code)
	}

	if rec := call(t, srv, "POST", "/api/v1/ui/runs/delete", body, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only delete: %d", rec.Code)
	}
	rec = call(t, srv, "POST", "/api/v1/ui/runs/delete", body, admin)
	json.Unmarshal(rec.Body.Bytes(), &counts)
	if rec.Code != http.StatusOK || counts["deleted"] != 2 {
		t.Fatalf("bulk delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := srv.Store.GetRun(a); err == nil {
		t.Fatal("the run is deleted")
	}

	// el umbral: se guarda desde la interfaz y vale sin reiniciar; la variable de entorno manda
	if rec := call(t, srv, "PUT", "/api/v1/ui/maintenance/stale", `{"hours":1.5}`, viewer); rec.Code != http.StatusForbidden {
		t.Fatalf("read-only threshold: %d", rec.Code)
	}
	for _, bad := range []string{`{}`, `{"hours":-1}`, `{"hours":1000}`} {
		if rec := call(t, srv, "PUT", "/api/v1/ui/maintenance/stale", bad, admin); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d", bad, rec.Code)
		}
	}
	if rec := call(t, srv, "PUT", "/api/v1/ui/maintenance/stale", `{"hours":1.5}`, admin); rec.Code != http.StatusOK {
		t.Fatalf("set threshold: %d %s", rec.Code, rec.Body)
	}
	if h, src := srv.StaleRunHours(); h != 1.5 || src != "ui" {
		t.Fatalf("saved threshold: %v %s", h, src)
	}
	t.Setenv("TRACEREPORTS_STALE_RUN_HOURS", "2")
	if h, src := srv.StaleRunHours(); h != 2 || src != "env" {
		t.Fatalf("the environment wins: %v %s", h, src)
	}
	if rec := call(t, srv, "PUT", "/api/v1/ui/maintenance/stale", `{"hours":3}`, admin); rec.Code != http.StatusConflict {
		t.Fatalf("set by the environment: %d", rec.Code)
	}
}
