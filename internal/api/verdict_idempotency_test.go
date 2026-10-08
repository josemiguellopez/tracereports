package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// rawDB abre otra conexión al archivo temporal del test (triggers y conteos directos).
func rawDB(t *testing.T, srv *Server) *sql.DB {
	t.Helper()
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(srv.ScreenshotsDir, "t.db"))+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func verdictRows(t *testing.T, conn *sql.DB) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM verdicts`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func postVerdict(t *testing.T, srv *Server, testID int64, key, verdict string) (int, int64) {
	t.Helper()
	rec := call(t, srv, "POST", fmt.Sprintf("/api/v1/ui/tests/%d/verdict", testID),
		`{"verdict":"`+verdict+`","comment":"c","author":"qa"}`, header("Idempotency-Key", key))
	var v struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(rec.Body.Bytes(), &v)
	return rec.Code, v.ID
}

func TestVerdictAndItsIdempotentResponseAreAtomic(t *testing.T) {
	srv, testID := newTestServer(t)
	conn := rawDB(t, srv)
	// solo falla guardar la respuesta idempotente
	if _, err := conn.Exec(`CREATE TRIGGER fail_idem BEFORE INSERT ON idempotency BEGIN SELECT RAISE(ABORT, 'idem off'); END`); err != nil {
		t.Fatal(err)
	}
	if code, _ := postVerdict(t, srv, testID, "v-key", "product_bug"); code < 500 {
		t.Fatalf("must fail as a whole: %d", code)
	}
	if n := verdictRows(t, conn); n != 0 {
		t.Fatalf("no partial write: %d verdicts", n)
	}
	// reinicio: sin caché en memoria, y la base vuelve a aceptar respuestas
	recent = recentResponses{}
	conn.Exec(`DROP TRIGGER fail_idem`)
	code, first := postVerdict(t, srv, testID, "v-key", "product_bug")
	if code != 201 || first == 0 {
		t.Fatalf("the retry applies it: %d", code)
	}
	recent = recentResponses{}
	code, again := postVerdict(t, srv, testID, "v-key", "product_bug")
	if code != 201 || again != first || verdictRows(t, conn) != 1 {
		t.Fatalf("a retry after losing the cache replays it: %d id=%d first=%d rows=%d", code, again, first, verdictRows(t, conn))
	}
}

func TestConcurrentVerdictRetriesAndDistinctActions(t *testing.T) {
	srv, testID := newTestServer(t)
	conn := rawDB(t, srv)
	ids := make([]int64, 10)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, ids[i] = postVerdict(t, srv, testID, "same", "flaky")
		}(i)
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] || id == 0 {
			t.Fatalf("every retry gets the same verdict: %v", ids)
		}
	}
	if n := verdictRows(t, conn); n != 1 {
		t.Fatalf("one verdict for one key: %d", n)
	}
	// dos acciones distintas (otra clave, o sin clave): historial legítimo
	_, second := postVerdict(t, srv, testID, "another", "test_bug")
	rec := call(t, srv, "POST", fmt.Sprintf("/api/v1/ui/tests/%d/verdict", testID), `{"verdict":"flaky"}`)
	if second == ids[0] || rec.Code != 201 || verdictRows(t, conn) != 3 {
		t.Fatalf("distinct actions keep their own rows: rows=%d", verdictRows(t, conn))
	}
}
