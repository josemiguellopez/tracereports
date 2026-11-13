package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

// muchas ejecuciones terminadas con evidencia, para que borrarlas deje páginas libres en la base
func seedFinishedRuns(t *testing.T, srv *Server, n int) []int64 {
	t.Helper()
	var ids []int64
	big := strings.Repeat("evidencia ", 2000)
	for range n {
		id, _ := srv.Store.CreateRun("viejo", "")
		tid, _ := srv.Store.CreateTest(id, "t", "", "")
		for range 5 {
			srv.Store.AddLog(tid, "INFO", big, 0, "")
		}
		srv.Store.FinishTest(tid, "PASS", "", "")
		srv.Store.FinishRun(id)
		ids = append(ids, id)
	}
	return ids
}

// Borrar en lote ya no compacta la base (en una base grande tarda minutos con la escritura tomada): lo
// deja pendiente, y Almacenamiento dice cuánto se recuperaría.
func TestDeletingLeavesTheCompactionForLater(t *testing.T) {
	srv, _ := newTestServer(t)
	ids := seedFinishedRuns(t, srv, 30)
	raw, _ := json.Marshal(map[string]any{"run_ids": ids})
	if rec := call(t, srv, http.MethodPost, "/api/v1/ui/runs/delete", string(raw)); rec.Code != 200 {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if rb, _ := srv.Store.ReclaimableBytes(); rb == 0 {
		t.Fatal("the delete compacted the database right away")
	}
	var st struct {
		Compact struct {
			Pending          bool  `json:"pending"`
			ReclaimableBytes int64 `json:"reclaimable_bytes"`
			NeedBytes        int64 `json:"need_bytes"`
		} `json:"compact"`
	}
	json.Unmarshal(call(t, srv, http.MethodGet, "/api/v1/maintenance/storage", "").Body.Bytes(), &st)
	if !st.Compact.Pending || st.Compact.ReclaimableBytes == 0 || st.Compact.NeedBytes == 0 {
		t.Fatalf("storage: %+v", st.Compact)
	}
	// compactar a mano: devuelve el espacio, lo anota y ya no queda pendiente
	rec := call(t, srv, http.MethodPost, "/api/v1/ui/maintenance/compact", "{}")
	var run CompactRun
	json.Unmarshal(rec.Body.Bytes(), &run)
	if rec.Code != 200 || run.FreedBytes <= 0 || run.Trigger != "manual" {
		t.Fatalf("compact: %d %s", rec.Code, rec.Body)
	}
	if pending, last := srv.compactInfo(); pending || last == nil || last.FreedBytes != run.FreedBytes {
		t.Fatalf("after compacting: pending=%v last=%+v", pending, last)
	}
	if rb, _ := srv.Store.ReclaimableBytes(); rb != 0 {
		t.Fatalf("still %d bytes to reclaim", rb)
	}
}

// Sin lugar en el disco para la copia que hace VACUUM no se empieza: lo dice con cuánto hace falta.
func TestCompactNeedsRoomOnTheDisk(t *testing.T) {
	srv, _ := newTestServer(t)
	old := compactSpaceMargin
	compactSpaceMargin = 1e12
	defer func() { compactSpaceMargin = old }()
	rec := call(t, srv, http.MethodPost, "/api/v1/ui/maintenance/compact", "{}")
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"kind":"disk_space"`) || !strings.Contains(rec.Body.String(), "free") {
		t.Fatalf("without room: %d %s", rec.Code, rec.Body)
	}
}

// La automática espera: algo pendiente, ninguna ejecución en curso, los clientes callados un rato y no
// haber compactado hace poco.
func TestAutomaticCompactionWaitsForAQuietServer(t *testing.T) {
	srv, runID := newTestServer(t) // newTestServer deja una ejecución en curso
	now := time.Now()
	if srv.compactDue(now) {
		t.Fatal("nothing pending")
	}
	srv.markCompactPending()
	if srv.compactDue(now) {
		t.Fatal("a run is in progress")
	}
	srv.Store.FinishRun(runID)
	srv.compact.lastWrite.Store(now.Add(-time.Minute).UnixMilli())
	if srv.compactDue(now) {
		t.Fatal("a client wrote a minute ago")
	}
	srv.compact.lastWrite.Store(now.Add(-compactQuietFor - time.Minute).UnixMilli())
	if !srv.compactDue(now) {
		t.Fatal("quiet, nothing running and pending: it is due")
	}
	if _, err := srv.CompactNow("auto", ""); err != nil {
		t.Fatal(err)
	}
	srv.markCompactPending()
	if srv.compactDue(now.Add(time.Hour)) {
		t.Fatal("compacted less than compactMinGap ago")
	}
	if !srv.compactDue(now.Add(compactMinGap + time.Hour)) {
		t.Fatal("after the gap it is due again")
	}
}

// Una escritura de un cliente anota la hora (lo que la automática espera).
func TestClientWritesAreNoted(t *testing.T) {
	srv, _ := newTestServer(t)
	if rec := call(t, srv, http.MethodPost, "/api/v1/runs", `{"name":"x"}`); rec.Code != http.StatusCreated {
		t.Fatalf("%d", rec.Code)
	}
	if lw := srv.compact.lastWrite.Load(); time.Since(time.UnixMilli(lw)) > 5*time.Second {
		t.Fatal("the write was not noted")
	}
}
