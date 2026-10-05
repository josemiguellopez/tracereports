package api

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// Auth configures access control (all optional):
//
//	Token       TRACEREPORTS_TOKEN: required by every write (POST/PATCH) to the API, sent by the
//	            clients as "Authorization: Bearer <token>" or "X-TraceReports-Token".
//	UIUser/Pass TRACEREPORTS_UI_USER / TRACEREPORTS_UI_PASSWORD: HTTP Basic login for the web UI and
//	            every read. A valid token also grants read access (CI downloading the ZIP).
//	LocalAdmin  TRACEREPORTS_LOCAL_ADMIN: with a token but no UI login, lets the same machine
//	            (direct connection, not through a proxy) change settings and use UI actions.
type Auth struct {
	Token      string
	UIUser     string
	UIPass     string
	LocalAdmin bool
}

func (a Auth) tokenOK(r *http.Request) bool {
	if a.Token == "" {
		return false
	}
	got := tokenHeader(r)
	if h := r.Header.Get("Authorization"); got == "" && strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	}
	return got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(a.Token)) == 1
}

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
		token := a.tokenOK(r)
		// los ajustes tienen su propia regla (settingsAccess): la UI no conoce el token
		isSettings := strings.HasPrefix(r.URL.Path, "/api/v1/settings") || strings.HasPrefix(r.URL.Path, "/api/v1/ui/")
		if isWrite && a.Token != "" && !token && !isSettings {
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
