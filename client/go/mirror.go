package tracereports

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type mirrorRun struct {
	Local  int64 `json:"local"`
	Server int64 `json:"server"`
}
type mirrorInfo struct {
	Server   string      `json:"server"`
	Runs     []mirrorRun `json:"runs"`
	Complete bool        `json:"complete"`
}
type mirrorMarker struct {
	Format     string      `json:"format"`
	Version    int         `json:"version"`
	ID         string      `json:"id"`
	Mirror     *mirrorInfo `json:"mirror,omitempty"`
	RawRemoved bool        `json:"raw_removed,omitempty"`
}
type mirrorStatus struct {
	Complete bool   `json:"complete"`
	Reason   string `json:"reason"`
}
type mirror struct {
	mu                       sync.Mutex
	dir, status              string
	rec                      *recorder
	runs, tests              map[int64]int64
	failed, disabled, closed bool
	active                   int
}

// staleLock: a process holds the lock for milliseconds; an older one was left by a process that died.
const staleLock = 30 * time.Second

func (m *mirror) locked(fn func() error) error {
	lock := filepath.Join(m.dir, ".mirror-lock")
	for n := 0; ; n++ {
		if err := os.Mkdir(lock, 0o755); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return err
			}
			if n >= 50 {
				info, statErr := os.Stat(lock)
				if statErr == nil && time.Since(info.ModTime()) <= staleLock {
					return fmt.Errorf("%s is held by another process (remove it if no run is using this folder)", lock)
				}
				_ = os.Remove(lock) // orphaned: removed and tried again
				n = 0
			}
			time.Sleep(10 * time.Millisecond)
		} else {
			break
		}
	}
	defer os.Remove(lock)
	return fn()
}
func (m *mirror) write(name string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	dest := filepath.Join(m.dir, name)
	tmp := dest + "." + randomHex(8) + ".tmp"
	if err = os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if err = os.Rename(tmp, dest); err == nil {
		return nil
	}
	// Windows can reject a replacement rename even while this process owns the lock.
	if errors.Is(err, os.ErrPermission) {
		return os.WriteFile(dest, raw, 0o644)
	}
	return err
}
func (m *mirror) marker() (mirrorMarker, error) {
	var v mirrorMarker
	raw, err := os.ReadFile(filepath.Join(m.dir, offlineMarker))
	if err == nil {
		err = json.Unmarshal(raw, &v)
	}
	return v, err
}
func newMirror(dir, server string, runID int64, payload any) (*mirror, error) {
	m := &mirror{dir: dir, runs: map[int64]int64{}, tests: map[int64]int64{}}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	err := m.locked(func() error {
		prior, err := m.marker()
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if prior.RawRemoved {
			return errors.New("only the report remains; use a new recording directory")
		}
		m.rec, err = newRecorder(dir)
		if err != nil {
			return err
		}
		m.status = fmt.Sprintf("status-%d-%s.json", m.rec.pid, m.rec.tag)
		if err = m.write(m.status, mirrorStatus{Reason: "active"}); err != nil {
			return err
		}
		marker, err := m.marker()
		if err != nil {
			return err
		}
		if marker.Mirror == nil {
			marker.Mirror = &mirrorInfo{Server: server, Runs: []mirrorRun{}}
		}
		if marker.Mirror.Server != server {
			return errors.New("the recording belongs to another server")
		}
		marker.Mirror.Complete = false
		if err = m.write(offlineMarker, marker); err != nil {
			return err
		}
		mapping := filepath.Join(dir, "ids", fmt.Sprintf("server-%d", runID))
		var local int64
		if payload != nil {
			raw, err := json.Marshal(payload)
			if err != nil {
				return err
			}
			response, err := m.rec.record("POST", "/api/v1/runs", raw, "application/json")
			if err != nil {
				return err
			}
			var out struct {
				ID int64 `json:"run_id"`
			}
			if err = json.Unmarshal(response, &out); err != nil {
				return err
			}
			local = out.ID
			file, err := os.OpenFile(mapping, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
			if err != nil {
				return err
			}
			_, err = fmt.Fprint(file, local)
			closeErr := file.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
			marker.Mirror.Runs = append(marker.Mirror.Runs, mirrorRun{Local: local, Server: runID})
			if err = m.write(offlineMarker, marker); err != nil {
				return err
			}
		} else {
			raw, err := os.ReadFile(mapping)
			if err == nil {
				local, err = strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
			}
			if err != nil || local >= 0 {
				m.failed = true
				local = 0
				log.Printf("tracereports: missing local id for run #%d in %s", runID, dir)
			}
		}
		if local != 0 {
			m.runs[runID] = local
		}
		return nil
	})
	if err != nil {
		if m.rec != nil {
			m.rec.close()
		}
		return nil, err
	}
	return m, nil
}
func (m *mirror) diskError(err error) {
	m.failed = true
	if !m.disabled {
		log.Printf("tracereports: stopped local copy in %s: %v", m.dir, err)
	}
	m.disabled = true
}

var mirrorPath = regexp.MustCompile(`^/api/v1/(runs|tests)/(-?[0-9]+)`)

func (m *mirror) record(method, path string, body []byte, contentType string) []byte {
	if m.disabled || m.closed {
		return nil
	}
	parts := mirrorPath.FindStringSubmatch(path)
	if len(parts) > 0 {
		id, _ := strconv.ParseInt(parts[2], 10, 64)
		ids := m.runs
		if parts[1] == "tests" {
			ids = m.tests
		}
		local, ok := ids[id]
		if !ok && id > 0 {
			m.failed = true
			var err error
			local, err = m.rec.localID()
			if err != nil {
				m.diskError(err)
				return nil
			}
			ids[id] = local
		}
		if local < 0 {
			path = strings.Replace(path, parts[0], "/api/v1/"+parts[1]+"/"+strconv.FormatInt(local, 10), 1)
		}
	}
	raw, err := m.rec.record(method, path, body, contentType)
	if err != nil {
		m.diskError(err)
	}
	return raw
}
func (c *Client) startMirror(runID int64, payload any) {
	dir := c.recordingDir()
	m, err := newMirror(dir, c.BaseURL, runID, payload)
	if err != nil {
		log.Printf("tracereports: could not start local copy in %s: %v", dir, err)
		return
	}
	c.mu.Lock()
	c.mirror = m
	c.mu.Unlock()
}
func (m *mirror) close(complete bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return
	}
	m.closed = true
	m.rec.close()
	complete = complete && !m.failed && m.active == 0
	reason := ""
	if !complete {
		reason = "delivery or recording incomplete"
	}
	if err := m.locked(func() error { return m.write(m.status, mirrorStatus{complete, reason}) }); err != nil {
		m.diskError(err)
	}
}

func (m *mirror) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}
func (m *mirror) snapshot() (string, error) {
	files, err := filepath.Glob(filepath.Join(m.dir, "status-*.json"))
	if err != nil {
		return "", err
	}
	if len(files) == 0 {
		return "", nil
	}
	entries := map[string]string{}
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		var status mirrorStatus
		if err = json.Unmarshal(raw, &status); err != nil {
			return "", err
		}
		if !status.Complete {
			return "", nil
		}
		entries[filepath.Base(file)] = string(raw)
	}
	events, err := filepath.Glob(filepath.Join(m.dir, "events-*.jsonl"))
	if err != nil {
		return "", err
	}
	for _, file := range events {
		name := strings.Replace(strings.TrimSuffix(filepath.Base(file), ".jsonl"), "events-", "status-", 1) + ".json"
		if _, ok := entries[name]; !ok {
			return "", nil
		}
	}
	raw, err := json.Marshal(entries)
	return string(raw), err
}
func (m *mirror) prepare() string {
	var snapshot string
	if err := m.locked(func() (err error) { snapshot, err = m.snapshot(); return }); err != nil {
		m.mu.Lock()
		m.diskError(err)
		m.mu.Unlock()
	}
	return snapshot
}
func (m *mirror) finalize(report string, keep bool, before string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	err := m.locked(func() error {
		snapshot, err := m.snapshot()
		if err != nil {
			return err
		}
		marker, err := m.marker()
		if err != nil {
			return err
		}
		marker.Mirror.Complete = !m.failed && snapshot != ""
		info, reportErr := os.Stat(report)
		remove := marker.Mirror.Complete && before == snapshot && reportErr == nil && info.Mode().IsRegular() && !keep
		if remove {
			marker.RawRemoved = true
		}
		if err = m.write(offlineMarker, marker); err != nil {
			return err
		}
		if remove {
			entries, err := os.ReadDir(m.dir)
			if err != nil {
				return err
			}
			for _, entry := range entries {
				n := entry.Name()
				if (strings.HasPrefix(n, "events-") && strings.HasSuffix(n, ".jsonl")) || (strings.HasPrefix(n, "status-") && strings.HasSuffix(n, ".json")) || n == "bodies" || n == "ids" {
					if err = os.RemoveAll(filepath.Join(m.dir, n)); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		m.diskError(err)
	}
}
