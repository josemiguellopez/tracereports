package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Auth configures access control (all optional):
//
//	Token       TRACEREPORTS_TOKEN: required by every write (POST/PATCH) to the API, sent by the
//	            clients as "Authorization: Bearer <token>" or "X-TraceReports-Token". It is also an
//	            admin credential: it may change Settings (including the AI key) and use the UI
//	            actions (escalate, tickets, quarantine...). Kept like that for compatibility.
//	IngestToken TRACEREPORTS_INGEST_TOKEN: a restricted alternative for test clients and CI. It
//	            may send results (every write except Settings and UI actions) and read, but never
//	            administer. Use it in pipelines and keep TRACEREPORTS_TOKEN for admins.
//	UIUser/Pass TRACEREPORTS_UI_USER / TRACEREPORTS_UI_PASSWORD: HTTP Basic login for the web UI and
//	            every read. A valid token (either) also grants read access (CI downloading the ZIP).
//	            Without it, reads are open to whoever reaches the server through an allowed host
//	            (localhost or TRACEREPORTS_ALLOWED_HOSTS): a token alone does not protect reads.
//	LocalAdmin  TRACEREPORTS_LOCAL_ADMIN: with a token but no UI login, lets the same machine
//	            (direct connection, not through a proxy) change settings and use UI actions.
type Auth struct {
	Token       string
	IngestToken string
	UIUser      string
	UIPass      string
	LocalAdmin  bool
}

// tokensSet reports whether writes require a token (the server counts as deployed).
func (a Auth) tokensSet() bool { return a.Token != "" || a.IngestToken != "" }

// sentToken is the token of the request (X-TraceReports-Token or Authorization: Bearer).
func sentToken(r *http.Request) string {
	got := tokenHeader(r)
	if h := r.Header.Get("Authorization"); got == "" && strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	return got
}

func tokenIs(got, want string) bool {
	return want != "" && got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// ingestOK reports whether the request carries the restricted ingestion token.
func (a Auth) ingestOK(r *http.Request) bool { return tokenIs(sentToken(r), a.IngestToken) }

// tokenOK reports whether the request carries TRACEREPORTS_TOKEN (the full, admin token).
func (a Auth) tokenOK(r *http.Request) bool { return tokenIs(sentToken(r), a.Token) }

// tokenHeader returns the X-TraceReports-Token header.
func tokenHeader(r *http.Request) string {
	return r.Header.Get("X-TraceReports-Token")
}

func (a Auth) basicOK(r *http.Request) bool {
	user, pass, ok := r.BasicAuth()
	return ok &&
		subtle.ConstantTimeCompare([]byte(user), []byte(a.UIUser)) == 1 &&
		subtle.ConstantTimeCompare([]byte(pass), []byte(a.UIPass)) == 1
}

// middleware enforces the configured rules; with nothing configured everything is open.
func (a Auth) middleware(next http.Handler) http.Handler {
	uiAuth := a.UIUser != "" && a.UIPass != ""
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isWrite := r.Method != http.MethodGet && r.Method != http.MethodHead
		// cualquiera de los dos tokens escribe resultados y lee; administrar es aparte
		token := a.tokenOK(r) || a.ingestOK(r)
		// los ajustes tienen su propia regla (settingsAccess): la UI no conoce el token
		isSettings := strings.HasPrefix(r.URL.Path, "/api/v1/settings") || strings.HasPrefix(r.URL.Path, "/api/v1/ui/")
		if isWrite && a.tokensSet() && !token && !isSettings {
			writeError(w, http.StatusUnauthorized, "missing or invalid API token (Authorization: Bearer <TRACEREPORTS_TOKEN>)")
			return
		}
		if uiAuth && !token && !a.basicOK(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="TraceReports", charset="UTF-8"`)
			writeError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}
