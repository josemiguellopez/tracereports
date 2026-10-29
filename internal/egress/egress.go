// Package egress lists every external destination the server may send data to, as configured: the
// AI provider and its status page, Teams and Slack, the ticket trackers and the extra Lab hosts. It
// is printed at startup and reused by `tracereports doctor --security`, so an organization can see
// where evidence may go without reading every variable. Only hosts are listed: the URLs of webhooks
// carry their secret in the path.
package egress

import (
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/env"
)

// Scopes of a destination host.
const (
	ScopeLoopback = "loopback" // este equipo
	ScopePrivate  = "private"  // una IP de red privada (RFC 1918, ULA, link-local)
	ScopePublic   = "public"   // una IP pública
	ScopeName     = "name"     // un nombre DNS: no se sabe sin resolverlo
)

// Destination is one place the server may send data to.
type Destination struct {
	Kind  string `json:"kind"`  // ai | ai_status | notify | tracker | lab
	Name  string `json:"name"`  // proveedor, Teams, Slack, GitHub...
	Host  string `json:"host"`  // host[:puerto], nunca la ruta ni la query
	Scope string `json:"scope"` // loopback | private | public | name
	What  string `json:"what"`  // qué se envía
}

// Destinations lists the configured destinations; c is the active AI configuration (it may come
// from the Settings screen, not only from the environment).
func Destinations(c ai.Config) []Destination {
	var out []Destination
	add := func(kind, name, raw, what string) {
		if host := hostOf(raw); host != "" {
			out = append(out, Destination{Kind: kind, Name: name, Host: host, Scope: Scope(host), What: what})
		}
	}
	if c.Provider != "" && c.Provider != ai.ProviderBuiltin {
		name := c.Provider
		if p := ai.ProviderByID(c.Provider); p != nil {
			name = p.Name
			if c.BaseURL == "" {
				c.BaseURL = p.DefaultBaseURL
			}
		}
		add("ai", name, c.BaseURL, "diagnosis prompts (redacted; never screenshots)")
		add("ai_status", name+" status", ai.StatusURL(c), "a status request, no evidence")
	}
	add("notify", "Microsoft Teams", os.Getenv("TEAMS_WEBHOOK_URL"), "run summaries: test names and errors")
	add("notify", "Slack", os.Getenv("SLACK_WEBHOOK_URL"), "run summaries: test names and errors")
	if env.Get("GITHUB_REPO") != "" && env.Get("GITHUB_TOKEN") != "" {
		api := env.Get("GITHUB_API")
		if api == "" {
			api = "https://api.github.com"
		}
		add("tracker", "GitHub Issues", api, "escalations created from the UI")
	}
	if env.Get("JIRA_PROJECT") != "" && env.Get("JIRA_TOKEN") != "" {
		add("tracker", "Jira", env.Get("JIRA_URL"), "escalations created from the UI")
	}
	if env.Get("AZURE_PROJECT") != "" && env.Get("AZURE_TOKEN") != "" {
		add("tracker", "Azure DevOps", env.Get("AZURE_URL"), "escalations created from the UI")
	}
	if !labOff(env.Get("LAB")) {
		for _, h := range strings.Split(env.Get("LAB_HOSTS"), ",") {
			if h = strings.ToLower(strings.TrimSpace(h)); h != "" {
				out = append(out, Destination{Kind: "lab", Name: "Lab", Host: h, Scope: Scope(h),
					What: "captured calls resent from the UI"})
			}
		}
	}
	return out
}

// hostOf returns host[:port] of a URL, or "" if it has none.
func hostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw // OLLAMA_HOST suele ser "localhost:11434"
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Host)
}

// Scope classifies a host[:port] without resolving it.
func Scope(host string) string {
	h := host
	if hh, _, err := net.SplitHostPort(host); err == nil {
		h = hh
	}
	h = strings.Trim(strings.TrimPrefix(h, "*."), "[]")
	if strings.EqualFold(h, "localhost") || strings.HasSuffix(strings.ToLower(h), ".localhost") {
		return ScopeLoopback
	}
	ip := net.ParseIP(h)
	switch {
	case ip == nil:
		return ScopeName
	case ip.IsLoopback():
		return ScopeLoopback
	case ip.IsPrivate() || ip.IsLinkLocalUnicast():
		return ScopePrivate
	}
	return ScopePublic
}

// labOff is TRACEREPORTS_LAB=off|0|false|no (the Lab sends nothing).
func labOff(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "off", "0", "false", "no":
		return true
	}
	return false
}
