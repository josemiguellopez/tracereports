package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Un lote con bodies grandes: después de leerlo solo queda en memoria lo que se guarda (el body
// recortado), no el body completo de cada conexión.
func TestNetworkBatchDoesNotRetainFullBodies(t *testing.T) {
	const conns, bodySize = 30, 1 << 20 // 30 MiB de bodies; se guardan 256 KB de cada uno
	var buf bytes.Buffer
	buf.WriteString(`{"connections":[`)
	for i := 0; i < conns; i++ {
		if i > 0 {
			buf.WriteByte(',')
		}
		fmt.Fprintf(&buf, `{"method":"GET","url":"https://x/%d","status":200,"response_body":"%s"}`, i, strings.Repeat("b", bodySize))
	}
	buf.WriteString(`]}`)
	body := buf.Bytes()

	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	var kept []db.NetConn
	err := decodeConnections(bytes.NewReader(body), func(c db.NetConn) error {
		normalizeConn(&c)
		kept = append(kept, c)
		return nil
	})
	runtime.GC()
	runtime.ReadMemStats(&after)
	if err != nil || len(kept) != conns || !kept[0].BodyTruncated || len(kept[0].ResponseBody) != maxResponseBodyChars {
		t.Fatalf("decode: %v n=%d", err, len(kept))
	}
	retained := int64(after.HeapAlloc) - int64(before.HeapAlloc)
	if retained > 16<<20 {
		t.Fatalf("retained %d MiB after reading 30 MiB of bodies cut to 256 KB: the full bodies are still referenced", retained>>20)
	}
	runtime.KeepAlive(kept)
	runtime.KeepAlive(body) // el request sigue vivo durante la medición, como en el handler
}

func TestNetworkBatchLimitsAndShapes(t *testing.T) {
	srv, testID := newTestServer(t)
	path := fmt.Sprintf("/api/v1/tests/%d/network", testID)
	post := func(body string) (int, map[string]any) {
		rec := call(t, srv, "POST", path, body)
		var out map[string]any
		json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, out
	}
	if code, out := post(`{"connections":[{"method":"get","url":"https://a/1","status":503}],"client":"py"}`); code != 201 || out["stored"] != 1.0 || out["errors"] != 1.0 {
		t.Fatalf("valid batch with another key: %d %v", code, out)
	}
	if code, out := post(`{"connections":null}`); code != 201 || out["stored"] != 0.0 {
		t.Fatalf("null: %d %v", code, out)
	}
	if code, _ := post(`{"connections":{"method":"GET"}}`); code != 400 {
		t.Fatalf("connections must be an array: %d", code)
	}
	if code, _ := post(`{"connections":[{"method":"GET",}]}`); code != 400 {
		t.Fatalf("invalid JSON: %d", code)
	}
	many := `{"connections":[` + strings.TrimSuffix(strings.Repeat(`{"url":"u"},`, maxNetworkConns+1), ",") + `]}`
	if code, _ := post(many); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("too many: %d", code)
	}
	got, _ := srv.Store.ListNetwork(testID)
	if len(got) != 1 || got[0].Method != "GET" {
		t.Fatalf("only the valid batch is stored: %+v", got)
	}
}

func TestNetworkBatchWaitsForASlot(t *testing.T) {
	srv, testID := newTestServer(t)
	for i := 0; i < cap(networkSlots); i++ {
		networkSlots <- struct{}{}
	}
	defer func() {
		for i := 0; i < cap(networkSlots); i++ {
			<-networkSlots
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/tests/%d/network", testID), strings.NewReader(`{"connections":[{"url":"u"}]}`)).WithContext(ctx)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Header.Set("Content-Type", "application/json")
	done := make(chan struct{})
	go func() { srv.Router().ServeHTTP(httptest.NewRecorder(), req); close(done) }()
	cancel()
	<-done
	if got, _ := srv.Store.ListNetwork(testID); len(got) != 0 {
		t.Fatal("a batch that never got a slot is not stored")
	}
}
