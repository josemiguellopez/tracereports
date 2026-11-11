package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Historial abierto desde un cuadro viejo: sin ?newer solo llega hasta esa ejecución (lo que usa la
// IA, sin cambios); con ?newer también trae las posteriores, de la más nueva a la más vieja, para
// poder volver hacia adelante.
func TestHistoryNewerBringsLaterRuns(t *testing.T) {
	srv, _ := newTestServer(t)
	var tests []int64
	for i := 0; i < 4; i++ {
		run, _ := srv.Store.CreateRun(fmt.Sprintf("suite %d", i), "")
		id, _ := srv.Store.CreateTest(run, "Login", "", "")
		srv.Store.FinishTest(id, "PASS", "", "")
		tests = append(tests, id)
	}
	history := func(path string) []db.HistoryEntry {
		rec := do(t, srv, http.MethodGet, path, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", path, rec.Code)
		}
		var h []db.HistoryEntry
		json.Unmarshal(rec.Body.Bytes(), &h)
		return h
	}
	ids := func(h []db.HistoryEntry) (out []int64) {
		for _, e := range h {
			out = append(out, e.TestID)
		}
		return out
	}
	second := tests[1]
	if got := ids(history(fmt.Sprintf("/api/v1/tests/%d/history", second))); fmt.Sprint(got) != fmt.Sprint([]int64{tests[1], tests[0]}) {
		t.Fatalf("without newer: %v", got)
	}
	want := []int64{tests[3], tests[2], tests[1], tests[0]}
	if got := ids(history(fmt.Sprintf("/api/v1/tests/%d/history?newer=20", second))); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("with newer: %v, want %v", got, want)
	}
	if got := ids(history(fmt.Sprintf("/api/v1/tests/%d/history?newer=1", second))); fmt.Sprint(got) != fmt.Sprint([]int64{tests[2], tests[1], tests[0]}) {
		t.Fatalf("newer=1 brings the next one only: %v", got)
	}
}
