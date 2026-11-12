package api

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func freshStalls(t *testing.T) {
	t.Helper()
	oldAfter, oldMin := stallAfter, stallMin
	stallAfter, stallMin = 80*time.Millisecond, 2
	stalls = &stallWatch{inflight: map[*inflightReq]struct{}{}}
	t.Cleanup(func() {
		stallAfter, stallMin = oldAfter, oldMin
		stalls = &stallWatch{inflight: map[*inflightReq]struct{}{}}
	})
}

// Un caso real: una tarea tiene tomada la única conexión a la base y los POST /runs quedan
// esperando. El vigilante abre un episodio, guarda un volcado que dice qué esperaba y, al soltarse,
// lo cierra con cuánto duró y cuánto se esperó por la base.
func TestStallWatchCatchesARealDatabaseHold(t *testing.T) {
	freshStalls(t)
	srv, _ := newTestServer(t)

	hold := make(chan struct{})
	go srv.Store.Atomic(func(tx *db.Store) (*db.Idem, error) { <-hold; return nil, nil }) // toma la conexión
	time.Sleep(50 * time.Millisecond)

	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			call(t, srv, "POST", "/api/v1/runs", `{"name":"trabado"}`)
		}()
	}
	time.Sleep(200 * time.Millisecond)
	srv.checkStalls(time.Now(), srv.writeStallDump)
	open := recentStalls(true)
	if len(open) != 1 || open[0].End != 0 || open[0].MaxStuck < 2 || open[0].Paths[0] != "POST /api/v1/runs" || open[0].Dump == "" {
		t.Fatalf("open episode: %+v", open)
	}
	raw, err := os.ReadFile(filepath.Join(srv.diagnosticsDir(), open[0].Dump))
	if err != nil {
		t.Fatal(err)
	}
	dump := string(raw)
	for _, want := range []string{"requests running for more than", "POST /api/v1/runs", "database connection: open=1 in_use=1", "read-only connections: open=", "goroutine "} {
		if !strings.Contains(dump, want) {
			t.Fatalf("the dump lacks %q:\n%.600s", want, dump)
		}
	}
	close(hold) // la tarea suelta la base: los pedidos terminan
	wg.Wait()
	srv.checkStalls(time.Now(), srv.writeStallDump)
	done := recentStalls(true)
	if len(done) != 1 || done[0].End == 0 || done[0].End <= done[0].Start || done[0].DBWaitMs <= 0 {
		t.Fatalf("closed episode: %+v", done)
	}
	if recentStalls(false)[0].Dump != "" {
		t.Fatal("the dump name is only for admins")
	}
}

// Un solo pedido largo (una subida grande) no es un bloqueo.
func TestStallWatchIgnoresASingleLongRequest(t *testing.T) {
	freshStalls(t)
	srv, _ := newTestServer(t)
	done := stalls.track("POST", "/api/v1/tests/1/artifact", time.Now().Add(-time.Minute))
	defer done()
	srv.checkStalls(time.Now(), func(string) string { t.Fatal("dumped for a single request"); return "" })
	if len(recentStalls(true)) != 0 {
		t.Fatal("no episode")
	}
}

// Mantenimiento > Conexiones muestra los episodios; el volcado solo lo descarga quien administra, y
// solo por su nombre (nada de rutas).
func TestStallsInConnectionsAndDumpDownload(t *testing.T) {
	freshStalls(t)
	srv, _ := newTestServer(t)
	a := stalls.track("POST", "/api/v1/runs", time.Now().Add(-time.Second))
	b := stalls.track("GET", "/api/v1/auth/session", time.Now().Add(-time.Second))
	srv.checkStalls(time.Now(), srv.writeStallDump)
	a()
	b()
	srv.checkStalls(time.Now(), srv.writeStallDump)
	var c struct {
		Stalls []stallEpisode `json:"stalls"`
	}
	rec := call(t, srv, "GET", "/api/v1/maintenance/connections", "")
	json.Unmarshal(rec.Body.Bytes(), &c)
	if len(c.Stalls) != 1 || c.Stalls[0].Dump == "" || c.Stalls[0].MaxStuck != 2 {
		t.Fatalf("stalls for the admin: %s", rec.Body)
	}
	name := c.Stalls[0].Dump
	rec = call(t, srv, "GET", "/api/v1/maintenance/stalls/"+name, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "goroutine ") || !strings.Contains(rec.Header().Get("Content-Disposition"), name) {
		t.Fatalf("download: %d %.200s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"..%2F..%2Fsecret.txt", "stall-1.txt", "t.db"} {
		if rec := call(t, srv, "GET", "/api/v1/maintenance/stalls/"+bad, ""); rec.Code != 404 {
			t.Fatalf("%s: %d", bad, rec.Code)
		}
	}
	srv.SettingsLocked = true
	if rec := call(t, srv, "GET", "/api/v1/maintenance/stalls/"+name, ""); rec.Code != 403 {
		t.Fatalf("without admin access: %d", rec.Code)
	}
	rec = call(t, srv, "GET", "/api/v1/maintenance/connections", "")
	if strings.Contains(rec.Body.String(), name) {
		t.Fatal("the dump name shown without admin access")
	}
}

// Se guardan los últimos stallKeepDumps volcados.
func TestStallDumpsAreRotated(t *testing.T) {
	srv, _ := newTestServer(t)
	dir := srv.diagnosticsDir()
	os.MkdirAll(dir, 0o700)
	for i := range stallKeepDumps + 4 {
		os.WriteFile(filepath.Join(dir, "stall-20260101-0000"+twoDigits(i)+".txt"), []byte("x"), 0o600)
	}
	srv.writeStallDump("header\n")
	files, _ := filepath.Glob(filepath.Join(dir, "stall-*.txt"))
	if len(files) != stallKeepDumps {
		t.Fatalf("kept %d dumps", len(files))
	}
}

func twoDigits(i int) string { return string([]byte{byte('0' + i/10), byte('0' + i%10)}) }
