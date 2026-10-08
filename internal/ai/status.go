package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/josemiguellopez/tracereports/internal/env"
)

// ProviderStatus is the general state of the configured AI provider, as its public status page
// says (Settings → AI usage): to know when a slow or failing AI is a problem of the service
// and not of TraceReports. Only a GET to that page: nothing about the tests is sent.
type ProviderStatus struct {
	Provider    string            `json:"provider"`
	Name        string            `json:"name"`
	Source      string            `json:"source"`                // statuspage | local | none
	Indicator   string            `json:"indicator"`             // none | minor | major | critical | maintenance | unknown
	Description string            `json:"description,omitempty"` // la de la página; local: la versión o la dirección de Ollama
	PageURL     string            `json:"page_url,omitempty"`    // página para personas
	Components  []StatusComponent `json:"components"`            // los que usa TraceReports
	Incidents   []StatusIncident  `json:"incidents"`             // abiertos
	Error       string            `json:"error,omitempty"`       // no se pudo consultar
	CheckedAt   int64             `json:"checked_at"`
}

// StatusComponent is one part of the provider's service.
type StatusComponent struct {
	Name   string `json:"name"`
	Status string `json:"status"` // operational | degraded_performance | partial_outage | major_outage | under_maintenance
}

// StatusIncident is an open incident of the provider.
type StatusIncident struct {
	Name      string `json:"name"`
	Status    string `json:"status"` // investigating | identified | monitoring
	Impact    string `json:"impact"` // none | minor | major | critical
	URL       string `json:"url,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

// statusPages are the public status pages (Statuspage format, /api/v2/summary.json) and the
// components that matter to TraceReports' calls. Gemini (AI Studio) has no machine-readable
// status: only its page is linked.
var statusPages = map[string]struct {
	api, page string
	component func(name string) bool
}{
	"openai": {"https://status.openai.com/api/v2/summary.json", "https://status.openai.com/",
		func(n string) bool { return n == "Chat Completions" || n == "Responses" }},
	"anthropic": {"https://status.claude.com/api/v2/summary.json", "https://status.claude.com/",
		func(n string) bool { return strings.Contains(n, "API") }},
	"gemini": {"", "https://aistudio.google.com/status", nil},
}

// statusTTL is how long a checked status is reused (the page is not hammered on every view).
const statusTTL = time.Minute

type statusCache struct {
	mu   sync.Mutex
	key  string
	when time.Time
	last *ProviderStatus
}

// ProviderStatus checks the status of the configured provider (cached for statusTTL; force
// checks again). TRACEREPORTS_AI_STATUS_URL points to another Statuspage-compatible summary
// (a mirror, a proxy, or the page of an OpenAI-compatible provider).
func (a *Analyzer) ProviderStatus(ctx context.Context, force bool) *ProviderStatus {
	c := a.Config()
	override := strings.TrimSpace(env.Get("AI_STATUS_URL"))
	key := c.Provider + "|" + c.BaseURL + "|" + override
	a.status.mu.Lock()
	if !force && a.status.last != nil && a.status.key == key && time.Since(a.status.when) < statusTTL {
		s := *a.status.last
		a.status.mu.Unlock()
		return &s
	}
	a.status.mu.Unlock()

	s := a.checkStatus(ctx, c, override)
	a.status.mu.Lock()
	a.status.key, a.status.when, a.status.last = key, time.Now(), s
	a.status.mu.Unlock()
	cp := *s
	return &cp
}

func (a *Analyzer) checkStatus(ctx context.Context, c Config, override string) *ProviderStatus {
	s := &ProviderStatus{Provider: c.Provider, Name: c.Provider, Indicator: "unknown", Source: "none",
		Components: []StatusComponent{}, Incidents: []StatusIncident{}, CheckedAt: time.Now().UnixMilli()}
	if p := ProviderByID(c.Provider); p != nil {
		s.Name = p.Name
	}
	if c.Provider == "" {
		return s
	}
	page := statusPages[c.Provider]
	api, keep := page.api, page.component
	s.PageURL = page.page
	if override != "" {
		api, keep = override, nil
		s.PageURL = strings.TrimSuffix(strings.TrimSuffix(override, "/api/v2/summary.json"), "/")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	switch {
	case api != "":
		s.Source = "statuspage"
		if err := readStatusPage(ctx, api, keep, s); err != nil {
			s.Indicator, s.Error = "unknown", err.Error()
		}
	case c.Provider == "ollama":
		s.Source = "local"
		version, err := ollamaVersion(ctx, c.withDefaults().BaseURL)
		if err != nil {
			s.Indicator, s.Error, s.Description = "major", err.Error(), c.withDefaults().BaseURL
		} else {
			s.Indicator, s.Description = "none", version
		}
	}
	return s
}

var statusClient = &http.Client{Timeout: 5 * time.Second}

func getJSON(ctx context.Context, url string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "tracereports")
	resp, err := statusClient.Do(req)
	if err != nil {
		return fmt.Errorf("no se pudo consultar %s: %w", req.URL.Host, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s respondió HTTP %d", req.URL.Host, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return fmt.Errorf("%s: respuesta inesperada: %w", req.URL.Host, err)
	}
	return nil
}

// readStatusPage reads a Statuspage summary: overall indicator, the components that matter
// and the open incidents.
func readStatusPage(ctx context.Context, url string, keep func(string) bool, s *ProviderStatus) error {
	var sum struct {
		Status struct {
			Indicator   string `json:"indicator"`
			Description string `json:"description"`
		} `json:"status"`
		Components []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		} `json:"components"`
		Incidents []struct {
			Name      string `json:"name"`
			Status    string `json:"status"`
			Impact    string `json:"impact"`
			Shortlink string `json:"shortlink"`
			UpdatedAt string `json:"updated_at"`
		} `json:"incidents"`
	}
	if err := getJSON(ctx, url, &sum); err != nil {
		return err
	}
	if sum.Status.Indicator == "" {
		return fmt.Errorf("la página de estado no informó un estado")
	}
	s.Indicator, s.Description = sum.Status.Indicator, sum.Status.Description
	seen := map[string]bool{}
	for _, c := range sum.Components {
		if (keep == nil || keep(c.Name)) && !seen[c.Name] {
			seen[c.Name] = true
			s.Components = append(s.Components, StatusComponent{Name: c.Name, Status: c.Status})
		}
	}
	for _, i := range sum.Incidents {
		if i.Status == "resolved" || i.Status == "postmortem" {
			continue
		}
		s.Incidents = append(s.Incidents, StatusIncident{Name: i.Name, Status: i.Status, Impact: i.Impact, URL: i.Shortlink, UpdatedAt: i.UpdatedAt})
	}
	return nil
}

func ollamaVersion(ctx context.Context, base string) (string, error) {
	var v struct {
		Version string `json:"version"`
	}
	if err := getJSON(ctx, strings.TrimRight(base, "/")+"/api/version", &v); err != nil {
		return "", err
	}
	return v.Version, nil
}
