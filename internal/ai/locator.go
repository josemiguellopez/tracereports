package ai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/locator"
)

const maxLocatorCandidates = 5

// LocatorCandidates returns replacement selectors for a test that failed because an element
// was not found, using the page snapshot uploaded by the client. It returns no candidates
// when the failure is not about a locator or there is no snapshot.
func LocatorCandidates(store *db.Store, t *db.Test) ([]locator.Suggestion, string, error) {
	text := t.ErrorMessage + "\n" + t.ErrorTrace
	failed := locator.ExtractSelector(t.ErrorMessage, t.ErrorTrace)
	for i := len(t.Logs) - 1; failed == "" && i >= 0; i-- {
		if t.Logs[i].Status == "FAIL" {
			failed = locator.ExtractSelector(t.Logs[i].Message)
			text += "\n" + t.Logs[i].Message
		}
	}
	if failed == "" && !locator.LooksLikeLocatorFailure(text) {
		return nil, "", nil
	}
	raw, err := store.GetDOM(t.ID)
	if err != nil || raw == "" {
		return nil, failed, err
	}
	var snap locator.Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, failed, nil // snapshot inválido: sin sugerencias, no es un error del análisis
	}
	return locator.Suggest(failed, snap, maxLocatorCandidates), failed, nil
}

func locatorPromptSection(failed string, cands []locator.Suggestion) string {
	if len(cands) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nThe failure looks like a broken locator (the UI changed). Failed selector: %s\n", failed)
	b.WriteString("Candidate elements found on the page at the moment of the failure (with a robust selector each):\n")
	for i, c := range cands {
		fmt.Fprintf(&b, "%d. %s -> %s (strategy: %s)\n", i+1, c.Element, c.Python, c.Kind)
	}
	b.WriteString(`If one of them is clearly the element the test meant, set "recommended_locator" to its Playwright
code exactly as listed and explain why in "locator_reason" (one sentence). Prefer role/test-id strategies.`)
	return b.String()
}
