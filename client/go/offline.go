package tracereports

import (
	"crypto/rand"
	"encoding/binary"
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

// Local ids: each recorder reserves blocks of idBlock ids by creating <dir>/ids/<block>
// exclusively, so two recorders sharing the folder (same process, another process or session)
// never repeat an id. The block is random in [minBlock, maxBlock): it does not collide with the
// ids of older recordings (pid % 10^6) and -(block*idBlock + n) stays exact in JavaScript (< 2^53).
// Same scheme in the Python, JavaScript and Java clients.
const (
	idBlock  = 100_000
	minBlock = 1_000_000
	maxBlock = 90_000_000_000
)

// recorder writes API calls to <dir>/events-<pid>-<tag>.jsonl and answers with local ids.
type recorder struct {
	dir    string
	mu     sync.Mutex
	file   *os.File
	seq    int64
	nextID int64
	block  int64
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
		id, err := r.localID()
		if err != nil {
			return nil, err
		}
		event["local_id"] = id
		out = []byte(fmt.Sprintf(`{"run_id":%d}`, id))
	case method == "POST" && strings.HasPrefix(path, "/api/v1/runs/") && strings.HasSuffix(path, "/tests"):
		id, err := r.localID()
		if err != nil {
			return nil, err
		}
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

// localID is negative and unique among the recorders recording into the same folder.
func (r *recorder) localID() (int64, error) {
	if r.block == 0 || r.nextID >= idBlock-1 {
		block, err := r.reserveBlock()
		if err != nil {
			return 0, err
		}
		r.block, r.nextID = block, 0
	}
	r.nextID++
	return -(r.block*idBlock + r.nextID), nil
}

func (r *recorder) reserveBlock() (int64, error) {
	ids := filepath.Join(r.dir, "ids")
	if err := os.MkdirAll(ids, 0o755); err != nil {
		return 0, err
	}
	for range 100 {
		var b [8]byte
		_, _ = rand.Read(b[:])
		block := minBlock + int64(binary.BigEndian.Uint64(b[:])%(maxBlock-minBlock))
		f, err := os.OpenFile(filepath.Join(ids, fmt.Sprint(block)), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			f.Close()
			return block, nil
		}
		if !errors.Is(err, os.ErrExist) { // os.ErrExist: es de otro grabador, se prueba otro
			return 0, err
		}
	}
	return 0, fmt.Errorf("tracereports: could not reserve local ids in %s", ids)
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
	return c.rec != nil || c.mirror != nil
}

// RecordingDir is the folder being recorded into ("" when sending to a server).
func (c *Client) RecordingDir() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.mirror != nil {
		return c.mirror.dir
	}
	if c.rec == nil {
		return ""
	}
	return c.rec.dir
}

// offlineMode is "auto" (default), "always", "both" or "off" (Client.Offline or $TRACEREPORTS_OFFLINE).
func (c *Client) offlineMode() string {
	m := strings.ToLower(strings.TrimSpace(c.Offline))
	switch m {
	case "1", "true", "yes", "on":
		return "always"
	case "0", "false", "no":
		return "off"
	case "always", "both", "off":
		return m
	}
	return "auto"
}

// recordingDir is OfflineDir, or a new folder for this session inside OfflineBase: successive
// runs never share a folder unless asked.
func (c *Client) recordingDir() string {
	if c.OfflineDir != "" {
		return c.OfflineDir
	}
	base := c.OfflineBase
	if base == "" {
		base = "tracereports-offline"
	}
	return filepath.Join(base, time.Now().Format("20060102-150405")+"-"+randomHex(3))
}

// startRecording switches the client to recording; reason "" logs nothing (asked for).
func (c *Client) startRecording(reason string) bool {
	dir := c.recordingDir()
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
	if c.mirror != nil {
		rec = c.mirror.rec
	}
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
	log.Printf("tracereports: local report in %s", c.OfflineReport)
}
