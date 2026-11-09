package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Login de la UI con su propia página (/login/) en vez del cuadro de HTTP Basic del navegador: una
// sesión en una cookie HttpOnly + SameSite=Strict (Secure con HTTPS) que vence sin uso y tiene un tope,
// "recordarme" para días, salir de verdad, y el mismo freno a la fuerza bruta que el resto. El usuario
// sigue siendo el de la configuración (TRACEREPORTS_UI_USER/PASSWORD y el de solo lectura). HTTP Basic y
// los tokens siguen valiendo igual para la API, los clientes de test y CI.
//
//	TRACEREPORTS_SESSION_IDLE_MIN      sin uso por más que esto, la sesión vence (30)
//	TRACEREPORTS_SESSION_MAX_HOURS     tope de una sesión normal (8)
//	TRACEREPORTS_SESSION_REMEMBER_DAYS tope con "recordarme" (7); sin uso, vence igual al tope
const sessionCookie = "tr_session"

type sessionConf struct{ idle, max, remember time.Duration }

func sessionSettings() sessionConf {
	num := func(key string, def float64) float64 {
		if v, err := strconv.ParseFloat(strings.TrimSpace(os.Getenv(key)), 64); err == nil && v > 0 {
			return v
		}
		return def
	}
	return sessionConf{
		idle:     time.Duration(num("TRACEREPORTS_SESSION_IDLE_MIN", 30) * float64(time.Minute)),
		max:      time.Duration(num("TRACEREPORTS_SESSION_MAX_HOURS", 8) * float64(time.Hour)),
		remember: time.Duration(num("TRACEREPORTS_SESSION_REMEMBER_DAYS", 7) * 24 * float64(time.Hour)),
	}
}

type sessionKey struct{}

// sessionInfo is what the session middleware learned from the cookie of a request.
type sessionInfo struct {
	role    string // admin | viewer ("" sin sesión válida)
	user    string
	expired bool // traía una cookie que ya no vale
	s       db.UISession
}

func sessionOf(r *http.Request) sessionInfo {
	si, _ := r.Context().Value(sessionKey{}).(sessionInfo)
	return si
}

func hashToken(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// credPrint is a fingerprint of the configured user and password: a session made with other
// credentials (the password changed in the configuration) is no longer valid.
func credPrint(user, pass string) string {
	h := sha256.Sum256([]byte("tracereports-ui-session\x00" + user + "\x00" + pass))
	return hex.EncodeToString(h[:16])
}

// roleFor checks a user and password against the configured logins (constant time, both compared).
func (a Auth) roleFor(user, pass string) string {
	eq := func(x, y string) bool { return y != "" && subtle.ConstantTimeCompare([]byte(x), []byte(y)) == 1 }
	admin := eq(user, a.UIUser) && eq(pass, a.UIPass)
	viewer := eq(user, a.ViewerUser) && eq(pass, a.ViewerPass)
	switch {
	case admin:
		return "admin"
	case viewer:
		return "viewer"
	}
	return ""
}

// currentCred is the fingerprint a session of that role must carry right now.
func (a Auth) currentCred(role string) string {
	if role == "admin" && a.UIUser != "" && a.UIPass != "" {
		return credPrint(a.UIUser, a.UIPass)
	}
	if role == "viewer" && a.ViewerUser != "" && a.ViewerPass != "" {
		return credPrint(a.ViewerUser, a.ViewerPass)
	}
	return "-" // ese rol ya no existe: ninguna sesión coincide
}

// sessionAuth reads the session cookie: a valid one marks the request as logged in (Auth.basicOK
// and viewerOK accept it); an expired one is deleted. Activity slides the idle window.
func (s *Server) sessionAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" || !s.Auth.uiLogin() {
			next.ServeHTTP(w, r)
			return
		}
		h := hashToken(c.Value)
		si := sessionInfo{expired: true}
		if sess, ok, err := s.Store.Session(h); err == nil && ok {
			now := db.NowMs()
			if sess.Valid(now) && subtle.ConstantTimeCompare([]byte(sess.Cred), []byte(s.Auth.currentCred(sess.Role))) == 1 {
				si = sessionInfo{role: sess.Role, user: sess.User, s: sess}
				// el pulso de la vista (sesión, eventos) no cuenta como uso: si no, nunca vencería por inactividad
				if !passive(r) && now-sess.LastSeen > 15_000 {
					_ = s.Store.TouchSession(h, now)
					si.s.LastSeen = now
				}
			} else {
				_ = s.Store.DeleteSession(h)
			}
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), sessionKey{}, si)))
	})
}

// passive: requests the open page makes on its own do not keep a session alive: the session status,
// the live event stream and whatever the UI sends with X-TraceReports-Passive (it marks its requests so
// when nobody touched the page for a minute: a forgotten tab still expires).
func passive(r *http.Request) bool {
	return r.URL.Path == "/api/v1/auth/session" || r.URL.Path == "/api/v1/stream" || r.Header.Get("X-TraceReports-Passive") == "1"
}

// loginPublic are the paths that work without a login: the login page with its assets and the
// login/logout endpoints (and /healthz, which goes before all this).
func loginPublic(r *http.Request) bool {
	p := r.URL.Path
	switch {
	case p == "/login" || strings.HasPrefix(p, "/login/"):
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	case p == "/api/v1/auth/login" || p == "/api/v1/auth/logout" || p == "/api/v1/auth/session":
		return true
	case strings.HasPrefix(p, "/buddy/") || strings.HasPrefix(p, "/vendor/fonts/") || p == "/favicon.svg" || p == "/favicon.ico":
		return r.Method == http.MethodGet || r.Method == http.MethodHead
	}
	return false
}

// wantsPage: a browser navigating to a page (not the API, not an asset fetched by a page).
func wantsPage(r *http.Request) bool {
	if r.Method != http.MethodGet || strings.HasPrefix(r.URL.Path, "/api/") {
		return false
	}
	if d := r.Header.Get("Sec-Fetch-Dest"); d != "" {
		return d == "document"
	}
	return strings.Contains(r.Header.Get("Accept"), "text/html")
}

// fromUI: a request from the web UI (it sends X-TraceReports-UI, or carries a session cookie). Its 401
// must not ask with WWW-Authenticate: the browser would open its own login box over the page.
func fromUI(r *http.Request) bool {
	if r.Header.Get("X-TraceReports-UI") != "" {
		return true
	}
	_, err := r.Cookie(sessionCookie)
	return err == nil
}

func secureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func newSessionToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: value, Path: "/", MaxAge: maxAge,
		HttpOnly: true, Secure: secureRequest(r), SameSite: http.SameSiteStrictMode})
}

type sessionView struct {
	LoggedIn  bool   `json:"logged_in"`
	Login     bool   `json:"login_required"` // el servidor pide login (si no, la UI está abierta)
	User      string `json:"user,omitempty"`
	Role      string `json:"role,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"` // tope absoluto
	IdleMs    int64  `json:"idle_ms,omitempty"`
	IdleUntil int64  `json:"idle_until,omitempty"` // vence aquí si no hay uso
	Remember  bool   `json:"remember,omitempty"`
	Expired   bool   `json:"expired,omitempty"`
	// lo que dice la página de ingreso antes de entrar: cuánto dura una sesión
	IdleMin      float64 `json:"idle_min"`
	RememberDays float64 `json:"remember_days"`
}

func viewOf(si sessionInfo, login bool) sessionView {
	c := sessionSettings()
	v := sessionView{Login: login, Expired: si.expired, IdleMin: c.idle.Minutes(), RememberDays: c.remember.Hours() / 24}
	if si.role == "" {
		return v
	}
	v.Expired = false
	v.LoggedIn, v.User, v.Role, v.ExpiresAt, v.IdleMs = true, si.user, si.role, si.s.ExpiresAt, si.s.IdleMs
	v.IdleUntil, v.Remember = min(si.s.ExpiresAt, si.s.LastSeen+si.s.IdleMs), si.s.Remember
	return v
}

// getSession: who is logged in and until when (the UI warns before it expires).
func (s *Server) getSession(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, viewOf(sessionOf(r), s.Auth.uiLogin()))
}

// postLogin checks the user and password and opens a session. Failures count for the brute-force brake
// of the address (the same as HTTP Basic and the tokens) and always say the same.
func (s *Server) postLogin(w http.ResponseWriter, r *http.Request) {
	if !s.Auth.uiLogin() {
		writeError(w, http.StatusNotFound, "this server has no UI login (TRACEREPORTS_UI_USER / TRACEREPORTS_UI_PASSWORD)")
		return
	}
	peer, _ := r.Context().Value(peerKey{}).(string)
	ip := s.Net.ClientIP(peer, r.Header)
	if left := s.authFails.blocked(ip); left > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(int(left.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "too many failed logins from this address: try again later")
		return
	}
	var in struct {
		User     string `json:"user"`
		Password string `json:"password"`
		Remember bool   `json:"remember"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid login request")
		return
	}
	role := s.Auth.roleFor(in.User, in.Password)
	if role == "" {
		s.authFails.fail(ip)
		time.Sleep(400 * time.Millisecond) // sin prisa para quien prueba claves
		slog.Warn("ui login failed", "ip", ip.String())
		writeError(w, http.StatusUnauthorized, "wrong user or password")
		return
	}
	s.authFails.reset(ip)
	// una sesión anterior de este navegador se cierra (no quedan dos)
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.Store.DeleteSession(hashToken(c.Value))
	}
	tok, err := newSessionToken()
	if err != nil {
		serverError(w, err)
		return
	}
	conf, now := sessionSettings(), db.NowMs()
	life, idle := conf.max, conf.idle
	if in.Remember {
		life, idle = conf.remember, conf.remember
	}
	sess := db.UISession{User: in.User, Role: role, CreatedAt: now, LastSeen: now, ExpiresAt: now + life.Milliseconds(),
		IdleMs: idle.Milliseconds(), Remember: in.Remember, Cred: s.Auth.currentCred(role)}
	if err := s.Store.CreateSession(hashToken(tok), sess); err != nil {
		serverError(w, err)
		return
	}
	maxAge := 0 // sin "recordarme": cookie de sesión del navegador (se va al cerrarlo)
	if in.Remember {
		maxAge = int(life.Seconds())
	}
	setSessionCookie(w, r, tok, maxAge)
	slog.Info("ui login", "role", role, "remember", in.Remember)
	writeJSON(w, http.StatusOK, viewOf(sessionInfo{role: role, user: in.User, s: sess}, true))
}

// postLogout ends the session of this browser.
func (s *Server) postLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		_ = s.Store.DeleteSession(hashToken(c.Value))
	}
	setSessionCookie(w, r, "", -1)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// loginRedirect sends a browser that asks for a page without a login to /login/, back to where it was after.
func loginRedirect(w http.ResponseWriter, r *http.Request) {
	next := r.URL.RequestURI()
	if next == "" || !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
		next = "/"
	}
	http.Redirect(w, r, "/login/?next="+url.QueryEscape(next), http.StatusSeeOther)
}
