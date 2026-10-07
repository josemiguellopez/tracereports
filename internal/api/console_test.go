package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestConsoleIsStoredMaskedAndCapped(t *testing.T) {
	clearAIEnv(t)
	srv, testID := newTestServer(t)
	body := `{"entries":[
		{"level":"error","text":"Failed to load resource: 500","location":"https://app/main.js:10:5","timestamp":1790000000000},
		{"level":"warn","text":"deprecated API"},
		{"level":"pageerror","text":"TypeError: cannot read 'total' of undefined; token=CONSOLESECRET"}]}`
	rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(testID)+"/console", body)
	if rec.Code != 201 || !strings.Contains(rec.Body.String(), `"stored":3`) {
		t.Fatalf("store: %d %s", rec.Code, rec.Body)
	}
	var tt db.Test
	json.Unmarshal(call(t, srv, "GET", "/api/v1/tests/"+itoa(testID), "").Body.Bytes(), &tt)
	if len(tt.Console) != 3 || tt.Console[1].Level != "warning" || tt.Console[0].Location != "https://app/main.js:10:5" || tt.Console[0].Timestamp != 1790000000000 {
		t.Fatalf("console: %+v", tt.Console)
	}
	if strings.Contains(tt.Console[2].Text, "CONSOLESECRET") {
		t.Fatal("console text must be masked")
	}
	if tt.ConsoleErrors != 2 { // error + pageerror; el warning no cuenta
		t.Fatalf("console errors: %d", tt.ConsoleErrors)
	}

	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(testID)+"/console", `{"entries":[{"level":"shout","text":"x"}]}`); rec.Code != 400 {
		t.Fatalf("unknown level: %d", rec.Code)
	}
	if rec := call(t, srv, "POST", "/api/v1/tests/99999/console", `{"entries":[]}`); rec.Code != 404 {
		t.Fatalf("unknown test: %d", rec.Code)
	}

	// una página que loguea en un bucle no llena la base
	var many []map[string]string
	for i := 0; i < 600; i++ {
		many = append(many, map[string]string{"level": "log", "text": "tick"})
	}
	raw, _ := json.Marshal(map[string]any{"entries": many})
	rec = call(t, srv, "POST", "/api/v1/tests/"+itoa(testID)+"/console", string(raw))
	if !strings.Contains(rec.Body.String(), `"stored":497`) || !strings.Contains(rec.Body.String(), `"dropped":103`) {
		t.Fatalf("cap at 500 per test: %s", rec.Body)
	}
}
