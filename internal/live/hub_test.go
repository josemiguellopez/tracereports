package live

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamFiltersByRun(t *testing.T) {
	h := NewHub()
	srv := httptest.NewServer(h)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"?run=7", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content type: %s", ct)
	}
	for h.Subscribers() == 0 {
		time.Sleep(10 * time.Millisecond)
	}

	h.Publish(Event{Type: "log", RunID: 8, TestID: 1})                                             // otra ejecución: filtrado
	h.Publish(Event{Type: "log", RunID: 7, TestID: 3, Data: map[string]string{"message": "paso"}}) // llega
	h.Publish(Event{Type: "run", RunID: 9})                                                        // "run" llega siempre

	sc := bufio.NewScanner(resp.Body)
	var events []string
	for sc.Scan() && len(events) < 2 {
		if line := sc.Text(); strings.HasPrefix(line, "data: ") {
			events = append(events, line)
		}
	}
	if len(events) != 2 || !strings.Contains(events[0], `"test_id":3`) || !strings.Contains(events[0], `"message":"paso"`) ||
		!strings.Contains(events[1], `"type":"run"`) {
		t.Fatalf("events: %v", events)
	}
}

func TestPublishNeverBlocks(t *testing.T) {
	h := NewHub()
	s := h.subscribe(0)
	defer h.unsubscribe(s)
	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ { // más que el buffer: no debe bloquear
			h.Publish(Event{Type: "log", RunID: 1})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a slow subscriber")
	}
	var nilHub *Hub
	nilHub.Publish(Event{Type: "log"}) // sin hub: no-op
}
