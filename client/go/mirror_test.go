package tracereports

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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

// needBinary skips a test that builds the HTML report when the tracereports binary is missing
// ($TRACEREPORTS_BIN or PATH), like TestEndToEndReportWithTheBinary.
func needBinary(t *testing.T) {
	t.Helper()
	bin := os.Getenv("TRACEREPORTS_BIN")
	if bin == "" { bin = os.Getenv("TRACEREPORTS_TEST_BIN") }
	if bin == "" {
		if p, err := exec.LookPath("tracereports"); err == nil {
			bin = p
		}
	}
	if bin == "" {
		t.Skip("needs the tracereports binary ($TRACEREPORTS_BIN or PATH)")
	}
	t.Setenv("TRACEREPORTS_BIN", bin)
}

func TestMirrorKeepRecordsAndSends(t *testing.T) {
	needBinary(t)
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
	needBinary(t)
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
	needBinary(t)
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

// An orphaned lock (older than staleLock, left by a process that died) is removed; a recent one
// belongs to another process and is reported clearly.
func TestMirrorOrphanedLockDoesNotBlockTheCopy(t *testing.T) {
	old, fresh := t.TempDir(), t.TempDir()
	for _, d := range []string{old, fresh} {
		if err := os.Mkdir(filepath.Join(d, ".mirror-lock"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	past := time.Now().Add(-2 * time.Minute)
	if err := os.Chtimes(filepath.Join(old, ".mirror-lock"), past, past); err != nil {
		t.Fatal(err)
	}
	m, err := newMirror(old, "http://server.test", 7, map[string]any{"name": "run"})
	if err != nil {
		t.Fatalf("orphaned lock: %v", err)
	}
	m.rec.close()
	if m.runs[7] >= 0 {
		t.Fatalf("local run id: %v", m.runs)
	}
	if _, err := os.Stat(filepath.Join(old, ".mirror-lock")); !os.IsNotExist(err) {
		t.Fatal("the orphaned lock is removed")
	}
	if _, err := newMirror(fresh, "http://server.test", 7, map[string]any{"name": "run"}); err == nil || !strings.Contains(err.Error(), "held by another process") {
		t.Fatalf("recent lock: %v", err)
	}
}

// With OfflineBase each run creates its own folder inside the base: the second one does not clash
// with the copy of the first (with a fixed folder, the second one had no copy).
func TestSuccessiveRunsKeepTheirOwnCopyInsideTheBase(t *testing.T) {
	srv, _, _ := mirrorServer(t)
	base := filepath.Join(t.TempDir(), "output", "tracereports")
	t.Setenv("TRACEREPORTS_OFFLINE_BASE", base)
	var dirs []string
	for i := 0; i < 2; i++ {
		c := mirrorClient(t, srv.URL)
		c.OfflineDir, c.OfflineBase = "", base
		runSuite(t, c)
		if !c.Recording() {
			t.Fatalf("run %d kept no local copy", i)
		}
		dirs = append(dirs, c.RecordingDir())
	}
	if dirs[0] == dirs[1] || filepath.Dir(dirs[0]) != base || filepath.Dir(dirs[1]) != base {
		t.Fatalf("one folder per run inside the base: %v", dirs)
	}
	if c := New(srv.URL); c.OfflineBase != base {
		t.Fatalf("OfflineBase from $TRACEREPORTS_OFFLINE_BASE: %q", c.OfflineBase)
	}
}
