package api

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/notify"
)

func TestWeeklySummaryPreviewAndSend(t *testing.T) {
	clearAIEnv(t)
	posts := 0
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.ReadAll(r.Body)
		posts++
	}))
	defer hook.Close()
	t.Setenv("SLACK_WEBHOOK_URL", hook.URL)
	t.Setenv("TEAMS_WEBHOOK_URL", "")
	srv, _ := newTestServer(t)
	srv.Notify = notify.New(srv.Store)
	runWith(t, srv, "shop") // una ejecución con un fallo, de hoy

	var out struct {
		Summary notify.Escalation `json:"summary"`
		Sent    []string          `json:"sent"`
	}
	rec := call(t, srv, "POST", "/api/v1/ui/summary/weekly", `{"lang":"en"}`)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil {
		t.Fatalf("preview: %d %s", rec.Code, rec.Body)
	}
	if out.Summary.Title != "Weekly test summary" || !strings.Contains(out.Summary.Headline, "50%") || len(out.Sent) != 0 || posts != 0 {
		t.Fatalf("preview does not send: %+v %d posts", out, posts)
	}
	rec = call(t, srv, "POST", "/api/v1/ui/summary/weekly", `{"send":true}`)
	json.Unmarshal(rec.Body.Bytes(), &out)
	if rec.Code != 200 || len(out.Sent) != 1 || out.Sent[0] != "slack" || posts != 1 || out.Summary.Title != "Resumen semanal de pruebas" {
		t.Fatalf("send (default language es): %d %s, %d posts", rec.Code, rec.Body, posts)
	}
	if rec := call(t, srv, "POST", "/api/v1/ui/summary/weekly", `{}`, fromAddr("10.0.0.9:1")); rec.Code != 403 {
		t.Fatalf("remote without login: %d", rec.Code)
	}
}
