package ai

import (
	"errors"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/redact"
)

// A provider (or a proxy in front of it) may repeat the credential in its error: "Incorrect API
// key provided: sk-…", the Authorization header, a URL with ?api-key=…. Those errors are logged,
// stored next to the test (ai_triage.error), in the run summary and returned to the UI (escalation,
// Test connection), so every error leaving call() has the key of THAT request removed: the one in
// the Config the request used, not the one configured now.

const keyMask = "[api key]"

// scrubKey removes the key (in every form) from s (redact.ScrubCredentials).
func scrubKey(s, key string) string { return redact.ScrubCredentials(s, keyMask, key) }

// scrubError returns err without the key used by the request. A provider HTTP error keeps its
// type (status code, retry and hint classification) with its body scrubbed.
func scrubError(err error, key string) error {
	if err == nil || len(strings.TrimSpace(key)) < 6 {
		return err
	}
	var se *httpStatusError
	if errors.As(err, &se) {
		return &httpStatusError{provider: se.provider, code: se.code, body: scrubKey(se.body, key)}
	}
	return redact.ScrubCredentialError(err, keyMask, key)
}
