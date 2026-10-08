package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/locator"
)

// Cada campo de texto del snapshot pasa por la misma redacción antes de guardarse, y el secreto
// tampoco reaparece en las sugerencias de locators.
func TestDOMSnapshotRedactsEveryTextField(t *testing.T) {
	srv, id := newTestServer(t)
	srv.Store.FinishTest(id, "FAIL", `TimeoutError: waiting for locator("#old-login")`, "")
	const secret = "AUDIT_SECRET_7f3a"
	fields := map[string]string{
		"tag": "button", "id": "token=" + secret, "name": "api_key=" + secret, "role": "button",
		"testid": "token=" + secret, "testid_attr": "data-testid", "label": "token=" + secret,
		"placeholder": "secret=" + secret, "type": "password=" + secret, "text": "password=" + secret,
	}
	el, _ := json.Marshal(map[string]any{"tag": fields["tag"], "id": fields["id"], "name": fields["name"], "role": fields["role"],
		"testid": fields["testid"], "testid_attr": fields["testid_attr"], "label": fields["label"], "placeholder": fields["placeholder"],
		"type": fields["type"], "text": fields["text"], "visible": true, "w": 80, "h": 20})
	body := `{"url":"https://app.test/login?token=` + secret + `","title":"t","elements":[` + string(el) + `]}`
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/dom", body); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	raw, err := srv.Store.GetDOM(id)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, secret) {
		t.Fatalf("a recognized secret was stored: %s", raw)
	}
	var snap locator.Snapshot
	json.Unmarshal([]byte(raw), &snap)
	e := snap.Elements[0]
	for name, got := range map[string]string{"id": e.ID, "name": e.Name, "testid": e.TestID, "label": e.Label,
		"placeholder": e.Placeholder, "type": e.Type, "text": e.Text} {
		if got == "" || strings.Contains(got, secret) {
			t.Errorf("%s: %q", name, got)
		}
	}
	// lo que no es secreto queda igual
	if e.Tag != "button" || e.Role != "button" || e.TestIDAttr != "data-testid" {
		t.Errorf("plain fields changed: %+v", e)
	}
	// ni en las sugerencias
	if rec := call(t, srv, "GET", "/api/v1/tests/"+itoa(id)+"/locator", ""); strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("secret in the locator suggestions: %s", rec.Body)
	}
}

func TestDOMSnapshotKeepsNormalValuesAndLimits(t *testing.T) {
	srv, id := newTestServer(t)
	long := strings.Repeat("x", 5000)
	body := `{"elements":[{"tag":"input","id":"email","name":"email","testid":"login-email","label":"Email","placeholder":"you@example.com","type":"email","text":"","visible":true},` +
		`{"tag":"div","id":"` + long + `","testid":"` + long + `","label":"` + long + `","text":"` + long + `","visible":true}]}`
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/dom", body); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	raw, _ := srv.Store.GetDOM(id)
	var snap locator.Snapshot
	json.Unmarshal([]byte(raw), &snap)
	n := snap.Elements[0]
	if n.ID != "email" || n.Name != "email" || n.TestID != "login-email" || n.Label != "Email" || n.Placeholder != "you@example.com" || n.Type != "email" {
		t.Fatalf("normal values must be kept: %+v", n)
	}
	b := snap.Elements[1]
	for name, v := range map[string]string{"id": b.ID, "testid": b.TestID, "label": b.Label, "text": b.Text} {
		if len(v) > 300 {
			t.Errorf("%s not bounded: %d bytes", name, len(v))
		}
	}
}
