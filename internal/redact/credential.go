package redact

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
)

// A provider (or a proxy in front of it) may repeat the credential of a request in its error:
// "Incorrect API key provided: sk-…", the Authorization header, a URL with ?api-key=…, a Basic
// "user:token" in base64. The policy above recognizes secrets by their key; these functions remove
// one KNOWN credential (the API key or tracker token that request used) wherever it appears, as
// free text too. Used for AI provider and tracker errors before they are logged, stored or
// returned.

// credentialForms are the ways a credential can show up inside a text.
func credentialForms(cred string) []string {
	forms := []string{cred, url.QueryEscape(cred), url.PathEscape(cred), strings.ReplaceAll(cred, "/", `\/`)}
	if j, err := json.Marshal(cred); err == nil {
		forms = append(forms, strings.Trim(string(j), `"`))
	}
	// base64 (Basic auth "user:key", tokens que la envuelven): la credencial puede ir después de
	// cualquier prefijo, así que se toma, para cada alineación posible, la parte de la
	// codificación que solo depende de ella
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		forms = append(forms, enc.EncodeToString([]byte(cred)), enc.EncodeToString([]byte(":"+cred)))
		for pad := 0; pad < 3; pad++ {
			full := enc.Strict().EncodeToString(append(make([]byte, pad), cred...))
			start := (pad*4 + 2) / 3 // caracteres que dependen del prefijo
			end := len(full) - 4     // el último bloque puede depender de lo que sigue
			if end-start >= 8 {
				forms = append(forms, full[start:end])
			}
		}
	}
	return forms
}

// ScrubCredentials replaces every given credential (in every form) in s with mask. A credential
// shorter than 6 characters is ignored: it would cover normal text.
func ScrubCredentials(s, mask string, creds ...string) string {
	for _, cred := range creds {
		cred = strings.TrimSpace(cred)
		if len(cred) < 6 {
			continue
		}
		for _, f := range credentialForms(cred) {
			if len(f) >= 6 {
				s = strings.ReplaceAll(s, f, mask)
			}
		}
	}
	return s
}

// scrubbedError is an error whose text no longer carries a credential. It still unwraps to the
// original, so cancellation, timeouts and the caller's own error types are recognized with
// errors.Is / errors.As.
type scrubbedError struct {
	msg string
	err error
}

func (e *scrubbedError) Error() string { return e.msg }
func (e *scrubbedError) Unwrap() error { return e.err }

// ScrubCredentialError returns err with the credentials removed from its text (err itself when
// none was there).
func ScrubCredentialError(err error, mask string, creds ...string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if clean := ScrubCredentials(msg, mask, creds...); clean != msg {
		return &scrubbedError{msg: clean, err: err}
	}
	return err
}
