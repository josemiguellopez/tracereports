package ai

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"

	"github.com/josemiguellopez/tracereports/internal/env"
)

// Allowlist de proveedores (TRACEREPORTS_AI_ALLOWED_PROVIDERS): una organización que no puede
// enviar evidencia a la nube deja solo los locales ("builtin,ollama"). Un proveedor fuera de la
// lista no se usa aunque haya una key olvidada en el entorno ni aunque alguien lo elija en Ajustes:
// ConfigFromEnv lo ignora (y lo dice en el log) y Validate lo rechaza, así ni "Probar conexión"
// llega a llamarlo. Vacía = todos, como antes.

// allowedProviders returns the allowed provider ids, or nil when every provider is allowed.
func allowedProviders() map[string]bool {
	raw := strings.TrimSpace(env.Get("AI_ALLOWED_PROVIDERS"))
	if raw == "" {
		return nil
	}
	out := map[string]bool{}
	for _, id := range strings.Split(raw, ",") {
		if id = strings.ToLower(strings.TrimSpace(id)); id != "" {
			out[id] = true
		}
	}
	return out
}

// ProviderAllowed reports whether TRACEREPORTS_AI_ALLOWED_PROVIDERS lets id be used. Turning the
// diagnosis off ("") is always allowed.
func ProviderAllowed(id string) bool {
	allowed := allowedProviders()
	return id == "" || allowed == nil || allowed[strings.ToLower(strings.TrimSpace(id))]
}

// AllowedProviders lists the providers the Settings screen offers: the allowed ones.
func AllowedProviders() []ProviderInfo {
	out := make([]ProviderInfo, 0, len(Providers))
	for _, p := range Providers {
		if ProviderAllowed(p.ID) {
			out = append(out, p)
		}
	}
	return out
}

// CheckAllowlist rejects a TRACEREPORTS_AI_ALLOWED_PROVIDERS with an unknown id: a typo ("olama")
// would silently leave no provider at all.
func CheckAllowlist() error {
	var unknown []string
	for id := range allowedProviders() {
		if ProviderByID(id) == nil {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	ids := make([]string, len(Providers))
	for i, p := range Providers {
		ids[i] = p.ID
	}
	return fmt.Errorf("TRACEREPORTS_AI_ALLOWED_PROVIDERS: unknown provider %s (known: %s)", strings.Join(unknown, ", "), strings.Join(ids, ", "))
}

// errNotAllowed is the Validate error for a provider outside the allowlist.
func errNotAllowed(p *ProviderInfo) error {
	return fmt.Errorf("%s no está permitido en este servidor (TRACEREPORTS_AI_ALLOWED_PROVIDERS=%s)", p.Name,
		strings.TrimSpace(env.Get("AI_ALLOWED_PROVIDERS")))
}

// warnedNotAllowed avoids repeating the warning on every read of the environment (the Settings
// screen reads it on each view).
var warnedNotAllowed sync.Map

// allowedFromEnv keeps the provider of the environment only if it is allowed; otherwise the local
// engine (if allowed) or no diagnosis, with a warning that names the ignored provider.
func allowedFromEnv(c Config) Config {
	if ProviderAllowed(c.Provider) {
		return c
	}
	fallback := Config{}
	if ProviderAllowed(ProviderBuiltin) {
		fallback = Config{Provider: ProviderBuiltin}.withDefaults()
	}
	if _, seen := warnedNotAllowed.LoadOrStore(c.Provider, true); !seen {
		slog.Warn("AI provider not allowed by TRACEREPORTS_AI_ALLOWED_PROVIDERS: its key is ignored and nothing is sent to it",
			"provider", c.Provider, "using", map[bool]string{true: fallback.Provider, false: "off"}[fallback.Provider != ""])
	}
	return fallback
}
