package tracereports

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
)

type event struct {
	Seq         int64           `json:"seq"`
	TS          int64           `json:"ts"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	ContentType string          `json:"content_type"`
	Body        json.RawMessage `json:"body"`
	BodyFile    string          `json:"body_file"`
	LocalID     int64           `json:"local_id"`
}

func readEvents(t *testing.T, dir string) []event {
	t.Helper()
	files, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	var out []event
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		sc := bufio.NewScanner(fh)
		for sc.Scan() {
			var e event
			if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
				t.Fatal(err)
			}
			out = append(out, e)
		}
		fh.Close()
	}
	return out
}

// unauthorized answers 401 to everything (a wrong token) and counts the calls.
func unauthorized(t *testing.T) (*httptest.Server, *atomic.Int64) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Error(w, `{"error":"invalid token"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

var png = []byte("\x89PNG\r\n\x1a\nfake")

func runSuite(t *testing.T, c *Client) {
	t.Helper()
	c.Project, c.Branch, c.Commit = "shop", "dev", "abc"
	c.StartRun("Checkout", "qa")
	tt, _ := c.StartTestWithKey("login/TestAdmin", "TestAdmin", "smoke", "")
	tt.Info("abrir login")
	tt.Screenshot(png, "pantalla", Fail)
	tt.Network([]Conn{{Method: "POST", URL: "https://api/login", Status: 500, RequestHeaders: map[string]string{"Authorization": "Bearer SECRET"}}})
	tt.Finish(Fail, "boom", "")
	c.FinishRun()
}

func TestWrongTokenRecordsInsteadOfLosingEverything(t *testing.T) {
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	srv, _ := unauthorized(t)
	dir := filepath.Join(t.TempDir(), "rec")
	c := New(srv.URL)
	c.Token, c.OfflineDir = "wrong", dir
	runSuite(t, c)
	if !c.Recording() || c.RecordingDir() != dir || c.RunID >= 0 {
		t.Fatalf("recording: %v %q run=%d", c.Recording(), c.RecordingDir(), c.RunID)
	}
	evs := readEvents(t, dir)
	run, test := evs[0].LocalID, evs[1].LocalID
	var got []string
	for _, e := range evs {
		got = append(got, e.Method+" "+e.Path)
	}
	want := []string{
		"POST /api/v1/runs",
		fmt.Sprintf("POST /api/v1/runs/%d/tests", run),
		fmt.Sprintf("POST /api/v1/tests/%d/logs", test),
		fmt.Sprintf("POST /api/v1/tests/%d/screenshot", test),
		fmt.Sprintf("POST /api/v1/tests/%d/network", test),
		fmt.Sprintf("PATCH /api/v1/tests/%d/finish", test),
		fmt.Sprintf("PATCH /api/v1/runs/%d/finish", run),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("events:\n%s", strings.Join(got, "\n"))
	}
	if run >= 0 || test >= 0 || run == test {
		t.Fatalf("local ids: %d %d", run, test)
	}
	var created map[string]string
	json.Unmarshal(evs[0].Body, &created)
	if created["name"] != "Checkout" || created["commit"] != "abc" || created["framework"] != "go" {
		t.Fatalf("run body: %v", created)
	}
	shot := evs[3]
	if !strings.HasPrefix(shot.ContentType, "multipart/form-data") || shot.BodyFile == "" {
		t.Fatalf("screenshot event: %+v", shot)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, shot.BodyFile)); !strings.Contains(string(b), string(png)) {
		t.Fatal("screenshot body not stored")
	}
	var marker map[string]any
	raw, _ := os.ReadFile(filepath.Join(dir, offlineMarker))
	if json.Unmarshal(raw, &marker) != nil || marker["format"] != "tracereports-offline" || marker["version"] != float64(1) {
		t.Fatalf("marker: %s", raw)
	}
}

func TestUnreachableServerRecordsInANewSessionFolder(t *testing.T) {
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	t.Setenv("TRACEREPORTS_OFFLINE_DIR", "")
	chdir(t, t.TempDir())
	c := New("http://127.0.0.1:1")
	runSuite(t, c)
	if !c.Recording() || filepath.Dir(c.RecordingDir()) != "tracereports-offline" {
		t.Fatalf("session folder: %q", c.RecordingDir())
	}
	if n := len(readEvents(t, c.RecordingDir())); n != 7 {
		t.Fatalf("events: %d", n)
	}
}

func TestOfflineOffAndDisabledRecordNothing(t *testing.T) {
	chdir(t, t.TempDir())
	off := New("http://127.0.0.1:1")
	off.Offline = "off"
	runSuite(t, off)
	t.Setenv("TRACEREPORTS_DISABLED", "1")
	disabled := New("http://127.0.0.1:1")
	runSuite(t, disabled)
	if off.Recording() || disabled.Recording() {
		t.Fatal("must not record")
	}
	if _, err := os.Stat("tracereports-offline"); err == nil {
		t.Fatal("no folder must be created")
	}
}

func TestOfflineAlwaysNeverContactsAServer(t *testing.T) {
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	srv, calls := unauthorized(t)
	dir := t.TempDir()
	c := New(srv.URL)
	c.Offline, c.OfflineDir = "always", dir
	runSuite(t, c)
	if calls.Load() != 0 || len(readEvents(t, dir)) != 7 {
		t.Fatalf("calls=%d events=%d", calls.Load(), len(readEvents(t, dir)))
	}
}

func TestShardJoinsANegativeRunAndSharesTheFolder(t *testing.T) {
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	dir := t.TempDir()
	owner := New("")
	owner.Offline, owner.OfflineDir = "always", dir
	runID, _ := owner.StartRun("shards", "")
	t.Setenv("TRACEREPORTS_RUN_ID", fmt.Sprint(runID))
	t.Setenv("TRACEREPORTS_OFFLINE_DIR", dir)
	shard := New("http://127.0.0.1:1")
	if id, _ := shard.StartRun("shards", ""); id != runID || !shard.Recording() || shard.RecordingDir() != dir {
		t.Fatalf("shard: id=%d recording=%v dir=%q", id, shard.Recording(), shard.RecordingDir())
	}
	shard.rec.pid++ // otro proceso
	tt, _ := shard.StartTest("a", "", "")
	tt.Finish(Pass, "", "")
	shard.FinishRun() // no es el dueño: solo cierra su archivo
	owner.FinishRun()
	files, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	if len(files) != 2 {
		t.Fatalf("one events file per process: %v", files)
	}
}

func TestEndToEndReportWithTheBinary(t *testing.T) {
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
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "")
	srv, _ := unauthorized(t)
	c := New(srv.URL)
	c.Token, c.OfflineDir = "wrong", filepath.Join(t.TempDir(), "rec")
	runSuite(t, c)
	if c.ReportURL() == "" || c.ReportURL() != c.OfflineReport {
		t.Fatalf("report: %q", c.ReportURL())
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(c.OfflineReport), "data.js"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "Checkout") || !strings.Contains(string(data), "abrir login") || strings.Contains(string(data), "SECRET") {
		t.Fatal("report content (and masking without a server)")
	}
}

// chdir is t.Chdir for go1.22 (the minimum version of this module).
func chdir(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(prev) })
}

func TestConsoleIsRecorded(t *testing.T) {
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	dir := t.TempDir()
	c := New("")
	c.Offline, c.OfflineDir = "always", dir
	c.StartRun("Consola", "")
	tt, _ := c.StartTest("t", "", "")
	if err := tt.Console([]ConsoleEntry{{Level: "error", Text: "boom", Timestamp: 1}}); err != nil {
		t.Fatal(err)
	}
	if err := tt.Console(nil); err != nil {
		t.Fatal(err)
	}
	tt.Finish(Fail, "x", "")
	c.FinishRun()
	n := 0
	for _, e := range readEvents(t, dir) {
		if strings.HasSuffix(e.Path, "/console") {
			n++
			if !strings.Contains(string(e.Body), `"level":"error"`) {
				t.Fatalf("body: %s", e.Body)
			}
		}
	}
	if n != 1 {
		t.Fatalf("console events: %d", n)
	}
}

func TestArtifactIsRecorded(t *testing.T) {
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	dir := t.TempDir()
	c := New("")
	c.Offline, c.OfflineDir = "always", dir
	c.StartRun("Artefactos", "")
	tt, _ := c.StartTest("t", "", "")
	if err := tt.Artifact("trace", []byte("PK\x03\x04zip"), "trace.zip"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Artifact("har", []byte("x"), ""); err == nil {
		t.Fatal("only trace or video")
	}
	if err := tt.Artifact("video", nil, ""); err == nil {
		t.Fatal("empty data")
	}
	var nilTest *Test
	if nilTest.Artifact("trace", []byte("PK"), "") != ErrDisabled {
		t.Fatal("nil test is safe")
	}
	tt.Finish(Fail, "x", "")
	c.FinishRun()
	var uploads []event
	for _, e := range readEvents(t, dir) {
		if strings.HasSuffix(e.Path, "/artifact") {
			uploads = append(uploads, e)
		}
	}
	if len(uploads) != 1 {
		t.Fatalf("uploads: %d", len(uploads))
	}
	b, _ := os.ReadFile(filepath.Join(dir, uploads[0].BodyFile))
	if !strings.Contains(string(b), "name=\"kind\"\r\n\r\ntrace") || !strings.Contains(string(b), "trace.zip") {
		t.Fatalf("multipart body: %q", b)
	}
}

// Two recorders sharing the folder (here in the same process: same pid) reserve different blocks
// in ids/: they never repeat a local id, ids stay exact in JavaScript and do not collide with older
// recordings.
func TestTwoRecordersInOneFolderNeverRepeatLocalIDs(t *testing.T) {
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	dir := filepath.Join(t.TempDir(), "rec")
	a, b := New(""), New("")
	a.Offline, a.OfflineDir = "always", dir
	b.Offline, b.OfflineDir = "always", dir
	ra, _ := a.StartRun("suite A", "")
	rb, _ := b.StartRun("suite B", "")
	ta, _ := a.StartTest("test A", "", "")
	tb, _ := b.StartTest("test B", "", "")
	b.rec.nextID = 100_000 - 1 // B's block runs out: it reserves another
	tb2, _ := b.StartTest("test B2", "", "")
	ta.Finish(Pass, "", "")
	tb.Finish(Fail, "only B failed", "")
	tb2.Finish(Pass, "", "")
	a.FinishRun()
	b.FinishRun()
	ids := []int64{ra, rb}
	for _, e := range readEvents(t, dir) {
		if strings.HasSuffix(e.Path, "/tests") {
			ids = append(ids, e.LocalID)
		}
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if seen[id] || id >= -1e11 || id <= -(1<<53) {
			t.Fatalf("local ids must be unique, exact in JS and new: %v", ids)
		}
		seen[id] = true
	}
	if len(ids) != 5 {
		t.Fatalf("ids: %v", ids)
	}
	if blocks, _ := os.ReadDir(filepath.Join(dir, "ids")); len(blocks) != 3 {
		t.Fatalf("reserved blocks: %d", len(blocks))
	}
	bin := os.Getenv("TRACEREPORTS_BIN")
	if bin == "" { bin = os.Getenv("TRACEREPORTS_TEST_BIN") }
	if bin == "" {
		return
	}
	out := filepath.Join(t.TempDir(), "report")
	if msg, err := exec.Command(bin, "report", dir, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, msg)
	}
	runs, _ := filepath.Glob(filepath.Join(out, "run-*", "data.js"))
	got := map[string][]string{}
	for _, f := range runs {
		raw, _ := os.ReadFile(f)
		var d struct {
			Run struct {
				Name  string
				Tests []struct{ Name string }
			}
		}
		js := strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(string(raw), "window.TRACEREPORTS_STATIC = ")), ";")
		if err := json.Unmarshal([]byte(js), &d); err != nil {
			t.Fatal(err)
		}
		for _, tt := range d.Run.Tests {
			got[d.Run.Name] = append(got[d.Run.Name], tt.Name)
		}
		sort.Strings(got[d.Run.Name])
	}
	if fmt.Sprint(got) != fmt.Sprint(map[string][]string{"suite A": {"test A"}, "suite B": {"test B", "test B2"}}) {
		t.Fatalf("replayed tests by run: %v", got)
	}
}
