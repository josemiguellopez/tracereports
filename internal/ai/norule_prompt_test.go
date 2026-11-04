package ai

import (
	"strings"
	"testing"
)

// Un triage del motor local sin regla que concluya no llega al LLM como consejo: si no, el chat repite
// "configura un proveedor de IA" en vez de responder lo que se le preguntó.
func TestPromptBlockSkipsInconclusiveTriage(t *testing.T) {
	f := &Facts{TestName: "seleccionar cliente", Error: "TimeoutException", Category: "LOGIC",
		AISummary: "Ninguna regla explica el fallo.", AISuggest: "configura un proveedor de IA", NoRule: true}
	got := f.promptBlock("test")
	if strings.Contains(got, "proveedor de IA") || !strings.Contains(got, "inconclusive") {
		t.Errorf("prompt with an inconclusive triage:\n%s", got)
	}
	f.NoRule = false
	if got := f.promptBlock("test"); !strings.Contains(got, "configura un proveedor de IA") {
		t.Errorf("a conclusive triage must reach the model:\n%s", got)
	}
}

// Un modelo chico a veces copia la conversación antes de responder: llega solo la respuesta.
func TestChatAnswerDropsTheCopiedConversation(t *testing.T) {
	cases := map[string]string{
		"User: mejora este xpath //a\n\nTrace Buddy: Usa contains:\n//a[contains(., 'x')]": "Usa contains:\n//a[contains(., 'x')]",
		"Trace Buddy: listo":                    "listo",
		"  ```css\n.btn\n```\nMás corto.  ":     "```css\n.btn\n```\nMás corto.",
		"User: hola (sin respuesta del modelo)": "User: hola (sin respuesta del modelo)",
	}
	for in, want := range cases {
		if got := chatAnswer(in); got != want {
			t.Errorf("chatAnswer(%q) = %q, want %q", in, got, want)
		}
	}
}
