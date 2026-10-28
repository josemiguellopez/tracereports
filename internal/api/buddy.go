package api

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

// probeTTL keeps a probe answer for a short while: opening and closing Trace Buddy, or several
// admins at once, do not pay one provider call each.
const probeTTL = 20 * time.Second

// probeCache is the last answer of aiProbe, for the configuration it was made with.
type probeCache struct {
	mu   sync.Mutex
	at   time.Time
	key  string
	body map[string]any
}

// aiProbe tells Trace Buddy whether the AI configured right now answers: the active
// configuration (not the one being edited in Settings, so it also works with Settings locked),
// with one tiny fixed request. Admin only: it spends the team's AI key.
func (s *Server) aiProbe(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	c := s.AI.Config()
	if !s.AI.Enabled() {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "state": "off"})
		return
	}
	if c.Provider == ai.ProviderBuiltin {
		// dentro del binario: no hay conexión que probar ni nada que gastar
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "state": "local", "provider": c.Provider, "model": s.AI.Model()})
		return
	}
	key := c.Provider + "\x00" + c.Model + "\x00" + c.BaseURL
	force := r.URL.Query().Get("force") == "1"
	s.probe.mu.Lock()
	defer s.probe.mu.Unlock() // una prueba a la vez: las demás esperan y reciben la misma respuesta
	if !force && s.probe.key == key && time.Since(s.probe.at) < probeTTL {
		writeJSON(w, http.StatusOK, s.probe.body)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	took, err := s.AI.Test(ctx, c)
	body := map[string]any{"ok": err == nil, "state": "llm", "provider": c.Provider, "model": s.AI.Model()}
	if err != nil {
		msg := s.redactor().Text(err.Error()) // la key ya viene quitada (ai.call); además, la política
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "the provider did not answer in 30 s"
		}
		hint, suggest := ai.Hint(err, c)
		body["error"], body["hint"], body["suggest"] = msg, hint, suggest
	} else {
		body["ms"] = took.Milliseconds()
	}
	s.probe.at, s.probe.key, s.probe.body = time.Now(), key, body
	writeJSON(w, http.StatusOK, body)
}
