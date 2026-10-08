package api

import (
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/repro"
)

// Un header aceptado y guardado por la API: el cURL del detalle técnico lo deja literal.
func TestStoredHeaderAndMethodStayLiteralInCurl(t *testing.T) {
	srv, id := newTestServer(t)
	body := "{\"connections\":[{\"method\":\"GET|id\",\"url\":\"https://example.test/api\",\"status\":503,\"request_headers\":{\"X-Auth-`id`\":\"<masked>\"}}]}"
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/network", body); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	conns, err := srv.Store.ListNetwork(id)
	if err != nil || len(conns) != 1 {
		t.Fatal(err, conns)
	}
	cmd := repro.Curl(&conns[0], srv.redactor())
	if strings.Contains(cmd, "-H \"X-Auth-`id`") || strings.Contains(cmd, "-X GET|ID") {
		t.Fatalf("stored values reach the shell:\n%s", cmd)
	}
	if !strings.Contains(cmd, "-H 'X-Auth-`id`: '\"$X_AUTH_ID_\"") || !strings.Contains(cmd, "-X 'GET|ID'") {
		t.Fatalf("literal forms:\n%s", cmd)
	}
}
