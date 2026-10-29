package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/repro"
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

// chatPerHour limits the free questions to Trace Buddy per server: each one spends the team's AI.
var chatPerHour = 60

// chatLimiter counts the questions of the last hour.
type chatLimiter struct {
	mu   sync.Mutex
	sent []time.Time
}

func (l *chatLimiter) allow(now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	keep := l.sent[:0]
	for _, t := range l.sent {
		if now.Sub(t) < time.Hour {
			keep = append(keep, t)
		}
	}
	l.sent = keep
	if len(l.sent) >= chatPerHour {
		return false
	}
	l.sent = append(l.sent, now)
	return true
}

// aiChat answers a free question in Trace Buddy with the evidence of the run or test being viewed.
// Admin only (it spends the team's AI key); needs an LLM; the prompt goes through the redaction.
func (s *Server) aiChat(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	var in struct {
		RunID    int64            `json:"run_id"`
		TestID   int64            `json:"test_id"`
		Lang     string           `json:"lang"`
		Messages []ai.ChatMessage `json:"messages"`
		Tools    []string         `json:"tools"` // lo que la web mostrará bajo la respuesta: curl, mock
	}
	if !decode(w, r, &in) {
		return
	}
	msgs, err := ai.CleanChat(in.Messages)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.AI.UsesLLM() {
		writeJSON(w, http.StatusConflict, map[string]any{"error": ai.ErrChatNeedsLLM.Error(), "code": "needs_llm"})
		return
	}
	var facts *ai.Facts
	var extra ai.ChatExtra
	var curl string // el de la primera llamada que falló: la web lo muestra con "Copiar" si se lo pidieron
	scope := "run"
	if in.RunID > 0 {
		if facts, err = ai.BuildFacts(s.Store, s.shots(), in.RunID, in.TestID); respondErr(w, err, "run or test") {
			return
		}
		if in.TestID > 0 {
			scope = "test"
			if extra.Calls, curl, err = s.chatCalls(in.TestID); err != nil {
				serverError(w, err)
				return
			}
		}
	}
	for _, t := range in.Tools { // lo que la web va a mostrar bajo la respuesta (armado del reporte, no por el modelo)
		if (t == "curl" && curl != "") || t == "mock" {
			extra.Shown = append(extra.Shown, map[string]string{"curl": "the cURL of the failed call, ready for Postman", "mock": "a mock of the failed backend"}[t])
		}
	}
	if !s.chat.allow(time.Now()) {
		writeError(w, http.StatusTooManyRequests, fmt.Sprintf("Trace Buddy answers up to %d questions per hour on this server", chatPerHour))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	answer, err := s.AI.Chat(ctx, facts, scope, in.Lang, msgs, extra)
	if err != nil {
		msg := s.redactor().Text(err.Error())
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "the provider did not answer in 2 minutes"
		}
		hint, _ := ai.Hint(err, s.AI.Config())
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": msg, "hint": hint})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"answer": answer, "provider": s.AI.Provider(), "model": s.AI.Model(), "curl": curl})
}

// chatCalls are the failed calls of a test for the chat: each as cURL (repro.Curl: secrets become
// $VARIABLES or ***) with the start of its response, also masked. curl is the first one's command.
func (s *Server) chatCalls(testID int64) (calls, curl string, err error) {
	conns, err := s.Store.ListNetworkErrors(testID, chatMaxCalls)
	if err != nil {
		return "", "", err
	}
	policy := s.redactor()
	var b strings.Builder
	for i := range conns {
		c := &conns[i]
		cmd := repro.Curl(c, policy)
		if i == 0 {
			curl = cmd
		}
		outcome := fmt.Sprintf("HTTP %d", c.Status)
		if c.Failed || c.Status == 0 {
			outcome = "no response (" + c.ErrorText + ")"
		}
		fmt.Fprintf(&b, "%s\n→ %s. Response: %s\n", cmd, outcome, clipText(strings.Join(strings.Fields(policy.Text(c.ResponseBody)), " "), 400))
	}
	return b.String(), curl, nil
}

// chatMaxCalls: the failed calls of a test that go to the model (the first ones are the cause).
const chatMaxCalls = 3
