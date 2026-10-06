package tracereports

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Recording without a server: when the run cannot be created (no server, unreachable, wrong
// token) the client writes the same API calls it would have made into a folder instead of
// losing the evidence. Then `tracereports report <dir>` builds a static HTML report and
// `tracereports push <dir>` uploads it later. Same format as the Python and JavaScript clients.

const offlineMarker = "tracereports-offline.json"

// recorder writes API calls to <dir>/events-<pid>-<tag>.jsonl and answers with local ids.
type recorder struct {
	dir    string
	mu     sync.Mutex
	file   *os.File
	seq    int64
	nextID int64
	tag    string
	pid    int64
}

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func newRecorder(dir string) (*recorder, error) {
	if err := os.MkdirAll(filepath.Join(dir, "bodies"), 0o755); err != nil {
		return nil, err
	}
	marker, err := os.OpenFile(filepath.Join(dir, offlineMarker), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err == nil { // el primer proceso lo crea
		_ = json.NewEncoder(marker).Encode(map[string]any{"format": "tracereports-offline", "version": 1, "id": randomHex(16)})
		marker.Close()
	} else if !errors.Is(err, os.ErrExist) {
		return nil, err
	}
	r := &recorder{dir: dir, tag: randomHex(4), pid: int64(os.Getpid())}
	r.file, err = os.OpenFile(filepath.Join(dir, fmt.Sprintf("events-%d-%s.jsonl", r.pid, r.tag)), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return r, nil
}

// record writes one call and returns the JSON the server would have answered (local ids).
func (r *recorder) record(method, path string, body []byte, contentType string) ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seq++
	event := map[string]any{"seq": r.seq, "ts": time.Now().UnixMilli(), "method": method, "path": path, "content_type": contentType}
	if strings.HasPrefix(contentType, "application/json") && json.Valid(body) {
		event["body"] = json.RawMessage(body)
	} else {
		name := fmt.Sprintf("bodies/%d-%s-%d.bin", r.pid, r.tag, r.seq)
		if err := os.WriteFile(filepath.Join(r.dir, filepath.FromSlash(name)), body, 0o644); err != nil {
			return nil, err
		}
		event["body_file"] = name
	}
	var out []byte
	switch {
	case method == "POST" && path == "/api/v1/runs":
		id := r.localID()
		event["local_id"] = id
		out = []byte(fmt.Sprintf(`{"run_id":%d}`, id))
	case method == "POST" && strings.HasPrefix(path, "/api/v1/runs/") && strings.HasSuffix(path, "/tests"):
		id := r.localID()
		event["local_id"] = id
		out = []byte(fmt.Sprintf(`{"test_id":%d}`, id))
	}
	line, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}
	if _, err := r.file.Write(append(line, '\n')); err != nil { // sin buffer: si el proceso muere, lo escrito queda
		return nil, err
	}
	return out, nil
}

// localID is negative and unique among the processes recording into the same folder.
func (r *recorder) localID() int64 {
	r.nextID++
	return -((r.pid%1_000_000)*100_000 + r.nextID)
}

func (r *recorder) close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.file.Close()
}

// Recording reports whether the evidence is recorded locally instead of sent (no server).
func (c *Client) Recording() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.rec != nil
}

// RecordingDir is the folder being recorded into ("" when sending to a server).
func (c *Client) RecordingDir() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.rec == nil {
		return ""
	}
	return c.rec.dir
}

// offlineMode is "auto" (default), "always" or "off" (Client.Offline or $TRACEREPORTS_OFFLINE).
func (c *Client) offlineMode() string {
	m := strings.ToLower(strings.TrimSpace(c.Offline))
	switch m {
	case "1", "true", "yes", "on":
		return "always"
	case "0", "false", "no":
		return "off"
	case "always", "off":
		return m
	}
	return "auto"
}

// startRecording switches the client to recording; reason "" logs nothing (asked for).
func (c *Client) startRecording(reason string) bool {
	dir := c.OfflineDir
	if dir == "" {
		dir = filepath.Join("tracereports-offline", time.Now().Format("20060102-150405")+"-"+randomHex(3))
	}
	rec, err := newRecorder(dir)
	if err != nil {
		log.Printf("tracereports: could not record locally in %s: %v", dir, err)
		return false
	}
	c.mu.Lock()
	c.rec = rec
	c.mu.Unlock()
	if reason != "" {
		log.Printf("tracereports: %s; recording the evidence in %s (report: `tracereports report %s`; upload later: `tracereports push %s`)", reason, dir, dir, dir)
	}
	return true
}

// finishRecording closes the recording and, with the tracereports binary installed
// ($TRACEREPORTS_BIN or in the PATH), builds the static report into <dir>/report.
func (c *Client) finishRecording() {
	c.mu.Lock()
	rec := c.rec
	c.mu.Unlock()
	if rec == nil {
		return
	}
	rec.close()
	bin := getenv("BIN")
	if bin == "" {
		bin, _ = exec.LookPath("tracereports")
	}
	if v := strings.ToLower(getenv("OFFLINE_REPORT")); bin == "" || v == "0" || v == "false" || v == "no" {
		log.Printf("tracereports: evidence recorded in %s. Report without a server: `tracereports report %s -o report`; upload it: `tracereports push %s`", rec.dir, rec.dir, rec.dir)
		return
	}
	out := filepath.Join(rec.dir, "report")
	if msg, err := exec.Command(bin, "report", "-o", out, rec.dir).CombinedOutput(); err != nil {
		log.Printf("tracereports: could not build the report (%v: %s); run `tracereports report %s`", err, strings.TrimSpace(string(msg)), rec.dir)
		return
	}
	c.OfflineReport = filepath.Join(out, "index.html")
	log.Printf("tracereports: no server; static report in %s", c.OfflineReport)
}
