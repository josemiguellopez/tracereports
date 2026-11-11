package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// Una corrida dice a qué ejecución pertenece (execution_id) y cuántos tests planificó, también en la
// lista: sin eso, la interfaz no distinguía una corrida nueva de la misma ejecución (un lanzador que
// abre una por archivo) de "otra ejecución" y avisaba con cada archivo.
func TestRunsCarryTheirExecution(t *testing.T) {
	srv, _ := newTestServer(t)
	rec := do(t, srv, http.MethodPost, "/api/v1/runs", `{"name":"test_cart.py","environment":"qa","execution_id":"build-42","planned":6}`)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		t.Fatalf("create run: %d %s", rec.Code, rec.Body.String())
	}
	var created struct {
		RunID int64 `json:"run_id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &created)
	var one struct {
		ExecutionID string `json:"execution_id"`
		Planned     int    `json:"planned"`
	}
	json.Unmarshal(do(t, srv, http.MethodGet, fmt.Sprintf("/api/v1/runs/%d", created.RunID), "").Body.Bytes(), &one)
	if one.ExecutionID != "build-42" || one.Planned != 6 {
		t.Fatalf("GET /runs/{id}: %+v", one)
	}
	var list []struct {
		ID          int64  `json:"id"`
		ExecutionID string `json:"execution_id"`
		Planned     int    `json:"planned"`
	}
	body := do(t, srv, http.MethodGet, "/api/v1/runs", "").Body.Bytes()
	if err := json.Unmarshal(body, &list); err != nil {
		var wrapped struct {
			Runs []struct {
				ID          int64  `json:"id"`
				ExecutionID string `json:"execution_id"`
				Planned     int    `json:"planned"`
			} `json:"runs"`
		}
		if err := json.Unmarshal(body, &wrapped); err != nil {
			t.Fatal(err)
		}
		for _, r := range wrapped.Runs {
			list = append(list, struct {
				ID          int64  `json:"id"`
				ExecutionID string `json:"execution_id"`
				Planned     int    `json:"planned"`
			}{r.ID, r.ExecutionID, r.Planned})
		}
	}
	for _, r := range list {
		if r.ID == created.RunID {
			if r.ExecutionID != "build-42" || r.Planned != 6 {
				t.Fatalf("GET /runs: %+v", r)
			}
			return
		}
	}
	t.Fatalf("run %d not in the list", created.RunID)
}
