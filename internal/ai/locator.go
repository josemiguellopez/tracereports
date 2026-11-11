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
	failed, isLocator := failedSelector(t)
	if !isLocator {
		return nil, "", nil
	}
	// el elemento no apareció porque el backend falló justo antes (sin respuesta, 5xx, 408, 429):
	// no es un selector roto, y sugerir otro sería engañoso. Es la misma regla del incidente
	if c, err := backendCause(store, t); err != nil || c != nil {
		return nil, "", err
	}
	raw, err := store.GetDOM(t.ID)
	if err != nil || raw == "" {
		return nil, failed, err
	}
	var snap locator.Snapshot
	if err := json.Unmarshal([]byte(raw), &snap); err != nil {
		return nil, failed, nil // snapshot inválido: sin sugerencias, no es un error del análisis
	}
	if locator.Truncated(failed) {
		return nil, failed, nil // medio selector: compararlo con la página daría reemplazos de otra cosa
	}
	return locator.Suggest(failed, snap, maxLocatorCandidates), failed, nil
}

// failedSelector: the selector that failed (from the error, or the last FAIL step) and whether the
// failure is about an element not found at all.
func failedSelector(t *db.Test) (string, bool) {
	text := t.ErrorMessage + "\n" + t.ErrorTrace
	failed := locator.ExtractSelector(t.ErrorMessage, t.ErrorTrace)
	for i := len(t.Logs) - 1; failed == "" && i >= 0; i-- {
		if t.Logs[i].Status == "FAIL" {
			failed = locator.ExtractSelector(t.Logs[i].Message)
			text += "\n" + t.Logs[i].Message
		}
	}
	failed = completeSelector(failed, t)
	return failed, failed != "" || locator.LooksLikeLocatorFailure(text)
}

// backendCause: the backend call that failed just before (no answer, 5xx, 408, 429), or nil.
func backendCause(store *db.Store, t *db.Test) (*db.NetConn, error) {
	conns, err := store.NetworkTimeline(t.ID)
	if err != nil {
		return nil, err
	}
	if c := causalConn(conns, failureTime(t)); c != nil && strongNetError(*c) {
		return c, nil
	}
	return nil, nil
}

// LocatorBackendCause: for an element-not-found failure that LocatorCandidates leaves without
// recommendation because the backend failed first, the selector and that call. The Inspector
// shows it instead of "the selector could not be extracted"; there are still no replacements.
func LocatorBackendCause(store *db.Store, t *db.Test) (string, *db.NetConn, error) {
	failed, isLocator := failedSelector(t)
	if !isLocator {
		return "", nil, nil
	}
	c, err := backendCause(store, t)
	if err != nil || c == nil {
		return "", nil, err
	}
	return failed, c, nil
}

// completeSelector: error_message is stored cut (300 characters) and a long XPath can end halfway.
// The same selector, whole, is usually in the trace or in the step that failed.
func completeSelector(failed string, t *db.Test) string {
	if failed == "" || !locator.Truncated(failed) {
		return failed
	}
	texts := []string{t.ErrorTrace}
	for i := len(t.Logs) - 1; i >= 0; i-- {
		if t.Logs[i].Status == "FAIL" {
			texts = append(texts, t.Logs[i].Message)
		}
	}
	for _, text := range texts {
		whole := locator.ExtractSelector(text)
		if len(whole) > len(failed) && strings.HasPrefix(whole, failed) && !locator.Truncated(whole) {
			return whole
		}
	}
	return failed
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
