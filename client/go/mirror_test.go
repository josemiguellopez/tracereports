package tracereports

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func mirrorServer(t *testing.T) (*httptest.Server, *atomic.Bool, *atomic.Int64) {
	t.Helper()
	fail, calls := &atomic.Bool{}, &atomic.Int64{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail.Load() {
			w.WriteHeader(503)
			return
		}
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/runs":
			fmt.Fprint(w, `{"run_id":7}`)
		case strings.HasSuffix(r.URL.Path, "/tests"):
			fmt.Fprint(w, `{"test_id":7}`)
		default:
			fmt.Fprint(w, `{}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, fail, calls
}

type testMirrorMarker struct {
	Mirror *struct {
		Complete bool   `json:"complete"`
		Server   string `json:"server"`
		Runs     []struct {
			Local  int64 `json:"local"`
			Server int64 `json:"server"`
		} `json:"runs"`
	} `json:"mirror"`
	RawRemoved bool `json:"raw_removed"`
}

func readMirrorMarker(t *testing.T, dir string) testMirrorMarker {
	t.Helper()
	var m testMirrorMarker
	raw, err := os.ReadFile(filepath.Join(dir, "tracereports-offline.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func mirrorClient(t *testing.T, url string) *Client {
	t.Helper()
	for _, key := range []string{"RUN_ID", "DISABLED", "OFFLINE_KEEP"} {
		t.Setenv("TRACEREPORTS_"+key, "")
	}
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "1")
	c := New(url)
	c.Offline = "both"
	c.OfflineDir = t.TempDir()
	c.HTTP.Timeout = 100 * time.Millisecond
	return c
}
func TestMirrorKeepRecordsAndSends(t *testing.T) {
	srv, _, calls := mirrorServer(t)
	c := mirrorClient(t, srv.URL)
	t.Setenv("TRACEREPORTS_OFFLINE_KEEP", "1")
	runSuite(t, c)
	ev := readEvents(t, c.OfflineDir)
	if len(ev) != 7 || calls.Load() != 7 {
		t.Fatalf("events=%d calls=%d", len(ev), calls.Load())
	}
	if ev[0].LocalID >= 0 || ev[1].LocalID >= 0 || ev[0].LocalID == ev[1].LocalID {
		t.Fatal("local ids")
	}
	if ev[1].Path != fmt.Sprintf("/api/v1/runs/%d/tests", ev[0].LocalID) || ev[2].Path != fmt.Sprintf("/api/v1/tests/%d/logs", ev[1].LocalID) {
		t.Fatal("ids not mapped")
	}
	m := readMirrorMarker(t, c.OfflineDir)
	if m.Mirror == nil || !m.Mirror.Complete || m.Mirror.Runs[0].Local != ev[0].LocalID {
		t.Fatalf("marker: %+v", m)
	}
	if c.ReportURL() != srv.URL+"/#run=7&view=dashboard" {
		t.Fatal(c.ReportURL())
	}
	if _, err := os.Stat(c.OfflineReport); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(c.OfflineDir, "report", "data.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "TestAdmin") || strings.Contains(string(raw), "SECRET") {
		t.Fatal("report missing test or exposing secret")
	}
}
func TestMirrorCleansOnlyAfterHTML(t *testing.T) {
	srv, _, _ := mirrorServer(t)
	c := mirrorClient(t, srv.URL)
	runSuite(t, c)
	entries, err := os.ReadDir(c.OfflineDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || !readMirrorMarker(t, c.OfflineDir).RawRemoved {
		t.Fatalf("raw not removed: %v", entries)
	}
	c.FinishRun()
	if !readMirrorMarker(t, c.OfflineDir).Mirror.Complete {
		t.Fatal("second close changed delivery status")
	}
}
func TestMirrorOutageKeepsNewTests(t *testing.T) {
	srv, fail, _ := mirrorServer(t)
	c := mirrorClient(t, srv.URL)
	c.StartRun("outage", "")
	fail.Store(true)
	tt, err := c.StartTest("after outage", "", "")
	if err != nil || tt == nil || tt.ID >= 0 {
		t.Fatalf("local test: %v %v", tt, err)
	}
	tt.Info("local step")
	tt.Finish(Pass, "", "")
	c.FinishRun()
	if len(readEvents(t, c.OfflineDir)) != 5 || readMirrorMarker(t, c.OfflineDir).Mirror.Complete {
		t.Fatal("incomplete recording")
	}
	if _, err = os.Stat(c.OfflineReport); err != nil {
		t.Fatal(err)
	}
}
func TestMirrorInitialFailureUsesFallback(t *testing.T) {
	srv, _ := unauthorized(t)
	c := mirrorClient(t, srv.URL)
	runSuite(t, c)
	if c.RunID >= 0 || readMirrorMarker(t, c.OfflineDir).Mirror != nil || len(readEvents(t, c.OfflineDir)) != 7 {
		t.Fatal("fallback")
	}
}
func TestMirrorMissingBinaryRetainsRaw(t *testing.T) {
	srv, _, _ := mirrorServer(t)
	c := mirrorClient(t, srv.URL)
	t.Setenv("TRACEREPORTS_BIN", filepath.Join(t.TempDir(), "missing"))
	runSuite(t, c)
	if c.OfflineReport != "" || len(readEvents(t, c.OfflineDir)) != 7 || !readMirrorMarker(t, c.OfflineDir).Mirror.Complete {
		t.Fatal("no binary")
	}
}
func TestMirrorDiskFailureDoesNotStopDelivery(t *testing.T) {
	srv, _, calls := mirrorServer(t)
	c := mirrorClient(t, srv.URL)
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	c.StartRun("disk", "")
	bodies := filepath.Join(c.OfflineDir, "bodies")
	if err := os.Remove(bodies); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bodies, []byte("blocked"), 0o644); err != nil {
		t.Fatal(err)
	}
	tt, _ := c.StartTest("sent", "", "")
	tt.Screenshot(png, "", Info)
	tt.Info("still sent")
	tt.Finish(Pass, "", "")
	c.FinishRun()
	if calls.Load() != 6 || readMirrorMarker(t, c.OfflineDir).Mirror.Complete {
		t.Fatal("disk error affected delivery")
	}
}
func TestMirrorWorkerWithoutClosePreventsCleanup(t *testing.T) {
	srv, _, _ := mirrorServer(t)
	owner := mirrorClient(t, srv.URL)
	owner.StartRun("shared", "")
	t.Setenv("TRACEREPORTS_RUN_ID", "7")
	worker := New(srv.URL)
	worker.Offline = "both"
	worker.OfflineDir = owner.OfflineDir
	worker.StartRun("joined", "")
	tt, _ := worker.StartTest("worker", "", "")
	tt.Finish(Pass, "", "")
	owner.FinishRun()
	if readMirrorMarker(t, owner.OfflineDir).Mirror.Complete || len(readEvents(t, owner.OfflineDir)) != 4 {
		t.Fatal("active worker cleaned")
	}
	worker.FinishRun()
}

func TestMirrorMissingMappingNeverRecordsRemoteIDs(t *testing.T) {
	srv, _, _ := mirrorServer(t)
	c := mirrorClient(t, srv.URL)
	t.Setenv("TRACEREPORTS_RUN_ID", "7")
	c.StartRun("joined", "")
	tt, _ := c.StartTest("unmapped", "", "")
	tt.Info("step")
	tt.Finish(Pass, "", "")
	c.FinishRun()
	if readMirrorMarker(t, c.OfflineDir).Mirror.Complete {
		t.Fatal("missing mapping declared complete")
	}
	for _, e := range readEvents(t, c.OfflineDir) {
		if !strings.Contains(e.Path, "/-") {
			t.Fatalf("remote path could modify an existing run on push: %s", e.Path)
		}
	}
}
