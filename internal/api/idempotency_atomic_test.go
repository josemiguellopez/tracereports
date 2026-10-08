package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func logCount(t *testing.T, srv *Server, testID int64) int {
	t.Helper()
	got, err := srv.Store.GetTest(testID)
	if err != nil {
		t.Fatal(err)
	}
	return len(got.Logs)
}

// Muchos reintentos a la vez con la misma clave: se aplica una sola vez y todos reciben la misma
// respuesta.
func TestIdempotentConcurrentRetriesApplyOnce(t *testing.T) {
	srv, testID := newTestServer(t)
	path := fmt.Sprintf("/api/v1/tests/%d/logs", testID)
	var wg sync.WaitGroup
	bodies := make([]string, 12)
	for i := range bodies {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := call(t, srv, "POST", path, `{"status":"INFO","message":"una vez"}`, header("Idempotency-Key", "same-key"))
			bodies[i] = fmt.Sprint(rec.Code, " ", rec.Body.String())
		}(i)
	}
	wg.Wait()
	if n := logCount(t, srv, testID); n != 1 {
		t.Fatalf("applied %d times", n)
	}
	for _, b := range bodies {
		if b != bodies[0] || !strings.HasPrefix(b, "201 ") {
			t.Fatalf("every retry gets the same response: %q vs %q", b, bodies[0])
		}
	}
}

// La respuesta se guarda en la misma transacción que la escritura: tras "reiniciar" (sin el
// respaldo en memoria) el reintento la encuentra y no escribe de nuevo.
func TestIdempotentResponseSurvivesRestartWithoutMemory(t *testing.T) {
	srv, testID := newTestServer(t)
	path := fmt.Sprintf("/api/v1/tests/%d/logs", testID)
	first := call(t, srv, "POST", path, `{"message":"paso"}`, header("Idempotency-Key", "k-restart"))
	recent = recentResponses{} // otro proceso: nada en memoria
	again := call(t, srv, "POST", path, `{"message":"paso"}`, header("Idempotency-Key", "k-restart"))
	if again.Header().Get("Idempotent-Replayed") != "true" || again.Body.String() != first.Body.String() || logCount(t, srv, testID) != 1 {
		t.Fatalf("replay after restart: %s / %s (logs=%d)", first.Body, again.Body, logCount(t, srv, testID))
	}
}

// Si no se puede guardar la respuesta, la escritura no queda aplicada: el cliente recibe un 5xx
// y su reintento la aplica una sola vez (antes quedaba aplicada sin respuesta guardada).
func TestIdempotentFailureBetweenStagesDoesNotHalfApply(t *testing.T) {
	srv, testID := newTestServer(t)
	path := fmt.Sprintf("/api/v1/tests/%d/logs", testID)
	breakIdempotencyTable(t, srv, true)
	rec := call(t, srv, "POST", path, `{"message":"paso"}`, header("Idempotency-Key", "k-fail"))
	if rec.Code < 500 || logCount(t, srv, testID) != 0 {
		t.Fatalf("must fail without writing: %d logs=%d", rec.Code, logCount(t, srv, testID))
	}
	breakIdempotencyTable(t, srv, false)
	for i := 0; i < 2; i++ {
		call(t, srv, "POST", path, `{"message":"paso"}`, header("Idempotency-Key", "k-fail"))
	}
	if n := logCount(t, srv, testID); n != 1 {
		t.Fatalf("after the failure the retries apply it once: %d", n)
	}
}

// Las escrituras que crean entidades devuelven el mismo id al reintentar.
func TestIdempotentCreateReturnsTheSameIDs(t *testing.T) {
	srv, _ := newTestServer(t)
	var a, b map[string]int64
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`, header("Idempotency-Key", "run-1")).Body.Bytes(), &a)
	json.Unmarshal(call(t, srv, "POST", "/api/v1/runs", `{"name":"x"}`, header("Idempotency-Key", "run-1")).Body.Bytes(), &b)
	if a["run_id"] == 0 || a["run_id"] != b["run_id"] {
		t.Fatalf("same run: %v %v", a, b)
	}
	tp := fmt.Sprintf("/api/v1/runs/%d/tests", a["run_id"])
	json.Unmarshal(call(t, srv, "POST", tp, `{"name":"t"}`, header("Idempotency-Key", "test-1")).Body.Bytes(), &a)
	json.Unmarshal(call(t, srv, "POST", tp, `{"name":"t"}`, header("Idempotency-Key", "test-1")).Body.Bytes(), &b)
	if a["test_id"] == 0 || a["test_id"] != b["test_id"] {
		t.Fatalf("same test: %v %v", a, b)
	}
}

func TestRecentResponsesBudget(t *testing.T) {
	var r recentResponses
	big := make([]byte, maxRecentBytes/3)
	for i := 0; i < 4; i++ {
		if !r.put(fmt.Sprint("k", i), storedResponse{200, big}) {
			t.Fatal("fits")
		}
	}
	if _, ok := r.get("k0"); ok {
		t.Fatal("the oldest is evicted past the byte budget")
	}
	if v, ok := r.get("k3"); !ok || len(v.body) != len(big) {
		t.Fatal("kept whole")
	}
	if r.bytes > maxRecentBytes || r.order.Len() != len(r.byKey) {
		t.Fatalf("accounting: %d bytes, %d/%d entries", r.bytes, r.order.Len(), len(r.byKey))
	}
	if r.put("huge", storedResponse{200, make([]byte, maxRecentBytes+1)}) {
		t.Fatal("larger than the budget: not kept (never truncated)")
	}
	if _, ok := r.get("huge"); ok {
		t.Fatal("not stored")
	}
	// reemplazar una clave no duplica su tamaño
	before := r.bytes
	r.put("k3", storedResponse{201, big})
	if r.bytes != before {
		t.Fatalf("replace keeps the accounting: %d vs %d", r.bytes, before)
	}
}

// breakIdempotencyTable renombra la tabla (on) o la restaura, desde otra conexión al mismo
// archivo temporal del test: simula que guardar la respuesta falla.
func breakIdempotencyTable(t *testing.T, srv *Server, on bool) {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(srv.ScreenshotsDir, "t.db"))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	q := `ALTER TABLE idempotency RENAME TO idempotency_off`
	if !on {
		q = `ALTER TABLE idempotency_off RENAME TO idempotency`
	}
	if _, err := conn.Exec(q); err != nil {
		t.Fatal(err)
	}
}
