package demo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/offline"
)

// Play sends a scenario to the TraceReports server at base, as its client did when it was
// recorded, and returns the run it created. The captured calls point to the shop at shopURL and
// the times move to now; each play is a new run (a fresh recording id), so a scenario can be
// played again and again.
func Play(ctx context.Context, base, shopURL string, sc Scenario, now time.Time) (int64, error) {
	return PlayLogged(ctx, base, shopURL, sc, now, nil)
}

// Sent is one request a play made to TraceReports (what the client sent).
type Sent struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Status int    `json:"status"`
	Ms     int64  `json:"ms"`
	Error  string `json:"error,omitempty"`
}

// PlayLogged is Play, telling log each request it sends.
func PlayLogged(ctx context.Context, base, shopURL string, sc Scenario, now time.Time, log func(Sent)) (int64, error) {
	base = strings.TrimRight(base, "/")
	if sc.Client == ClientJUnit {
		return playJUnit(ctx, base, sc, now, log)
	}
	dir, err := os.MkdirTemp("", "tracereports-scenario-")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(dir)
	if err := prepare(sc.ID, dir, shopURL, now); err != nil {
		return 0, fmt.Errorf("scenario %s: %w", sc.ID, err)
	}
	rec, err := offline.Open(dir)
	if err != nil {
		return 0, err
	}
	res, err := rec.Replay(offline.Target{Doer: ctxDoer{ctx, &http.Client{Timeout: 30 * time.Second}, log}, BaseURL: base})
	if err != nil {
		return 0, err
	}
	if res.Rejected > 0 || len(res.Runs) == 0 {
		return 0, fmt.Errorf("scenario %s: %d events rejected: %s", sc.ID, res.Rejected, strings.Join(res.Errors, "; "))
	}
	return res.Runs[0], nil
}

// ctxDoer cancels the replay with ctx and tells log each request.
type ctxDoer struct {
	ctx context.Context
	c   *http.Client
	log func(Sent)
}

func (d ctxDoer) Do(r *http.Request) (*http.Response, error) {
	start := time.Now()
	res, err := d.c.Do(r.WithContext(d.ctx))
	if d.log != nil {
		s := Sent{Method: r.Method, Path: r.URL.RequestURI(), Ms: time.Since(start).Milliseconds()}
		if err != nil {
			s.Error = err.Error()
		} else {
			s.Status = res.StatusCode
		}
		d.log(s)
	}
	return res, err
}

// prepare copies a recording to dir: the shop's origin, the times and the recording id of now.
func prepare(id, dir, shopURL string, now time.Time) error {
	src := path.Join("scenarios", id)
	var events [][]byte
	var names []string
	err := fs.WalkDir(scenarioFiles, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel := strings.TrimPrefix(p, src+"/")
		data, err := scenarioFiles.ReadFile(p)
		if err != nil {
			return err
		}
		if strings.HasPrefix(rel, "events-") && strings.HasSuffix(rel, ".jsonl") {
			events = append(events, data)
			names = append(names, rel)
			return nil
		}
		if rel == offline.Marker {
			return nil // lleva un id nuevo (abajo)
		}
		out := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, bytes.ReplaceAll(data, []byte(ShopPlaceholder), []byte(shopURL)), 0o644)
	})
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return fmt.Errorf("not recorded yet (go run ./test/scenarios/record -only %s)", id)
	}
	// desplazamiento: el último evento de la grabación pasa a ser "ahora"
	first, last := int64(0), int64(0)
	parsed := make([][]map[string]any, len(events))
	for i, data := range events {
		for _, line := range bytes.Split(bytes.TrimSpace(bytes.ReplaceAll(data, []byte(ShopPlaceholder), []byte(shopURL))), []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var e map[string]any
			dec := json.NewDecoder(bytes.NewReader(line))
			dec.UseNumber()
			if err := dec.Decode(&e); err != nil {
				return fmt.Errorf("%s: %w", names[i], err)
			}
			if ts, err := e["ts"].(json.Number).Int64(); err == nil {
				if first == 0 || ts < first {
					first = ts
				}
				last = max(last, ts)
			}
			parsed[i] = append(parsed[i], e)
		}
	}
	delta := now.UnixMilli() - last
	window := [2]int64{first - int64(time.Hour/time.Millisecond), last + int64(time.Hour/time.Millisecond)}
	for i, evs := range parsed {
		var buf bytes.Buffer
		for _, e := range evs {
			shift(e, delta, window)
			b, err := json.Marshal(e)
			if err != nil {
				return err
			}
			buf.Write(append(b, '\n'))
		}
		if err := os.WriteFile(filepath.Join(dir, names[i]), buf.Bytes(), 0o644); err != nil {
			return err
		}
	}
	nonce := make([]byte, 6)
	_, _ = rand.Read(nonce)
	marker, _ := json.Marshal(map[string]any{"format": "tracereports-offline", "version": offline.Version, "id": "scenario-" + id + "-" + hex.EncodeToString(nonce)})
	return os.WriteFile(filepath.Join(dir, offline.Marker), marker, 0o644)
}

// shift moves every time of an event (its ts and the times inside its body: steps, network,
// console) by delta. Only numbers inside the recording's time window are times.
func shift(v any, delta int64, window [2]int64) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			x[k] = shift(val, delta, window)
		}
	case []any:
		for i, val := range x {
			x[i] = shift(val, delta, window)
		}
	case json.Number:
		if n, err := x.Int64(); err == nil && n >= window[0] && n <= window[1] {
			return json.Number(fmt.Sprint(n + delta))
		}
	}
	return v
}

func playJUnit(ctx context.Context, base string, sc Scenario, now time.Time, log func(Sent)) (int64, error) {
	data, err := scenarioFiles.ReadFile(path.Join("scenarios", sc.ID, "junit.xml"))
	if err != nil {
		return 0, fmt.Errorf("scenario %s: not recorded yet", sc.ID)
	}
	xml := strings.Replace(string(data), "__TIMESTAMP__", now.Add(-2*time.Second).UTC().Format("2006-01-02T15:04:05"), 1)
	q := url.Values{"name": {sc.Run}, "project": {"demo-shop"}, "environment": {"staging"}, "branch": {"main"}, "framework": {"junit5"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/import/junit?"+q.Encode(), strings.NewReader(xml))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/xml")
	res, err := ctxDoer{ctx, http.DefaultClient, log}.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	var out struct {
		RunID int64 `json:"run_id"`
	}
	if res.StatusCode != http.StatusCreated || json.Unmarshal(body, &out) != nil || out.RunID == 0 {
		return 0, fmt.Errorf("import %s: %s %s", sc.ID, res.Status, strings.TrimSpace(string(body)))
	}
	return out.RunID, nil
}
