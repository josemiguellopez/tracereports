// Command record graba los escenarios de internal/demo con los clientes REALES de TraceReports:
// levanta la tienda de la demo, ejecuta cada test (pytest, Playwright JS, Selenium Java, go test)
// con TRACEREPORTS_OFFLINE=always y guarda la grabación en internal/demo/scenarios/<id>. Los
// escenarios REST y JUnit no usan un cliente: se arman con las mismas llamadas de la API.
//
//	go run ./test/scenarios/record                     # todos
//	go run ./test/scenarios/record -only pytest-ok,go-ok
//	go run ./test/scenarios/record -python .venv/Scripts/python
//
// Requisitos: Python con pytest, pytest-playwright y ./client/python; Node con las dependencias de
// test/scenarios/js (npm install); Java 17 y Chrome para Selenium. Ver test/scenarios/README.md.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/demo"
)

// el commit "del build" que llevan todas las grabaciones
const commit = "9c4d2e1f7a3b5c8d0e6f1a2b3c4d5e6f7a8b9c0d"

func main() {
	only := flag.String("only", "", "scenario ids separated by commas (default: all)")
	out := flag.String("out", filepath.Join("internal", "demo", "scenarios"), "folder of the recordings")
	python := flag.String("python", "python", "Python with pytest, pytest-playwright and ./client/python installed")
	flag.Parse()
	if _, err := os.Stat("go.mod"); err != nil {
		fail(errors.New("run it from the repository root: go run ./test/scenarios/record"))
	}
	root, _ := os.Getwd()
	want := map[string]bool{}
	for _, id := range strings.Split(*only, ",") {
		if id = strings.TrimSpace(id); id != "" {
			want[id] = true
		}
	}

	shop := demo.NewShop()
	ln, err := net.Listen("tcp", strings.TrimPrefix(demo.RecordShop, "http://"))
	if err != nil {
		fail(fmt.Errorf("the shop needs %s: %w", demo.RecordShop, err))
	}
	go func() { _ = http.Serve(ln, shop) }()

	r := recorder{root: root, python: *python, shop: shop}
	failed := 0
	for _, sc := range demo.Scenarios {
		if len(want) > 0 && !want[sc.ID] {
			continue
		}
		dir := filepath.Join(root, *out, sc.ID)
		fmt.Printf("● %-24s %s\n", sc.ID, sc.Title)
		shop.Reset()
		for k, v := range sc.Bugs {
			shop.SetBug(k, v)
		}
		if err := r.record(sc, dir); err != nil {
			failed++
			fmt.Printf("  ✗ %v\n", err)
			continue
		}
		n, err := normalize(dir, root)
		if err != nil {
			failed++
			fmt.Printf("  ✗ normalize: %v\n", err)
			continue
		}
		fmt.Printf("  ✓ %d events\n", n)
	}
	if failed > 0 {
		fail(fmt.Errorf("%d scenarios were not recorded", failed))
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

type recorder struct {
	root, python string
	shop         *demo.Shop
}

func (r recorder) env(sc demo.Scenario, dir string) []string {
	var env []string
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if !strings.HasPrefix(strings.ToUpper(k), "TRACEREPORTS_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"TRACEREPORTS_OFFLINE=always", "TRACEREPORTS_OFFLINE_DIR="+dir, "TRACEREPORTS_OFFLINE_REPORT=0", "TRACEREPORTS_OFFLINE_KEEP=1",
		"TRACEREPORTS_BIN_DOWNLOAD=0", "TRACEREPORTS_URL=http://127.0.0.1:9", "TRACEREPORTS_RUN_NAME="+sc.Run,
		"TRACEREPORTS_ENV=staging", "TRACEREPORTS_PROJECT=demo-shop", "TRACEREPORTS_BRANCH=main", "TRACEREPORTS_COMMIT="+commit,
		"SHOP_URL="+demo.RecordShop, "SHOP_PASSWORD="+demo.ShopPassword)
}

func (r recorder) record(sc demo.Scenario, dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch sc.Client {
	case demo.ClientREST:
		return restScenario(sc, dir)
	case demo.ClientJUnit:
		return os.WriteFile(filepath.Join(dir, "junit.xml"), []byte(junitXML), 0o644)
	// sc.Test elige los tests; vacío graba la suite completa
	case demo.ClientPytest:
		args := []string{"-m", "pytest", "test_shop.py", "--tracereports", "-p", "no:cacheprovider", "-q"}
		if sc.Test != "" {
			args = append(args, "-k", sc.Test)
		}
		cmd = exec.Command(r.python, args...)
		cmd.Dir = filepath.Join(r.root, "test", "scenarios", "python")
	case demo.ClientPlaywright:
		args := []string{filepath.Join("node_modules", "@playwright", "test", "cli.js"), "test"}
		if sc.Test != "" {
			args = append(args, "-g", sc.Test) // expresión regular sobre el título
		}
		cmd = exec.Command("node", args...)
		cmd.Dir = filepath.Join(r.root, "test", "scenarios", "js")
	case demo.ClientSelenium:
		gradlew := "./gradlew"
		if runtime.GOOS == "windows" {
			gradlew = "gradlew.bat"
		}
		args := []string{"test", "-q"}
		if sc.Test != "" {
			args = append(args, "--tests", sc.Test)
		}
		cmd = exec.Command(filepath.Join(r.root, "test", "scenarios", "java", gradlew), args...)
		cmd.Dir = filepath.Join(r.root, "test", "scenarios", "java")
	case demo.ClientGo:
		run := "."
		if sc.Test != "" {
			run = "^(" + sc.Test + ")$"
		}
		cmd = exec.Command("go", "test", "-count=1", "-run", run, ".")
		cmd.Dir = filepath.Join(r.root, "test", "scenarios", "go")
	default:
		return fmt.Errorf("unknown client %q", sc.Client)
	}
	cmd.Env = r.env(sc, dir)
	var log bytes.Buffer
	cmd.Stdout, cmd.Stderr = &log, &log
	_ = cmd.Run() // los escenarios NOK fallan a propósito: lo que importa es la grabación
	files, _ := filepath.Glob(filepath.Join(dir, "events-*.jsonl"))
	if len(files) == 0 {
		return fmt.Errorf("nothing was recorded; output:\n%s", log.String())
	}
	return nil
}

// normalize leaves only what a replay needs, with the shop's origin replaced by a placeholder and
// no path of the machine that recorded it.
func normalize(dir, root string) (int, error) {
	for _, extra := range []string{"report", "ids", "report.zip"} {
		_ = os.RemoveAll(filepath.Join(dir, extra))
	}
	home, _ := os.UserHomeDir()
	repl := []string{demo.RecordShop, demo.ShopPlaceholder, strings.Replace(demo.RecordShop, "127.0.0.1", "localhost", 1), demo.ShopPlaceholder}
	for _, p := range []string{root, home} {
		if p == "" {
			continue
		}
		for _, form := range []string{p, filepath.ToSlash(p), strings.ReplaceAll(p, `\`, `\\`)} {
			to := "/repo"
			if p == home {
				to = "~"
			}
			repl = append(repl, form, to)
		}
	}
	rep := strings.NewReplacer(repl...)
	events := 0
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		ext := filepath.Ext(path)
		if ext != ".jsonl" && ext != ".json" && ext != ".xml" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		// LF en cualquier sistema (el cliente de Python escribe CRLF en Windows)
		text := strings.ReplaceAll(rep.Replace(string(data)), "\r\n", "\n")
		if ext == ".jsonl" {
			events += strings.Count(strings.TrimSpace(text), "\n") + 1
		}
		return os.WriteFile(path, []byte(text), 0o644)
	})
	return events, err
}

// ---------- REST: las llamadas de la API, hechas a mano como lo haría cualquier lenguaje ----------

type event struct {
	Seq         int64           `json:"seq"`
	TS          int64           `json:"ts"`
	Method      string          `json:"method"`
	Path        string          `json:"path"`
	ContentType string          `json:"content_type"`
	Body        json.RawMessage `json:"body,omitempty"`
	LocalID     int64           `json:"local_id,omitempty"`
}

type restWriter struct {
	events []event
	t      time.Time
}

func (w *restWriter) add(method, path string, body any, localID int64) {
	b, _ := json.Marshal(body)
	w.t = w.t.Add(400 * time.Millisecond)
	w.events = append(w.events, event{Seq: int64(len(w.events) + 1), TS: w.t.UnixMilli(), Method: method, Path: path,
		ContentType: "application/json", Body: b, LocalID: localID})
}

// shopCall makes a real call to the shop and returns it in the network format of the API.
func shopCall(method, path, token, body string) map[string]any {
	req, _ := http.NewRequest(method, demo.RecordShop+path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	reqH := map[string]string{"content-type": "application/json"}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
		reqH["authorization"] = "Bearer " + token
	}
	start := time.Now()
	res, err := http.DefaultClient.Do(req)
	c := map[string]any{"method": method, "url": demo.RecordShop + path, "resource_type": "fetch", "request_headers": reqH,
		"post_data": body, "started_at": start.UnixMilli()}
	if err != nil {
		c["failed"], c["error_text"] = true, err.Error()
		return c
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	resH := map[string]string{}
	for k := range res.Header {
		resH[strings.ToLower(k)] = res.Header.Get(k)
	}
	c["status"], c["status_text"], c["response_body"], c["response_headers"] = res.StatusCode, http.StatusText(res.StatusCode), string(b), resH
	c["duration_ms"] = time.Since(start).Milliseconds()
	c["mime_type"] = res.Header.Get("Content-Type")
	return c
}

func restScenario(sc demo.Scenario, dir string) error {
	w := &restWriter{t: time.Now()}
	w.add("POST", "/api/v1/runs", map[string]string{"name": sc.Run, "environment": "staging", "project": "demo-shop",
		"branch": "main", "commit": commit, "framework": "rest"}, -1)
	test := func(local int64, name, key string, steps [][2]string, net []map[string]any, status, errMsg string) {
		w.add("POST", "/api/v1/runs/-1/tests", map[string]string{"name": name, "category": "api", "key": key}, local)
		p := fmt.Sprintf("/api/v1/tests/%d", local)
		for i, st := range steps {
			if i == len(steps)-1 && len(net) > 0 {
				w.add("POST", p+"/network", map[string]any{"connections": net}, 0)
			}
			w.add("POST", p+"/logs", map[string]any{"status": st[0], "message": st[1], "timestamp": w.t.UnixMilli()}, 0)
		}
		w.add("PATCH", p+"/finish", map[string]string{"status": status, "error_message": errMsg}, 0)
	}
	switch sc.ID {
	case "rest-ok":
		health := shopCall("GET", "/api/health", "", "")
		products := shopCall("GET", "/api/products", "", "")
		test(-2, "health check answers", "smoke/health", [][2]string{{"INFO", "GET /api/health"}, {"PASS", "status ok"}}, []map[string]any{health}, "PASS", "")
		test(-3, "catalog lists 3 products", "smoke/catalog", [][2]string{{"INFO", "GET /api/products"}, {"PASS", "3 products"}}, []map[string]any{products}, "PASS", "")
	case "rest-nok-backend":
		login := shopCall("POST", "/api/login", "", fmt.Sprintf(`{"email":%q,"password":%q}`, demo.ShopUser, demo.ShopPassword))
		var tok struct{ AccessToken string }
		_ = json.Unmarshal([]byte(login["response_body"].(string)), &tok)
		order := shopCall("POST", "/api/orders", tok.AccessToken, `{"items":[{"id":"P-1","qty":1}]}`)
		test(-2, "create an order", "orders/create", [][2]string{{"PASS", "POST /api/login → token"},
			{"FAIL", fmt.Sprintf("POST /api/orders → %v (expected 201)", order["status"])}},
			[]map[string]any{login, order}, "FAIL", fmt.Sprintf("expected status 201, got %v", order["status"]))
	default:
		return fmt.Errorf("no REST script for %s", sc.ID)
	}
	w.add("PATCH", "/api/v1/runs/-1/finish", map[string]any{}, 0)
	var buf bytes.Buffer
	for _, e := range w.events {
		b, _ := json.Marshal(e)
		buf.Write(append(b, '\n'))
	}
	if err := os.WriteFile(filepath.Join(dir, "events-rest.jsonl"), buf.Bytes(), 0o644); err != nil {
		return err
	}
	marker, _ := json.Marshal(map[string]any{"format": "tracereports-offline", "version": 1, "id": "scenario-" + sc.ID})
	return os.WriteFile(filepath.Join(dir, "tracereports-offline.json"), marker, 0o644)
}

const junitXML = `<?xml version="1.0" encoding="UTF-8"?>
<testsuite name="com.demoshop.InventoryServiceTest" tests="4" failures="1" errors="0" skipped="1" time="1.84" timestamp="__TIMESTAMP__">
  <testcase classname="com.demoshop.InventoryServiceTest" name="reservesStock" time="0.21"/>
  <testcase classname="com.demoshop.InventoryServiceTest" name="releasesStockOnCancel" time="0.18"/>
  <testcase classname="com.demoshop.InventoryServiceTest" name="rejectsNegativeQuantity" time="0.05">
    <failure message="expected: &lt;400&gt; but was: &lt;500&gt;" type="org.opentest4j.AssertionFailedError">org.opentest4j.AssertionFailedError: expected: &lt;400&gt; but was: &lt;500&gt;
	at com.demoshop.InventoryServiceTest.rejectsNegativeQuantity(InventoryServiceTest.java:44)</failure>
  </testcase>
  <testcase classname="com.demoshop.InventoryServiceTest" name="syncsWithWarehouse" time="0">
    <skipped message="warehouse sandbox not available"/>
  </testcase>
</testsuite>
`
