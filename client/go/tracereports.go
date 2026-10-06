// Package tracereports is the Go client for a TraceReports server.
//
// It is "best effort" by design: reporting must never break or slow down a test suite.
// Every call uses a short timeout and errors are returned (and can be ignored). Failed calls
// (no connection, 5xx, 408, 429) are retried twice with an Idempotency-Key, so a retry never
// duplicates a step; after a few consecutive connection failures the client stops trying for
// 30 seconds instead of waiting its timeout in every test. Unlike the Python client there is
// no background queue: calls are synchronous.
//
// The run context (project, branch, commit) comes from $TRACEREPORTS_PROJECT and the CI variables
// or git: history and comparisons only use runs of the same project, environment and branch.
//
// If the run cannot be created (no server, unreachable, wrong token) the evidence is recorded in
// a folder instead ($TRACEREPORTS_OFFLINE_DIR, default ./tracereports-offline/<session>):
// `tracereports report <dir>` builds a static HTML report and `tracereports push <dir>` uploads
// it later. Client.Offline = "always" records without a server, "off" disables it.
//
//	c := tracereports.New("")                       // $TRACEREPORTS_URL or http://localhost:8080
//	c.StartRun("Regresión web", "staging")
//	t, _ := c.StartTestWithKey("login/TestAdmin", "Login", "smoke", "El admin entra al Dashboard")
//	t.Info("Abrir el login")
//	t.Screenshot(png, "Formulario de login", tracereports.Info)
//	t.Finish(tracereports.Pass, "", "")
//	c.FinishRun()
package tracereports

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Statuses accepted for steps and tests.
const (
	Info    = "INFO"
	Pass    = "PASS"
	Fail    = "FAIL"
	Warning = "WARNING"
	Skip    = "SKIP"
)

// ErrDisabled is returned when the client is disabled or the server is unreachable.
var ErrDisabled = errors.New("tracereports: client disabled")

const (
	maxConsecutiveFailures = 3
	circuitOpen            = 30 * time.Second
	retries                = 2
)

// Client talks to the TraceReports REST API.
type Client struct {
	BaseURL string
	Token   string // server's TRACEREPORTS_TOKEN; default $TRACEREPORTS_TOKEN
	HTTP    *http.Client
	RunID   int64
	// Run context (defaults: $TRACEREPORTS_PROJECT, and branch/commit from the CI variables or git).
	Project, Branch, Commit string
	// OfflineDir is where to record without a server (default $TRACEREPORTS_OFFLINE_DIR; empty: a
	// new folder per session inside ./tracereports-offline). Offline is "auto" (default: record
	// only if the run cannot be created), "always" or "off" (default $TRACEREPORTS_OFFLINE).
	OfflineDir, Offline string
	// OfflineReport is the index.html built when a recording finishes (needs the binary).
	OfflineReport string

	rec       *recorder
	mu        sync.Mutex
	failures  int
	downUntil time.Time
	disabled  bool
	ownsRun   bool
}

// New creates a client. An empty baseURL uses $TRACEREPORTS_URL or http://localhost:8080.
// Set TRACEREPORTS_DISABLED=1 to turn every call into a no-op.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = getenv("URL")
	}
	if baseURL == "" {
		baseURL = "http://localhost:8080"
	}
	d := getenv("DISABLED")
	return &Client{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		Token:      getenv("TOKEN"),
		HTTP:       &http.Client{Timeout: 3 * time.Second},
		Project:    getenv("PROJECT"),
		Branch:     firstEnv("TRACEREPORTS_BRANCH", "GITHUB_HEAD_REF", "GITHUB_REF_NAME", "CI_COMMIT_REF_NAME", "BITBUCKET_BRANCH", "BUILD_SOURCEBRANCHNAME", "BRANCH_NAME", "CIRCLE_BRANCH", "GIT_BRANCH"),
		Commit:     firstEnv("TRACEREPORTS_COMMIT", "GITHUB_SHA", "CI_COMMIT_SHA", "BITBUCKET_COMMIT", "BUILD_SOURCEVERSION", "CIRCLE_SHA1", "GIT_COMMIT"),
		disabled:   d == "1" || d == "true",
		OfflineDir: getenv("OFFLINE_DIR"),
		Offline:    getenv("OFFLINE"),
	}
}

// getenv returns TRACEREPORTS_<name>.
func getenv(name string) string {
	return os.Getenv("TRACEREPORTS_" + name)
}

func firstEnv(keys ...string) string {
	for _, k := range keys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return strings.TrimPrefix(v, "origin/")
		}
	}
	return ""
}

func git(args ...string) string {
	out, err := exec.Command("git", args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// StartRun creates the run (suite execution) that the following tests belong to. With
// $TRACEREPORTS_RUN_ID set it joins that run instead (CI shards); the creator closes it. If the
// run cannot be created, the evidence is recorded locally (see the package doc).
func (c *Client) StartRun(name, environment string) (int64, error) {
	if id, err := strconv.ParseInt(getenv("RUN_ID"), 10, 64); err == nil && id != 0 {
		// negativo: una ejecución que otro proceso graba sin servidor ($TRACEREPORTS_OFFLINE_DIR)
		if id < 0 && !c.disabled && !c.Recording() {
			c.startRecording("")
		}
		c.RunID, c.ownsRun = id, false
		return id, nil
	}
	if c.offlineMode() == "always" && !c.disabled && !c.Recording() {
		c.startRecording("")
	}
	if c.Branch == "" {
		if b := git("rev-parse", "--abbrev-ref", "HEAD"); b != "HEAD" {
			c.Branch = b
		}
	}
	if c.Commit == "" {
		c.Commit = git("rev-parse", "HEAD")
	}
	var out struct {
		RunID int64 `json:"run_id"`
	}
	payload := map[string]string{"name": name, "environment": environment,
		"project": c.Project, "branch": c.Branch, "commit": c.Commit, "framework": "go"}
	err := c.do(http.MethodPost, "/api/v1/runs", payload, &out)
	if out.RunID == 0 && !c.disabled && c.offlineMode() == "auto" && !c.Recording() {
		// sin servidor, caído o con el token equivocado: se graba en vez de perderlo todo
		if c.startRecording(fmt.Sprintf("could not create the run at %s (%v)", c.BaseURL, err)) {
			c.mu.Lock()
			c.failures, c.downUntil = 0, time.Time{}
			c.mu.Unlock()
			err = c.do(http.MethodPost, "/api/v1/runs", payload, &out)
		}
	}
	c.RunID, c.ownsRun = out.RunID, out.RunID != 0
	return out.RunID, err
}

// FinishRun closes the run; the server then builds the run diagnosis and notifications.
// A client that joined a run created elsewhere ($TRACEREPORTS_RUN_ID) does not close it.
func (c *Client) FinishRun() error { return c.finishRun(false) }

// FinishRunInterrupted closes the run as incomplete (cancelled suite, crashed process...):
// the server never shows it as passed.
func (c *Client) FinishRunInterrupted() error { return c.finishRun(true) }

func (c *Client) finishRun(interrupted bool) error {
	if c.RunID == 0 {
		return ErrDisabled
	}
	if !c.ownsRun {
		c.mu.Lock()
		if c.rec != nil { // grabando para una ejecución de otro proceso: el dueño arma el reporte
			c.rec.close()
		}
		c.mu.Unlock()
		return nil
	}
	err := c.do(http.MethodPatch, fmt.Sprintf("/api/v1/runs/%d/finish", c.RunID), map[string]bool{"interrupted": interrupted}, nil)
	if c.Recording() {
		c.finishRecording()
	}
	return err
}

// ReportURL is the link to the run in the web UI. When recording without a server it is the
// static report built at FinishRun ("" if the tracereports binary is not installed).
func (c *Client) ReportURL() string {
	if c.Recording() {
		return c.OfflineReport
	}
	return fmt.Sprintf("%s/#run=%d&view=dashboard", c.BaseURL, c.RunID)
}

// Test is a running test case. A nil *Test (when the server is unreachable) is safe to use.
type Test struct {
	c  *Client
	ID int64
}

// StartTest starts a test inside the current run. category accepts comma-separated tags.
// Its identity is its name: prefer StartTestWithKey so homonymous tests keep separate histories.
func (c *Client) StartTest(name, category, description string) (*Test, error) {
	return c.StartTestWithKey("", name, category, description)
}

// StartTestWithKey starts a test with a stable identity (e.g. "package/TestName/subtest", see
// Key): history, flakiness and comparisons follow the key, not the visible name.
func (c *Client) StartTestWithKey(key, name, category, description string) (*Test, error) {
	if c.RunID == 0 {
		return nil, ErrDisabled
	}
	var out struct {
		TestID int64 `json:"test_id"`
	}
	err := c.do(http.MethodPost, fmt.Sprintf("/api/v1/runs/%d/tests", c.RunID),
		map[string]string{"name": name, "category": category, "description": description, "key": key}, &out)
	if err != nil {
		return nil, err
	}
	return &Test{c: c, ID: out.TestID}, nil
}

// Log adds a step with the given status.
func (t *Test) Log(status, message string) error {
	if t == nil {
		return ErrDisabled
	}
	return t.c.do(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/logs", t.ID), map[string]any{
		"status": status, "message": message, "timestamp": time.Now().UnixMilli(),
	}, nil)
}

// Info, Pass, Fail and Warn are shortcuts for Log.
func (t *Test) Info(message string) error { return t.Log(Info, message) }
func (t *Test) Pass(message string) error { return t.Log(Pass, message) }
func (t *Test) Fail(message string) error { return t.Log(Fail, message) }
func (t *Test) Warn(message string) error { return t.Log(Warning, message) }

// Screenshot uploads a PNG/JPEG as a step and returns the image URL.
func (t *Test) Screenshot(png []byte, message, status string) (string, error) {
	if t == nil {
		return "", ErrDisabled
	}
	if status == "" {
		status = Info
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("message", message)
	_ = w.WriteField("status", status)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="screenshot.png"`)
	h.Set("Content-Type", "image/png")
	part, _ := w.CreatePart(h)
	_, _ = part.Write(png)
	_ = w.Close()

	var out struct {
		URL string `json:"url"`
	}
	if err := t.c.send(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/screenshot", t.ID), body.Bytes(), w.FormDataContentType(), &out, 10*time.Second); err != nil {
		return "", err
	}
	if out.URL == "" { // grabando sin servidor: todavía no tiene URL
		return "", nil
	}
	return t.c.BaseURL + out.URL, nil
}

// Conn is one captured network call (see the playwright-go example for the capture).
type Conn struct {
	Method          string            `json:"method"`
	URL             string            `json:"url"`
	Status          int               `json:"status"`
	StatusText      string            `json:"status_text"`
	MimeType        string            `json:"mime_type"`
	ResourceType    string            `json:"resource_type"`
	Failed          bool              `json:"failed"`
	ErrorText       string            `json:"error_text"`
	StartedAt       int64             `json:"started_at"` // unix ms
	DurationMs      *int64            `json:"duration_ms"`
	RequestHeaders  map[string]string `json:"request_headers"`
	PostData        string            `json:"post_data"`
	ResponseHeaders map[string]string `json:"response_headers"`
	ResponseBody    string            `json:"response_body"`
	BodySize        int64             `json:"body_size"`
	// Expected marks a negative response the test checks on purpose (e.g. a 401 with bad
	// credentials): it is not counted as an error nor proposed as the cause of a failure.
	Expected bool `json:"expected"`
}

// Network uploads the network calls of the test (its "Red" tab). Send it before Finish so
// the AI triage of a failure can see the backend errors.
func (t *Test) Network(conns []Conn) error {
	if t == nil {
		return ErrDisabled
	}
	if len(conns) == 0 {
		return nil
	}
	const maxBody = 256 << 10
	for i := range conns {
		if n := int64(len(conns[i].ResponseBody)); n > conns[i].BodySize {
			conns[i].BodySize = n
		}
		if len(conns[i].ResponseBody) > maxBody {
			conns[i].ResponseBody = conns[i].ResponseBody[:maxBody]
		}
	}
	raw, err := json.Marshal(map[string]any{"connections": conns})
	if err != nil {
		return err
	}
	return t.c.send(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/network", t.ID), raw, "application/json", nil, 15*time.Second)
}

// DOM uploads the page snapshot taken when the test failed (elements with their attributes
// and on-screen position, see examples/playwright-go). With it the report recommends robust
// selectors when a locator broke. Send it before Finish.
func (t *Test) DOM(snapshot any) error {
	if t == nil {
		return ErrDisabled
	}
	if snapshot == nil {
		return nil
	}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return err
	}
	return t.c.send(http.MethodPost, fmt.Sprintf("/api/v1/tests/%d/dom", t.ID), raw, "application/json", nil, 10*time.Second)
}

// Finish closes the test. status "" derives it from the steps; a FAIL triggers the AI triage.
func (t *Test) Finish(status, errorMessage, errorTrace string) error {
	return t.FinishAttempts(status, errorMessage, errorTrace, 1)
}

// FinishAttempts closes a test that the runner executed `attempts` times (retries): a PASS
// with attempts > 1 is shown as "passed after retry" and counts as flakiness evidence.
func (t *Test) FinishAttempts(status, errorMessage, errorTrace string, attempts int) error {
	if t == nil {
		return ErrDisabled
	}
	return t.c.do(http.MethodPatch, fmt.Sprintf("/api/v1/tests/%d/finish", t.ID), map[string]any{
		"status": status, "error_message": errorMessage, "error_trace": errorTrace, "attempts": attempts,
	}, nil)
}

// Key builds a stable test identity from the package path and t.Name() (which includes the
// subtest path), e.g. Key("shop/checkout", t.Name()) -> "shop/checkout/TestPay/visa".
func Key(pkg, testName string) string { return strings.Trim(pkg, "/") + "/" + testName }

// ─── HTTP ────────────────────────────────────────────────────────────────

func (c *Client) do(method, path string, payload, out any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return c.send(method, path, raw, "application/json", out, 0)
}

func newKey() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// retryable reports whether a response status is worth retrying.
func retryable(code int) bool {
	return code == 408 || code == 425 || code == 429 || code >= 500
}

// send performs the call with up to `retries` retries (same Idempotency-Key, so the server
// applies it once). While the circuit is open (server unreachable) it fails fast.
func (c *Client) send(method, path string, body []byte, contentType string, out any, timeout time.Duration) error {
	c.mu.Lock()
	off := c.disabled || time.Now().Before(c.downUntil)
	rec := c.rec
	c.mu.Unlock()
	if rec != nil && !c.disabled {
		raw, err := rec.record(method, path, body, contentType)
		if err == nil && out != nil && len(raw) > 0 {
			err = json.Unmarshal(raw, out)
		}
		return err
	}
	if off {
		return ErrDisabled
	}
	key := newKey()
	var err error
	for attempt := 0; attempt <= retries; attempt++ {
		if attempt > 0 {
			time.Sleep([]time.Duration{300 * time.Millisecond, time.Second}[min(attempt-1, 1)])
		}
		var retry bool
		retry, err = c.sendOnce(method, path, body, contentType, out, timeout, key)
		if err == nil || !retry {
			return err
		}
		c.mu.Lock()
		down := time.Now().Before(c.downUntil)
		c.mu.Unlock()
		if down {
			break
		}
	}
	return err
}

func (c *Client) sendOnce(method, path string, body []byte, contentType string, out any, timeout time.Duration, key string) (bool, error) {
	req, err := http.NewRequest(method, c.BaseURL+path, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("User-Agent", "tracereports-go/0.2")
	req.Header.Set("Idempotency-Key", key)
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := c.HTTP
	if timeout > 0 {
		cp := *client
		cp.Timeout = timeout
		client = &cp
	}
	resp, err := client.Do(req)
	if err != nil {
		c.mu.Lock()
		c.failures++
		if c.failures >= maxConsecutiveFailures {
			c.downUntil = time.Now().Add(circuitOpen)
			if c.failures == maxConsecutiveFailures {
				log.Printf("tracereports: server unreachable at %s, retrying in %s", c.BaseURL, circuitOpen)
			}
		}
		c.mu.Unlock()
		return true, err
	}
	defer resp.Body.Close()
	c.mu.Lock()
	c.failures = 0
	c.downUntil = time.Time{}
	c.mu.Unlock()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if resp.StatusCode == http.StatusUnauthorized {
			msg += " (configure the token: Client.Token or $TRACEREPORTS_TOKEN)"
		}
		return retryable(resp.StatusCode), fmt.Errorf("tracereports: %s %s -> HTTP %d: %s", method, path, resp.StatusCode, msg)
	}
	if out != nil && len(raw) > 0 {
		return false, json.Unmarshal(raw, out)
	}
	return false, nil
}
