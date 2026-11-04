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
