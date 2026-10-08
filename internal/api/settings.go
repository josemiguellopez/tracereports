package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/env"
	"github.com/josemiguellopez/tracereports/internal/secret"
)

// Ajustes guardados (tabla settings). Lo que no está guardado sale del entorno (.env).
const (
	setLanguage   = "ui.language" // idioma por defecto de la interfaz: es | en
	setAILanguage = "ai.language" // idioma de los diagnósticos: auto | es | en
	setProvider   = "ai.provider" // id del proveedor, u "off" para apagar la IA
	setModel      = "ai.model"
	setBaseURL    = "ai.base_url"
	setAPIKey     = "ai.api_key"
)

var languages = set("es", "en")

// SecretSettings are the settings that hold credentials: encrypted with TRACEREPORTS_SECRET_KEY
// (see internal/secret) and never returned by the API.
var SecretSettings = []string{setAPIKey}

// errSecretKey is returned when a credential cannot be stored or read because of the master key.
type errSecretKey struct{ err error }

func (e errSecretKey) Error() string { return e.err.Error() }
func (e errSecretKey) Unwrap() error { return e.err }

// keyState explains the stored AI key for the Settings screen and the logs (never its value).
func keyState(stored string, err error) string {
	switch {
	case stored == "":
		return ""
	case errors.Is(err, secret.ErrNoKey):
		return "master_key_missing"
	case errors.Is(err, secret.ErrWrongKey):
		return "master_key_wrong"
	case errors.Is(err, secret.ErrCorrupt):
		return "damaged"
	case !secret.IsSealed(stored):
		return "plaintext"
	}
	return "encrypted"
}

// savedSettings reads the settings with the saved AI key decrypted. When it cannot be decrypted
// (missing or wrong TRACEREPORTS_SECRET_KEY, damaged value) the key is left out and keyErr says
// why; the stored value is never changed here.
func (s *Server) savedSettings() (saved map[string]string, stored string, keyErr error, err error) {
	saved, err = s.Store.Settings()
	if err != nil {
		return nil, "", nil, err
	}
	stored = saved[setAPIKey]
	if stored != "" {
		plain, e := s.Secrets.Open(setAPIKey, stored)
		if e != nil {
			keyErr = errSecretKey{e}
			plain = ""
		}
		saved[setAPIKey] = plain
	}
	return saved, stored, keyErr, nil
}

// keyProblem is the message for a saved AI key that cannot be used (without revealing it).
func keyProblem(err error) string {
	return "the AI key saved from Settings cannot be decrypted: " + err.Error() +
		". Set the TRACEREPORTS_SECRET_KEY it was saved with (it was not deleted), or type the key again"
}

type peerKey struct{}

// peerAddr keeps the socket address before middleware.RealIP rewrites RemoteAddr from
// X-Forwarded-For (which any client can send): "same machine" checks must use the real peer.
func peerAddr(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), peerKey{}, r.RemoteAddr)))
	})
}

func fromLoopback(r *http.Request) bool {
	addr, _ := r.Context().Value(peerKey{}).(string)
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// proxied reports whether r came through a reverse proxy: then the loopback peer is the proxy,
// not the person, and the request may come from anywhere.
func proxied(r *http.Request) bool {
	return r.Header.Get("X-Forwarded-For") != "" || r.Header.Get("X-Real-IP") != "" || r.Header.Get("Forwarded") != ""
}

// adminAccess is the rule shared by settings and UI actions: API token, UI login or, only in
// local mode, the same machine. Local mode is a server without token nor UI login, reached
// directly (not through a proxy): as soon as TRACEREPORTS_TOKEN is configured the server counts as
// deployed and the same machine needs credentials too, unless TRACEREPORTS_LOCAL_ADMIN=1 says so.
func (s *Server) adminAccess(r *http.Request) (bool, string) {
	switch {
	case s.Auth.tokenOK(r):
		return true, ""
	case s.Auth.UIUser != "" && s.Auth.UIPass != "":
		if s.Auth.basicOK(r) {
			return true, ""
		}
		return false, "login"
	case (!s.Auth.tokensSet() || s.Auth.LocalAdmin) && fromLoopback(r) && !proxied(r):
		return true, ""
	}
	if s.Auth.ingestOK(r) {
		return false, "ingest_token" // el token de ingesta nunca administra
	}
	return false, "remote"
}

// settingsAccess decides whether r may change the settings. The reason ("locked", "login",
// "remote") is translated by the UI.
func (s *Server) settingsAccess(r *http.Request) (bool, string) {
	switch {
	case s.SettingsLocked:
		return false, "locked"
	}
	return s.adminAccess(r)
}

// guardSettingsWrite enforces access plus basic CSRF protection: JSON body (a cross-site form
// cannot send it without a CORS preflight, which this server never approves) and, when the
// browser sends Origin, the same host.
func (s *Server) guardSettingsWrite(w http.ResponseWriter, r *http.Request) bool {
	if ok, reason := s.settingsAccess(r); !ok {
		writeError(w, http.StatusForbidden, "settings are read-only here ("+reason+")")
		return false
	}
	return jsonSameOrigin(w, r)
}

// jsonSameOrigin rejects writes that a cross-site page could forge: they must be JSON and, when
// the browser sends Origin, come from this same host.
func jsonSameOrigin(w http.ResponseWriter, r *http.Request) bool {
	if mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type")); mt != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "expected application/json")
		return false
	}
	if o := r.Header.Get("Origin"); o != "" {
		if u, err := url.Parse(o); err != nil || !strings.EqualFold(u.Host, r.Host) {
			writeError(w, http.StatusForbidden, "cross-origin request rejected")
			return false
		}
	}
	return true
}

// LoadSettings applies the saved settings over the environment (call once at startup).
func (s *Server) LoadSettings() error {
	saved, stored, keyErr, err := s.savedSettings()
	if err != nil {
		return err
	}
	switch {
	case keyErr != nil:
		slog.Error("settings: "+keyProblem(keyErr), "state", keyState(stored, keyErr), "key_id", secret.KeyIDOf(stored))
	case stored != "" && !secret.IsSealed(stored):
		slog.Warn("settings: the AI key saved from Settings is stored in clear; set TRACEREPORTS_SECRET_KEY and run 'tracereports secrets migrate' (see docs: configuration)")
	}
	s.AI.SetLanguage(aiLanguage(saved))
	if saved[setProvider] == "" {
		return nil // IA desde el .env
	}
	cfg := savedAIConfig(saved)
	if err := s.AI.SetConfig(cfg); err != nil {
		slog.Warn("settings: saved AI configuration is not usable, using the environment", "err", err)
		return s.AI.SetConfig(ai.ConfigFromEnv())
	}
	return nil
}

func aiLanguage(saved map[string]string) string {
	if l := saved[setAILanguage]; languages[l] {
		return l
	}
	return ""
}

func savedAIConfig(saved map[string]string) ai.Config {
	p := saved[setProvider]
	if p == "off" {
		return ai.Config{}
	}
	key := saved[setAPIKey]
	if key == "" {
		key = ai.EnvKey(p)
	}
	return ai.Config{Provider: p, Model: saved[setModel], BaseURL: saved[setBaseURL], APIKey: key}
}

func keyHint(key string) string {
	if len(key) < 8 {
		if key == "" {
			return ""
		}
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}

type aiView struct {
	Provider  string `json:"provider"`
	Model     string `json:"model"`
	BaseURL   string `json:"base_url"`
	Enabled   bool   `json:"enabled"`
	KeySet    bool   `json:"key_set"`
	KeyHint   string `json:"key_hint"`
	KeySource string `json:"key_source"` // ui | env | ""
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	saved, stored, keyErr, err := s.savedSettings()
	if err != nil {
		serverError(w, err)
		return
	}
	editable, reason := s.settingsAccess(r)
	cur := s.AI.Config()
	env := ai.ConfigFromEnv()
	keySource := ""
	switch {
	case cur.APIKey == "":
	case stored != "" && saved[setProvider] == cur.Provider:
		keySource = "ui"
	default:
		keySource = "env"
	}
	source := "env"
	if saved[setProvider] != "" {
		source = "ui"
	}
	aiLang := saved[setAILanguage]
	if aiLang == "" {
		aiLang = "auto"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"editable":    editable,
		"edit_reason": reason,
		"runtime":     runtimeKind(),
		"token_set":   s.Auth.Token != "",                         // el servidor exige TRACEREPORTS_TOKEN para escribir
		"ui_login":    s.Auth.UIUser != "" && s.Auth.UIPass != "", // la UI pide usuario y clave
		"language":    saved[setLanguage],
		"ai_language": aiLang,
		"ai_source":   source,
		"ai": aiView{Provider: cur.Provider, Model: cur.Model, BaseURL: cur.BaseURL, Enabled: s.AI.Enabled(),
			KeySet: cur.APIKey != "", KeyHint: keyHint(cur.APIKey), KeySource: keySource},
		"env_ai":    aiView{Provider: env.Provider, Model: env.Model, BaseURL: env.BaseURL, KeySet: env.APIKey != ""},
		"providers": ai.Providers,
		// cifrado de la key guardada: estado, nunca el valor (master_key_missing/_wrong, damaged,
		// plaintext = guardada antes del cifrado, encrypted)
		"secrets": map[string]any{"master_key_set": s.Secrets.Enabled(), "saved_key": keyState(stored, keyErr)},
	})
}

type aiInput struct {
	Provider string  `json:"provider"`
	Model    string  `json:"model"`
	BaseURL  string  `json:"base_url"`
	APIKey   *string `json:"api_key"` // nil o "" = mantener la key actual de ese proveedor
}

// resolve builds the config to use; without a new key it reuses the saved one (same provider)
// or the one in the environment.
func (s *Server) resolveAI(in aiInput, saved map[string]string, keyErr error) (ai.Config, bool, error) {
	p := strings.ToLower(strings.TrimSpace(in.Provider))
	c := ai.Config{Provider: p, Model: in.Model, BaseURL: in.BaseURL}
	if in.APIKey != nil && strings.TrimSpace(*in.APIKey) != "" {
		c.APIKey = strings.TrimSpace(*in.APIKey)
		return c, true, nil
	}
	if saved[setProvider] == p && keyErr != nil {
		return c, false, keyErr // no se cambia en silencio por la key del .env
	}
	if saved[setProvider] == p && saved[setAPIKey] != "" {
		c.APIKey = saved[setAPIKey]
	} else {
		c.APIKey = ai.EnvKey(p)
	}
	return c, false, nil
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	if !s.guardSettingsWrite(w, r) {
		return
	}
	var in struct {
		Language   *string  `json:"language"`
		AILanguage *string  `json:"ai_language"`
		AI         *aiInput `json:"ai"`
	}
	if !decode(w, r, &in) {
		return
	}
	saved, _, keyErr, err := s.savedSettings()
	if err != nil {
		serverError(w, err)
		return
	}
	changes := map[string]string{}
	if in.Language != nil {
		if *in.Language != "" && !languages[*in.Language] {
			writeError(w, http.StatusBadRequest, "language must be es or en")
			return
		}
		changes[setLanguage] = *in.Language
	}
	if in.AILanguage != nil {
		l := *in.AILanguage
		if l != "auto" && !languages[l] {
			writeError(w, http.StatusBadRequest, "ai_language must be auto, es or en")
			return
		}
		changes[setAILanguage] = l
	}
	var newCfg *ai.Config
	if in.AI != nil {
		if p := strings.ToLower(strings.TrimSpace(in.AI.Provider)); p == "" || p == "off" {
			newCfg = &ai.Config{}
			changes[setProvider], changes[setModel], changes[setBaseURL], changes[setAPIKey] = "off", "", "", ""
		} else {
			c, newKey, err := s.resolveAI(*in.AI, saved, keyErr)
			if err != nil {
				writeError(w, http.StatusConflict, keyProblem(err))
				return
			}
			if err := c.Validate(); err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			newCfg = &c
			changes[setProvider], changes[setModel], changes[setBaseURL] = c.Provider, strings.TrimSpace(c.Model), strings.TrimSpace(c.BaseURL)
			switch {
			case newKey:
				// en la base solo cifrada: sin clave maestra no se guarda (la del .env sigue sirviendo)
				sealed, err := s.Secrets.Seal(setAPIKey, c.APIKey)
				if err != nil {
					writeError(w, http.StatusConflict, "to save an API key from Settings the server needs TRACEREPORTS_SECRET_KEY "+
						"(32 random bytes, e.g. openssl rand -base64 32), so the key is stored encrypted. "+
						"Alternatively set the key in the environment (AI_API_KEY or the provider's variable)")
					return
				}
				changes[setAPIKey] = sealed
			case saved[setProvider] != c.Provider:
				changes[setAPIKey] = "" // la key guardada era de otro proveedor
			}
		}
	}
	if err := s.Store.SaveSettings(changes); err != nil {
		serverError(w, err)
		return
	}
	if newCfg != nil {
		if err := s.AI.SetConfig(*newCfg); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if in.AILanguage != nil {
		saved[setAILanguage] = *in.AILanguage
		s.AI.SetLanguage(aiLanguage(saved))
	}
	s.getSettings(w, r)
}

// resetAISettings forgets the AI configuration saved from the UI: back to the .env.
func (s *Server) resetAISettings(w http.ResponseWriter, r *http.Request) {
	if !s.guardSettingsWrite(w, r) {
		return
	}
	if err := s.Store.SaveSettings(map[string]string{setProvider: "", setModel: "", setBaseURL: "", setAPIKey: ""}); err != nil {
		serverError(w, err)
		return
	}
	if err := s.AI.SetConfig(ai.ConfigFromEnv()); err != nil {
		writeError(w, http.StatusBadRequest, "the .env AI configuration is not valid: "+err.Error())
		return
	}
	s.getSettings(w, r)
}

// testAISettings checks a provider configuration before saving it.
func (s *Server) testAISettings(w http.ResponseWriter, r *http.Request) {
	if !s.guardSettingsWrite(w, r) {
		return
	}
	var in aiInput
	if !decode(w, r, &in) {
		return
	}
	saved, _, keyErr, err := s.savedSettings()
	if err != nil {
		serverError(w, err)
		return
	}
	c, _, err := s.resolveAI(in, saved, keyErr)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": keyProblem(err)})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	took, err := s.AI.Test(ctx, c)
	if err != nil {
		msg := s.redactor().Text(err.Error()) // la key ya viene quitada (ai.call); además, la política
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "el proveedor no respondió en 90 s"
		}
		hint, suggest := ai.Hint(err, c)
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": msg, "hint": hint, "suggest": suggest})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ms": took.Milliseconds()})
}

// runtimeKind tells the UI how the server runs, to show the right "apply the .env" steps.
func runtimeKind() string {
	if env.Get("RUNTIME") == "docker" {
		return "docker"
	}
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	return "binary"
}

// checkToken tells whether the token sent in the request (Authorization: Bearer / X-TraceReports-Token)
// is the server's TRACEREPORTS_TOKEN. Lets the Settings screen confirm a token without writing data.
// Writes already answer 401/201 for the same token, so it reveals nothing new.
func (s *Server) checkToken(w http.ResponseWriter, r *http.Request) {
	sent := tokenHeader(r) != "" || strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ")
	scope := ""
	switch {
	case s.Auth.tokenOK(r):
		scope = "admin" // TRACEREPORTS_TOKEN: escribe, lee y administra
	case s.Auth.ingestOK(r):
		scope = "ingest" // TRACEREPORTS_INGEST_TOKEN: escribe resultados y lee
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token_required": s.Auth.tokensSet(),
		"token_sent":     sent,
		"token_valid":    scope != "",
		"token_scope":    scope,
	})
}

// aiUsage reports the calls to the AI provider and the tokens it reported: today, the last 7
// and 30 days, by model, by kind and by day (Settings → AI usage).
func (s *Server) aiUsage(w http.ResponseWriter, r *http.Request) {
	u, err := s.Store.AIUsage(time.Now())
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"usage": u, "max_per_run": s.AI.MaxPerRun})
}

// ---------- métricas ----------

// dayStart is the first instant of a calendar date in loc (d may overflow: day+1). Usually its
// midnight, but some daylight saving changes skip midnight (Santiago jumps from 23:59:59 to
// 01:00): Go then normalizes 00:00 to the evening before, so the start is searched forward.
func dayStart(y int, m time.Month, d int, loc *time.Location) time.Time {
	want := time.Date(y, m, d, 12, 0, 0, 0, loc).Format("2006-01-02") // fecha pedida, normalizada
	t := time.Date(y, m, d, 0, 0, 0, 0, loc)
	if t.Format("2006-01-02") == want {
		return t
	}
	// t cayó en la fecha anterior; t+4h ya está en la pedida (ningún salto dura más): búsqueda binaria
	lo, hi := t, t.Add(4*time.Hour)
	for hi.Sub(lo) > time.Millisecond {
		mid := lo.Add(hi.Sub(lo) / 2)
		if mid.Format("2006-01-02") == want {
			hi = mid
		} else {
			lo = mid
		}
	}
	return hi.Truncate(time.Second)
}

func (s *Server) getMetrics(w http.ResponseWriter, r *http.Request) {
	qs := r.URL.Query()
	q := db.MetricsQuery{Suite: qs.Get("suite"), Env: qs.Get("env"), Tag: qs.Get("tag")}
	q.Days, _ = strconv.Atoi(qs.Get("days"))
	if q.Days <= 0 || q.Days > 365 {
		q.Days = 30
	}
	// rango propio: from/to como fechas (2026-09-01) en la hora del servidor, "to" inclusive: desde
	// el primer instante de "from" hasta el primer instante del día siguiente a "to"
	if f, err1 := time.Parse("2006-01-02", qs.Get("from")); err1 == nil {
		if t, err2 := time.Parse("2006-01-02", qs.Get("to")); err2 == nil && !t.Before(f) {
			// presupuesto: el gráfico tiene un punto por fecha; un rango más largo se rechaza
			// (explicado), no se recorta
			if n := int(t.Sub(f)/(24*time.Hour)) + 1; n > db.MaxMetricsDays {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("custom range too long: %d days, at most %d (a year); choose a shorter range", n, db.MaxMetricsDays))
				return
			}
			q.From = dayStart(f.Year(), f.Month(), f.Day(), time.Local).UnixMilli()
			q.To = dayStart(t.Year(), t.Month(), t.Day()+1, time.Local).UnixMilli()
		}
	}
	m, err := s.Store.Metrics(q)
	if errors.Is(err, db.ErrMetricsRangeTooLong) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, m)
}
