package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/posture"
)

// postureVars son todas las variables que mira doctor: cada test parte de cero.
var postureVars = []string{"BIND", "RUNTIME", "TOKEN", "INGEST_TOKEN", "UI_USER", "UI_PASSWORD", "UI_VIEWER_USER", "UI_VIEWER_PASSWORD", "INSECURE",
	"ALLOWED_HOSTS", "ALLOWED_CIDRS", "TRUSTED_PROXIES", "SETTINGS_LOCKED", "RETENTION_DAYS", "SCREENSHOTS",
	"SCREENSHOT_MASK", "ENCRYPT_AT_REST", "DB_KEY", "DB_KEY_FILE", "DB_KEY_PREVIOUS", "DB_KEY_PREVIOUS_FILE",
	"REDACT", "AI_ALLOWED_PROVIDERS", "LAB_HOSTS", "GITHUB_REPO", "GITHUB_TOKEN", "JIRA_PROJECT", "AZURE_PROJECT"}

func cleanPostureEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, k := range postureVars {
		t.Setenv("TRACEREPORTS_"+k, "")
	}
	for _, k := range []string{"AI_PROVIDER", "AI_API_KEY", "AI_BASE_URL", "GEMINI_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"OLLAMA_HOST", "TEAMS_WEBHOOK_URL", "SLACK_WEBHOOK_URL", "NOTIFY_DETAIL", "DATA_DIR"} {
		t.Setenv(k, "")
	}
	t.Setenv("TRACEREPORTS_ENV_FILE", filepath.Join(dir, "none.env"))
	return dir
}

// guideProfile lee el bloque .env del "Perfil de producción" de la guía de hardening y completa los
// valores vacíos (tokens, clave): la guía y doctor no pueden divergir.
func guideProfile(t *testing.T, dir string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "docs", "es", "security-hardening.md"))
	if err != nil {
		t.Fatal(err)
	}
	doc := strings.ReplaceAll(string(raw), "\r\n", "\n")
	start := strings.Index(doc, "## Perfil de producción")
	block := doc[start:]
	block = block[strings.Index(block, "```ini\n")+7:]
	block = block[:strings.Index(block, "```")]
	keyFile := filepath.Join(t.TempDir(), "db.key") // fuera de DATA_DIR
	os.WriteFile(keyFile, []byte(strings.Repeat("ab", 32)), 0o600)
	fill := map[string]string{
		"TRACEREPORTS_TOKEN": "admin-" + strings.Repeat("a", 32), "TRACEREPORTS_INGEST_TOKEN": "ingest-" + strings.Repeat("b", 32),
		"TRACEREPORTS_UI_PASSWORD": "una-clave-larga-de-prueba", "TRACEREPORTS_DB_KEY_FILE": keyFile,
		"TRACEREPORTS_UI_VIEWER_PASSWORD": "otra-clave-de-lectura",
	}
	vars := map[string]string{}
	line := regexp.MustCompile(`^([A-Z_]+)=(\S*)`)
	for _, l := range strings.Split(block, "\n") {
		m := line.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		v := m[2]
		if f, ok := fill[m[1]]; ok {
			v = f
		}
		vars[m[1]] = v
	}
	for _, k := range []string{"TRACEREPORTS_TOKEN", "TRACEREPORTS_ALLOWED_CIDRS", "TRACEREPORTS_ENCRYPT_AT_REST", "NOTIFY_DETAIL"} {
		if _, ok := vars[k]; !ok {
			t.Fatalf("el perfil de la guía no tiene %s", k)
		}
	}
	return vars
}

func doctor(t *testing.T, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runDoctor(args, &out)
	return out.String(), err
}

func TestDoctorGuideProfileIsAllOK(t *testing.T) {
	if runtime.GOOS != "windows" && os.Getuid() == 0 {
		t.Skip("como root, process.user da WARN a propósito")
	}
	dir := cleanPostureEnv(t)
	data := filepath.Join(dir, "data")
	os.MkdirAll(data, 0o700)
	for k, v := range guideProfile(t, dir) {
		t.Setenv(k, v)
	}
	out, err := doctor(t, "--security", "-data-dir", data)
	if err != nil || strings.Contains(out, "WARN") || strings.Contains(out, "FAIL") || !strings.Contains(out, "12 ok, 0 warn, 0 fail") {
		t.Fatalf("perfil de la guía: %v\n%s", err, out)
	}
	for _, secret := range []string{strings.Repeat("a", 32), strings.Repeat("b", 32), "una-clave-larga", "otra-clave-de-lectura", strings.Repeat("ab", 32)} {
		if strings.Contains(out, secret) {
			t.Fatal("un secreto apareció en la salida")
		}
	}
}

func TestDoctorExposedDefaultFails(t *testing.T) {
	dir := cleanPostureEnv(t)
	t.Setenv("TRACEREPORTS_BIND", "0.0.0.0")
	t.Setenv("TRACEREPORTS_INSECURE", "1")
	out, err := doctor(t, "--security", "-data-dir", filepath.Join(dir, "data"))
	if !errors.Is(err, errPostureFailed) {
		t.Fatalf("debe salir con código 1: %v", err)
	}
	for _, want := range []string{"FAIL  API tokens", "FAIL  Network exposure", "→ set TRACEREPORTS_TOKEN", "→ remove TRACEREPORTS_INSECURE"} {
		if !strings.Contains(out, want) {
			t.Errorf("falta %q:\n%s", want, out)
		}
	}
}

// Un portátil sin nada configurado: solo este equipo, sin críticos.
func TestDoctorLaptopHasNoCritical(t *testing.T) {
	dir := cleanPostureEnv(t)
	if _, err := doctor(t, "--security", "-data-dir", filepath.Join(dir, "data")); err != nil {
		t.Fatal(err)
	}
}

func TestDoctorJSONContract(t *testing.T) {
	dir := cleanPostureEnv(t)
	t.Setenv("TRACEREPORTS_REDACT", "off")
	t.Setenv("TRACEREPORTS_ENCRYPT_AT_REST", "1") // sin clave
	out, err := doctor(t, "--security", "-json", "-data-dir", filepath.Join(dir, "data"))
	if !errors.Is(err, errPostureFailed) {
		t.Fatalf("%v", err)
	}
	var r posture.Report
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Version != 1 || len(r.Checks) != 12 {
		t.Fatalf("%v %+v", err, r)
	}
	got := map[string]string{}
	for _, c := range r.Checks {
		got[c.ID] = c.Level
	}
	if got["evidence.redaction"] != "fail" || got["data.encryption"] != "fail" || got["auth.tokens"] != "warn" {
		t.Fatalf("%v", got)
	}
}

func TestDoctorKeyInsideDataDirFails(t *testing.T) {
	dir := cleanPostureEnv(t)
	data := filepath.Join(dir, "data")
	os.MkdirAll(data, 0o700)
	key := filepath.Join(data, "db.key")
	os.WriteFile(key, []byte(strings.Repeat("cd", 32)), 0o600)
	t.Setenv("TRACEREPORTS_ENCRYPT_AT_REST", "1")
	t.Setenv("TRACEREPORTS_DB_KEY_FILE", key)
	out, err := doctor(t, "--security", "-data-dir", data)
	if !errors.Is(err, errPostureFailed) || !strings.Contains(out, "inside DATA_DIR") || strings.Contains(out, strings.Repeat("cd", 32)) {
		t.Fatalf("%v\n%s", err, out)
	}
}

func TestDoctorNeedsSecurityFlag(t *testing.T) {
	cleanPostureEnv(t)
	if _, err := doctor(t); err == nil {
		t.Fatal("sin --security")
	}
}

// -url pregunta al servidor; la postura exige credenciales de administración.
func TestDoctorRemote(t *testing.T) {
	dir := cleanPostureEnv(t)
	srv := &api.Server{Auth: api.Auth{Token: "admin-tok", IngestToken: "ingest-tok"},
		Posture: func() posture.Report { return posture.Evaluate(postureFromEnv(aiConfigNone(), dir)) }}
	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	t.Setenv("TRACEREPORTS_TOKEN", "admin-tok")
	out, err := doctor(t, "--security", "-url", ts.URL)
	if err != nil || !strings.Contains(out, "API tokens") {
		t.Fatalf("%v\n%s", err, out)
	}
	t.Setenv("TRACEREPORTS_TOKEN", "ingest-tok")
	if _, err := doctor(t, "--security", "-url", ts.URL); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("el token de ingesta no lee la postura: %v", err)
	}
	res, _ := http.Get(ts.URL + "/api/v1/security/posture")
	if res.StatusCode == http.StatusOK {
		t.Fatal("sin credenciales no se lee la postura")
	}
	res.Body.Close()
}

func aiConfigNone() ai.Config { return ai.Config{Provider: ai.ProviderBuiltin} }
