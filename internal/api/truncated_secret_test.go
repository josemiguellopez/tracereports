package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/redact"
)

const truncSecret = "TRUNC_SECRET_SENTINEL"

// labelAround builds a JSON-ish text of exactly n+extra bytes whose sensitive value ends `extra`
// bytes after the cut at n (negative: before it).
func labelAround(n, extra int) string {
	open, tail := `{"token":"`, `"}`
	pad := n + extra - len(open) - len(truncSecret) - len(tail)
	return strings.Repeat("x", pad) + open + truncSecret + tail
}

func noSecret(t *testing.T, where, s string) {
	t.Helper()
	if strings.Contains(s, truncSecret) || strings.Contains(s, truncSecret[:10]) {
		t.Fatalf("%s keeps the secret: %.300s", where, s[max(0, len(s)-300):])
	}
}

// El recorte del servidor no puede dejar un secreto sin enmascarar: DOM, red y consola, en lo
// guardado y en lo que la API devuelve. Límites antes, justo en y después del valor.
func TestTruncationNeverKeepsARecognizedSecret(t *testing.T) {
	for _, extra := range []int{-3, 0, 1, 2, 5, 12, 40} {
		srv, id := newTestServer(t)
		// DOM: label de 120 bytes
		label := labelAround(120, extra)
		raw, _ := json.Marshal(map[string]any{"elements": []map[string]any{{"tag": "button", "label": label, "visible": true, "w": 100, "h": 20}}})
		if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/dom", string(raw)); rec.Code != 201 {
			t.Fatal(rec.Code, rec.Body)
		}
		stored, err := srv.Store.GetDOM(id)
		if err != nil {
			t.Fatal(err)
		}
		noSecret(t, "stored DOM", stored)
		// red: body de 256 KB, recortado por el servidor
		body := `{"padding":"` + strings.Repeat("y", maxResponseBodyChars) + `"}`
		body = body[:maxResponseBodyChars+extra-len(`","token":"`+truncSecret+`"}`)] + `","token":"` + truncSecret + `"}`
		raw, _ = json.Marshal(map[string]any{"connections": []map[string]any{{"method": "GET", "url": "https://example.test/api", "status": 200, "response_body": body}}})
		if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/network", string(raw)); rec.Code != 201 {
			t.Fatal(rec.Code, rec.Body)
		}
		conns, _ := srv.Store.ListNetwork(id)
		noSecret(t, "stored body", conns[0].ResponseBody)
		noSecret(t, "body endpoint", call(t, srv, "GET", "/api/v1/network/"+itoa(conns[0].ID)+"/body", "").Body.String())
		// consola: texto de 4000 bytes
		raw, _ = json.Marshal(map[string]any{"entries": []map[string]any{{"level": "error", "text": labelAround(4000, extra)}}})
		if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/console", string(raw)); rec.Code != 201 {
			t.Fatal(rec.Code, rec.Body)
		}
		entries, _ := srv.Store.ConsoleOf(id)
		noSecret(t, "stored console", entries[0].Text)
		noSecret(t, "test endpoint", call(t, srv, "GET", "/api/v1/tests/"+itoa(id), "").Body.String())
	}
}

// Lo que el cliente ya recortó llega sin la comilla de cierre: se enmascara igual. Lo no sensible
// recortado se guarda tal cual.
func TestClientTruncatedContentIsMaskedToo(t *testing.T) {
	srv, id := newTestServer(t)
	cut := `{"padding":"` + strings.Repeat("é", 1000) + `","token":"` + truncSecret + `-and-more`
	plain := `{"items":[1,2,3],"note":"un texto largo ñ que se cor`
	raw, _ := json.Marshal(map[string]any{"connections": []map[string]any{
		{"method": "GET", "url": "https://example.test/a", "status": 200, "response_body": cut, "body_truncated": true, "body_size": 900000},
		{"method": "GET", "url": "https://example.test/b", "status": 200, "response_body": plain, "body_truncated": true},
	}})
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/network", string(raw)); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	conns, _ := srv.Store.ListNetwork(id)
	noSecret(t, "client-cut body", conns[0].ResponseBody)
	if !strings.HasPrefix(conns[0].ResponseBody, `{"padding":"éé`) || !conns[0].BodyTruncated {
		t.Errorf("the rest is kept: %.40s %v", conns[0].ResponseBody, conns[0].BodyTruncated)
	}
	if conns[1].ResponseBody != plain {
		t.Errorf("non-sensitive cut body changed: %q", conns[1].ResponseBody)
	}
	raw, _ = json.Marshal(map[string]any{"elements": []map[string]any{{"tag": "input", "label": `{"password":"` + truncSecret, "placeholder": "Correo electrónico", "visible": true, "w": 10, "h": 10}}})
	call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/dom", string(raw))
	stored, _ := srv.Store.GetDOM(id)
	noSecret(t, "client-cut DOM", stored)
	if !strings.Contains(stored, "Correo electrónico") {
		t.Errorf("non-sensitive fields kept: %s", stored)
	}
}

// Una clave larga configurada como sensible (TRACEREPORTS_REDACT_KEYS) se enmascara en la ingesta
// de red: lo guardado y lo que devuelve la API no traen el valor.
func TestIngestMasksALongConfiguredKey(t *testing.T) {
	key := strings.Repeat("long-field-", 9) + "token"
	const marker = "FAKE-CURRENT-PRIVATE-VALUE-12345"
	t.Setenv("TRACEREPORTS_REDACT_KEYS", key)
	srv, id := newTestServer(t)
	srv.Redact = redact.FromEnv()
	body := `{"user":"ana","` + key + `":"` + marker + `","nested":{"` + key + `":{"v":"` + marker + `"}}}`
	raw, _ := json.Marshal(map[string]any{"connections": []map[string]any{{"method": "POST", "url": "https://fake.test/api", "status": 200,
		"response_body": body, "post_data": body}}})
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/network", string(raw)); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	cs, _ := srv.Store.ListNetwork(id)
	noSecretIn := func(where, s string) {
		if strings.Contains(s, marker) {
			t.Fatalf("%s keeps the value: %.200s", where, s)
		}
	}
	noSecretIn("stored body", cs[0].ResponseBody)
	noSecretIn("stored post data", cs[0].PostData)
	noSecretIn("network list", call(t, srv, "GET", "/api/v1/tests/"+itoa(id)+"/network", "").Body.String())
	if !strings.Contains(cs[0].ResponseBody, `"user":"ana"`) {
		t.Fatalf("the rest is kept: %s", cs[0].ResponseBody)
	}
}
