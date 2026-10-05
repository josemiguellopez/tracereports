// Package live streams report changes to the web UI with Server-Sent Events (SSE).
package live

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// Event is a change the UI may react to. Types: run, test, log, network, triage, summary.
type Event struct {
	Type   string `json:"type"`
	RunID  int64  `json:"run_id"`
	TestID int64  `json:"test_id,omitempty"`
	Data   any    `json:"data,omitempty"`
}

type subscriber struct {
	ch    chan Event
	runID int64 // 0 = every run
}

// Hub fans events out to the connected browsers. Publishing never blocks: a client that
// cannot keep up loses events (the UI re-syncs from the REST API on the next one).
type Hub struct {
	mu        sync.Mutex
	subs      map[*subscriber]struct{}
	heartbeat time.Duration
}

// NewHub creates an empty hub.
func NewHub() *Hub {
	return &Hub{subs: map[*subscriber]struct{}{}, heartbeat: 20 * time.Second}
}

// Publish sends an event to every subscriber interested in its run.
func (h *Hub) Publish(e Event) {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for s := range h.subs {
		if s.runID != 0 && s.runID != e.RunID && e.Type != "run" {
			continue // los eventos "run" (nueva ejecución) llegan a todos para actualizar el selector
		}
		select {
		case s.ch <- e:
		default:
		}
	}
}

// Subscribers returns how many clients are connected.
func (h *Hub) Subscribers() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs)
}

func (h *Hub) subscribe(runID int64) *subscriber {
	s := &subscriber{ch: make(chan Event, 64), runID: runID}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) unsubscribe(s *subscriber) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// ServeHTTP is the SSE endpoint: GET /api/v1/stream[?run=<id>].
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	runID, _ := strconv.ParseInt(r.URL.Query().Get("run"), 10, 64)
	s := h.subscribe(runID)
	defer h.unsubscribe(s)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: no bufferizar el stream
	fmt.Fprint(w, "retry: 3000\n: connected\n\n")
	flusher.Flush()

	tick := time.NewTicker(h.heartbeat)
	defer tick.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-tick.C:
			fmt.Fprint(w, ": ping\n\n") // mantiene viva la conexión a través de proxies
			flusher.Flush()
		case e := <-s.ch:
			data, err := json.Marshal(e)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.Type, data)
			flusher.Flush()
		}
	}
}
