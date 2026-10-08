package api

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// sendBody stores one connection and returns it as stored and as the API lists it.
func sendBody(t *testing.T, body string, clientCut bool) (db.NetConn, map[string]any) {
	t.Helper()
	srv, id := newTestServer(t)
	conn := map[string]any{"method": "GET", "url": "https://example.test/api", "status": 200, "response_body": body}
	if clientCut {
		conn["body_truncated"] = true
		conn["body_size"] = 900000
	}
	raw, _ := json.Marshal(map[string]any{"connections": []map[string]any{conn}})
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(id)+"/network", string(raw)); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	stored, err := srv.Store.ListNetwork(id)
	if err != nil || len(stored) != 1 {
		t.Fatal(err)
	}
	var listed []map[string]any
	rec := call(t, srv, "GET", "/api/v1/tests/"+itoa(id)+"/network", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil || len(listed) != 1 {
		var wrapped struct{ Connections []map[string]any }
		json.Unmarshal(rec.Body.Bytes(), &wrapped)
		listed = wrapped.Connections
	}
	if len(listed) != 1 {
		t.Fatalf("listed: %s", rec.Body)
	}
	return stored[0], listed[0]
}

func checkFlag(t *testing.T, name string, c db.NetConn, listed map[string]any, want bool, size int) {
	t.Helper()
	if c.BodyTruncated != want || listed["body_truncated"] != want {
		t.Fatalf("%s: body_truncated stored %v, listed %v, want %v", name, c.BodyTruncated, listed["body_truncated"], want)
	}
	if size > 0 && c.BodySize != int64(size) {
		t.Fatalf("%s: body_size %d, want the original %d", name, c.BodySize, size)
	}
}

// Un descarte por tamaño (aunque la redacción achique después lo que queda) deja el body marcado
// como recortado; enmascarar sin descartar nada no lo marca.
func TestBodyTruncatedReportsEveryDiscard(t *testing.T) {
	// recorte previo que descarta la cola no sensible; la redacción deja muy poco
	big := `{"token":"` + strings.Repeat("x", maxResponseBodyChars+redactSlack+1000) + `","result":"important tail"}`
	c, l := sendBody(t, big, false)
	checkFlag(t, "pre-cut", c, l, true, len(big))
	if strings.Contains(c.ResponseBody, "important tail") || strings.Contains(c.ResponseBody, "xxxxxxxx") {
		t.Fatalf("pre-cut: %.80s", c.ResponseBody)
	}

	// entre el límite y el recorte previo: la redacción lo deja bajo el límite sin descartar nada
	fits := `{"token":"` + strings.Repeat("x", maxResponseBodyChars) + `","result":"important tail"}`
	c, l = sendBody(t, fits, false)
	checkFlag(t, "masked below the limit", c, l, false, len(fits))
	if !strings.Contains(c.ResponseBody, "important tail") {
		t.Fatalf("nothing discarded: %q", c.ResponseBody)
	}

	// recorte final normal
	plain := strings.Repeat("y", maxResponseBodyChars+10)
	c, l = sendBody(t, plain, false)
	checkFlag(t, "final cut", c, l, true, len(plain))
	if len(c.ResponseBody) != maxResponseBodyChars {
		t.Fatalf("final cut: %d", len(c.ResponseBody))
	}

	// dentro del límite, enmascarado
	small := `{"password":"hunter2","ok":true}`
	c, l = sendBody(t, small, false)
	checkFlag(t, "masked within the limit", c, l, false, len(small))
	if strings.Contains(c.ResponseBody, "hunter2") {
		t.Fatal("masked")
	}

	// el cliente ya lo recortó: se respeta, con su tamaño original
	c, l = sendBody(t, `{"items":[1,2`, true)
	checkFlag(t, "client cut", c, l, true, 900000)
}

// Con el límite de almacenamiento en cero no se guarda ningún body: si había uno, queda marcado.
func TestBodyTruncatedWithZeroLimit(t *testing.T) {
	prev := maxResponseBodyChars
	maxResponseBodyChars = 0
	defer func() { maxResponseBodyChars = prev }()
	c, l := sendBody(t, `{"a":1}`, false)
	checkFlag(t, "zero limit", c, l, true, 7)
	if c.ResponseBody != "" {
		t.Fatalf("no body stored: %q", c.ResponseBody)
	}
	c, l = sendBody(t, "", false)
	checkFlag(t, "zero limit, empty body", c, l, false, 0)
}
