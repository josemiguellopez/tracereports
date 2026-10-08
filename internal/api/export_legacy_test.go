package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Evidencia guardada antes de la redacción central (escrita directo en la base) no sale sin
// enmascarar en el ZIP: logs, error y traza, post data y bodies chicos y grandes, en data.js,
// network/test_<id>.js y network/bodies. Lo que llega por la ingesta actual, como control.
// Los .js exportados siguen siendo JSON válido y el resto del contenido se conserva.
func TestExportMasksLegacyJSONEvidence(t *testing.T) {
	const marker = "MARCADOR-FICTICIO"
	small := `{"password":"` + marker + `","ok":true,"n":12345678901234567}`
	large := `{"items":"` + strings.Repeat("z", bodyFileMinChars+500) + `","password":"` + marker + `"}`
	srv, id := newTestServer(t)
	if err := srv.Store.AddNetwork(id, []db.NetConn{
		{Method: "POST", URL: "https://fake.test/small", Status: 200, PostData: small, ResponseBody: small, BodySize: int64(len(small))},
		{Method: "POST", URL: "https://fake.test/large", Status: 200, PostData: large, ResponseBody: large, BodySize: int64(len(large))},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AddLog(id, "INFO", small, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.AddLog(id, "INFO", large, 0, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Store.FinishTest(id, "FAIL", small, large); err != nil {
		t.Fatal(err)
	}
	got, _ := srv.Store.GetTest(id)
	// control: un segundo test con la misma evidencia por la ingesta actual
	rec := call(t, srv, "POST", "/api/v1/runs/"+itoa(got.RunID)+"/tests", `{"name":"current"}`)
	var cur struct {
		ID int64 `json:"test_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &cur); err != nil || cur.ID == 0 {
		t.Fatal(rec.Code, rec.Body)
	}
	raw, _ := json.Marshal(map[string]any{"connections": []map[string]any{{"method": "POST", "url": "https://fake.test/current", "status": 200, "post_data": small, "response_body": large}}})
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(cur.ID)+"/network", string(raw)); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}
	raw, _ = json.Marshal(map[string]any{"message": small, "status": "INFO"})
	if rec := call(t, srv, "POST", "/api/v1/tests/"+itoa(cur.ID)+"/logs", string(raw)); rec.Code != 201 {
		t.Fatal(rec.Code, rec.Body)
	}

	rec = call(t, srv, "GET", "/api/v1/runs/"+itoa(got.RunID)+"/export", "")
	if rec.Code != 200 {
		t.Fatal(rec.Code, rec.Body)
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, f := range zr.File {
		name := f.Name[strings.Index(f.Name, "/")+1:]
		if name != "data.js" && !strings.HasPrefix(name, "network/") {
			continue
		}
		r, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Contains(content, []byte(marker)) {
			t.Errorf("%s keeps the legacy value", name)
		}
		var doc string
		switch {
		case name == "data.js":
			doc = strings.TrimSuffix(strings.TrimPrefix(string(content), "window.TRACEREPORTS_STATIC = "), ";\n")
			seen["data"] = true
		case strings.HasPrefix(name, "network/test_"):
			doc = strings.TrimSuffix(string(content[bytes.Index(content, []byte("] = "))+4:]), ";\n")
			seen["net"] = true
		case strings.HasPrefix(name, "network/bodies/"):
			seen["body"] = true
			continue
		default:
			continue
		}
		if !json.Valid([]byte(doc)) {
			t.Errorf("%s is not valid JSON: %.200s", name, doc)
		}
		// el resto se conserva: el texto no sensible y el número exacto dentro del string
		if !strings.Contains(doc, `\"ok\":true,\"n\":12345678901234567`) {
			t.Errorf("%s lost the non-sensitive content", name)
		}
	}
	if !seen["data"] || !seen["net"] || !seen["body"] {
		t.Fatalf("missing exported files: %v", seen)
	}
}
