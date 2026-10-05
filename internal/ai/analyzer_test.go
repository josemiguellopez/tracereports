package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestAnalyzeAsyncStoresTriage(t *testing.T) {
	var gotPrompt string
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-goog-api-key") != "test-key" {
			t.Errorf("missing api key header")
		}
		var req geminiRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		gotPrompt = req.Contents[0].Parts[0].Text
		w.Write([]byte(`{"candidates":[{"content":{"parts":[{"text":"{\"category\":\"LOCATOR_CHANGED\",\"summary\":\"El selector ya no existe.\",\"suggestion\":\"Actualizar el data-test-id.\"}"}]}}]}`))
	}))
	defer mock.Close()

	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	runID, _ := store.CreateRun("r", "")
	testID, _ := store.CreateTest(runID, "Crear tablero", "", "")
	store.AddLog(testID, "INFO", "Click en 'Crear'", 0, "")
	store.AddNetwork(testID, []db.NetConn{
		{Method: "GET", URL: "https://app/api/ok", Status: 200},
		{Method: "POST", URL: "https://app/api/boards", Status: 503, StatusText: "Service Unavailable", ResponseBody: `{"error": "db pool exhausted"}`},
	})
	store.FinishTest(testID, "FAIL", "NoSuchElementException", "trace...")

	t.Setenv("GEMINI_API_KEY", "test-key")
	t.Setenv("GEMINI_BASE_URL", mock.URL)
	a := New(store)
	a.AnalyzeAsync(testID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	a.Wait(ctx)

	got, err := store.GetTest(testID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Triage == nil || got.Triage.State != "DONE" || got.Triage.Category != "LOCATOR_CHANGED" {
		t.Fatalf("unexpected triage: %+v", got.Triage)
	}
	if !strings.Contains(gotPrompt, "Click en 'Crear'") || !strings.Contains(gotPrompt, "NoSuchElementException") {
		t.Errorf("prompt missing last step or error: %s", gotPrompt)
	}
	if !strings.Contains(gotPrompt, "POST https://app/api/boards -> HTTP 503") || !strings.Contains(gotPrompt, "db pool exhausted") ||
		strings.Contains(gotPrompt, "/api/ok") {
		t.Errorf("prompt should list only failed network calls: %s", gotPrompt)
	}
}

func TestDisabledWithoutKey(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	if New(nil).Enabled() {
		t.Fatal("analyzer should be disabled without a key")
	}
}

func TestNormalizeCategory(t *testing.T) {
	if normalizeCategory(" backend_timeout ") != "BACKEND_TIMEOUT" || normalizeCategory("weird") != "LOGIC_BUG" {
		t.Fatal("normalizeCategory mismatch")
	}
}
