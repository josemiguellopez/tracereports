// Package offline replays a recording made by a client without a server (no server configured,
// unreachable, or a wrong token) against a TraceReports API: an in-process one to build a static
// report (tracereports report), or a remote server later (tracereports push).
//
// A recording is a folder with:
//
//	tracereports-offline.json   {"format": "tracereports-offline", "version": 1, "id": "<unique>"}
//	events-*.jsonl              one file per process (pytest-xdist workers, shards...)
//	bodies/                     request bodies that are not JSON (screenshots)
//	ids/                        blocks of local ids reserved by each recorder (empty files)
//
// Each line of an events file is one API call the client would have made:
//
//	{"seq": 7, "ts": 1791319221317, "method": "POST", "path": "/api/v1/runs/-1/tests",
//	 "content_type": "application/json", "body": {...}, "local_id": -2}
//
// "body" is the JSON body, or "body_file" (relative to the folder) for any other body. Run and
// test ids are negative local ids: the call that creates one says which in "local_id", and later
// paths use it. "ts" is when it happened (Unix ms): it travels as X-TraceReports-Timestamp so the
// report keeps the real times. Within a file events keep their order; files are merged by ts.
package offline

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Marker is the file that identifies a recording folder.
const Marker = "tracereports-offline.json"

// Version of the recording format this package reads.
const Version = 1

// maxEvent is the largest line accepted in an events file. A line is one API call with its JSON
// body embedded (bodies that are not JSON go to body_file), so it must fit the largest JSON
// request the server accepts: a network batch of up to 48 MiB (clients send one connection alone
// up to ~40 MiB) plus 1 MiB for the event's own fields. A longer line could not be replayed
// anyway: it is reported, never cut nor skipped.
const maxEvent = 49 << 20

// Event is one recorded API call.
type Event struct {
	Seq         int64           `json:"seq"`
	TS          int64           `json:"ts"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	ContentType string          `json:"content_type"`
	Body        json.RawMessage `json:"body,omitempty"`
	BodyFile    string          `json:"body_file,omitempty"`
	LocalID     int64           `json:"local_id,omitempty"`

	file string // events file it came from (for messages and idempotency keys)
}

// Mirror identifies the server runs already receiving this local copy.
type Mirror struct {
	Server   string      `json:"server"`
	Runs     []MirrorRun `json:"runs"`
	Complete bool        `json:"complete"`
}
type MirrorRun struct {
	Local  int64 `json:"local"`
	Server int64 `json:"server"`
}

// Recording is a recording folder, read but not replayed yet.
type Recording struct {
	Mirror     *Mirror
	RawRemoved bool
	Dir        string
	ID         string
	// files are the events files, in file order. They are read in streaming (one event per file
	// in memory at a time): a recording of gigabytes does not take gigabytes to replay.
	files []string
}

// IsRecording reports whether dir looks like a recording (marker or events files).
func IsRecording(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, Marker)); err == nil {
		return true
	}
	files, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	return len(files) > 0
}

// Open reads a recording folder.
func Open(dir string) (*Recording, error) {
	rec := &Recording{Dir: dir}
	if raw, err := os.ReadFile(filepath.Join(dir, Marker)); err == nil {
		var m struct {
			Mirror     *Mirror `json:"mirror"`
			RawRemoved bool    `json:"raw_removed"`
			Format     string  `json:"format"`
			Version    int     `json:"version"`
			ID         string  `json:"id"`
		}
		if err := json.Unmarshal(raw, &m); err != nil || m.Format != "tracereports-offline" {
			return nil, fmt.Errorf("%s: not a TraceReports recording", filepath.Join(dir, Marker))
		}
		if m.Version > Version {
			return nil, fmt.Errorf("%s: recording format v%d is newer than this tracereports (v%d): update it", dir, m.Version, Version)
		}
		rec.ID = m.ID
		rec.Mirror, rec.RawRemoved = m.Mirror, m.RawRemoved
		if rec.RawRemoved {
			return nil, fmt.Errorf("%s: raw recording removed; only the HTML report remains, nothing to upload", dir)
		}
	}
	files, err := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("%s: no events-*.jsonl files: nothing was recorded", dir)
	}
	sort.Strings(files)
	if rec.ID == "" {
		rec.ID = filepath.Base(filepath.Clean(dir))
	}
	// todo se valida antes de enviar nada (un evento que no es evidencia invalida la grabaciÃ³n
	// entera), sin guardar los eventos: se vuelven a leer al reproducir
	for _, f := range files {
		if err := validateEvents(f); err != nil {
			return nil, err
		}
	}
	rec.files = files
	return rec, nil
}

// validateEvents reads a whole events file and checks every event, keeping none of them.
func validateEvents(path string) error {
	r, err := newEventReader(path)
	if err != nil {
		return err
	}
	defer r.close()
	for {
		e, err := r.next()
		if err != nil || e == nil {
			return err
		}
	}
}

// eventReader reads an events file one event at a time.
type eventReader struct {
	f      *os.File
	sc     *bufio.Scanner
	name   string
	line   int
	broken error // lÃ­nea invÃ¡lida: solo se perdona si es la Ãºltima (el proceso muriÃ³ escribiÃ©ndola)
}

func newEventReader(path string) (*eventReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), maxEvent)
	return &eventReader{f: f, sc: sc, name: filepath.Base(path)}, nil
}

func (r *eventReader) close() { r.f.Close() }

// next returns the next event of the file, nil at its end.
func (r *eventReader) next() (*Event, error) {
	for r.sc.Scan() {
		r.line++
		raw := bytes.TrimSpace(r.sc.Bytes())
		if len(raw) == 0 {
			continue
		}
		if r.broken != nil {
			return nil, r.broken
		}
		var e Event
		if err := json.Unmarshal(raw, &e); err != nil {
			r.broken = fmt.Errorf("%s:%d: invalid event: %v", r.name, r.line, err)
			continue
		}
		// solo evidencia: crear ejecuciones y tests, adjuntarles evidencia y cerrarlos (allowed.go).
		// Un evento distinto invalida la grabaciÃ³n entera: nada se envÃ­a a ninguna parte
		if !Ingest(e.Method, e.Path) {
			return nil, fmt.Errorf("%s:%d: %s %q is not an evidence event (a recording can only create runs and tests, add their evidence and finish them)",
				r.name, r.line, e.Method, e.Path)
		}
		e.file = r.name
		return &e, nil
	}
	if err := r.sc.Err(); errors.Is(err, bufio.ErrTooLong) {
		return nil, fmt.Errorf("%s:%d: event larger than %d MiB (the most the server accepts in one request): "+
			"it was not written by a TraceReports client or the file is damaged", r.name, r.line+1, maxEvent>>20)
	} else if err != nil {
		return nil, fmt.Errorf("%s: %w", r.name, err)
	}
	return nil, nil
}

// cursor yields every event in replay order: files merged by ts, each file keeping its order.
// At the same millisecond, a run must exist before a worker's tests and finish after them. Only
// the next event of each file is in memory.
type cursor struct {
	readers []*eventReader
	heads   []*Event
}

func (r *Recording) cursor() (*cursor, error) {
	c := &cursor{}
	for _, path := range r.files {
		rd, err := newEventReader(path)
		if err != nil {
			c.close()
			return nil, err
		}
		c.readers = append(c.readers, rd)
		e, err := rd.next()
		if err != nil {
			c.close()
			return nil, err
		}
		c.heads = append(c.heads, e)
	}
	return c, nil
}

// next returns the next event, nil when there are no more.
func (c *cursor) next() (*Event, error) {
	best := -1
	for i, e := range c.heads {
		if e != nil && (best < 0 || before(*e, *c.heads[best])) {
			best = i
		}
	}
	if best < 0 {
		return nil, nil
	}
	e := c.heads[best]
	h, err := c.readers[best].next()
	if err != nil {
		return nil, err
	}
	c.heads[best] = h
	return e, nil
}

func (c *cursor) close() {
	for _, rd := range c.readers {
		rd.close()
	}
}

// Events returns every event in replay order (see cursor), all in memory: for tests. Replay
// streams them.
func (r *Recording) Events() []Event {
	c, err := r.cursor()
	if err != nil {
		return nil
	}
	defer c.close()
	var out []Event
	for {
		e, err := c.next()
		if err != nil || e == nil {
			return out
		}
		out = append(out, *e)
	}
}

func before(a, b Event) bool {
	if a.TS != b.TS {
		return a.TS < b.TS
	}
	priority := func(e Event) int {
		if e.Method == "POST" && e.Path == "/api/v1/runs" {
			return -1
		}
		if e.Method == "PATCH" && strings.HasPrefix(e.Path, "/api/v1/runs/") && strings.HasSuffix(e.Path, "/finish") {
			return 1
		}
		return 0
	}
	return priority(a) < priority(b)
}

// body returns the bytes of an event body.
func (r *Recording) body(e Event) ([]byte, error) {
	if e.BodyFile == "" {
		return e.Body, nil
	}
	clean := filepath.Clean(filepath.FromSlash(e.BodyFile))
	if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
		return nil, errors.New("body_file must stay inside the recording folder")
	}
	return os.ReadFile(filepath.Join(r.Dir, clean))
}
