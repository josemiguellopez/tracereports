package egress

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
)

func clear(t *testing.T) {
	for _, k := range []string{"TEAMS_WEBHOOK_URL", "SLACK_WEBHOOK_URL", "TRACEREPORTS_GITHUB_REPO", "TRACEREPORTS_GITHUB_TOKEN",
		"TRACEREPORTS_GITHUB_API", "TRACEREPORTS_JIRA_URL", "TRACEREPORTS_JIRA_PROJECT", "TRACEREPORTS_JIRA_TOKEN",
		"TRACEREPORTS_AZURE_URL", "TRACEREPORTS_AZURE_PROJECT", "TRACEREPORTS_AZURE_TOKEN", "TRACEREPORTS_LAB",
		"TRACEREPORTS_LAB_HOSTS", "TRACEREPORTS_AI_STATUS_URL"} {
		t.Setenv(k, "")
	}
}

// Lista IA, su página de estado, Teams/Slack, trackers y hosts del Lab, sin ningún secreto.
func TestDestinationsListEverythingWithoutSecrets(t *testing.T) {
	clear(t)
	t.Setenv("TEAMS_WEBHOOK_URL", "https://example.webhook.office.com/webhookb2/SECRET-TEAMS-PATH?x=SECRET-Q")
	t.Setenv("SLACK_WEBHOOK_URL", "https://hooks.slack.com/services/T000/B000/SECRET-SLACK")
	t.Setenv("TRACEREPORTS_GITHUB_REPO", "acme/web")
	t.Setenv("TRACEREPORTS_GITHUB_TOKEN", "ghp_SECRET")
	t.Setenv("TRACEREPORTS_JIRA_URL", "https://acme.atlassian.net")
	t.Setenv("TRACEREPORTS_JIRA_PROJECT", "QA")
	t.Setenv("TRACEREPORTS_JIRA_TOKEN", "SECRET-JIRA")
	t.Setenv("TRACEREPORTS_AZURE_URL", "https://dev.azure.com/acme")
	t.Setenv("TRACEREPORTS_AZURE_PROJECT", "web")
	t.Setenv("TRACEREPORTS_AZURE_TOKEN", "SECRET-AZURE")
	t.Setenv("TRACEREPORTS_LAB_HOSTS", "api.staging.test, 10.0.0.8:8443")
	t.Setenv("TRACEREPORTS_AI_STATUS_URL", "https://status.example.com/api/v2/summary.json")

	got := Destinations(ai.Config{Provider: "openai", APIKey: "sk-SECRET"})
	want := map[string]string{
		"ai":        "api.openai.com",
		"ai_status": "status.example.com",
		"notify":    "example.webhook.office.com|hooks.slack.com",
		"tracker":   "api.github.com|acme.atlassian.net|dev.azure.com",
		"lab":       "api.staging.test|10.0.0.8:8443",
	}
	byKind := map[string][]string{}
	for _, d := range got {
		byKind[d.Kind] = append(byKind[d.Kind], d.Host)
	}
	for kind, hosts := range want {
		if strings.Join(byKind[kind], "|") != hosts {
			t.Errorf("%s: %v, quería %s", kind, byKind[kind], hosts)
		}
	}
	raw, _ := json.Marshal(got)
	if strings.Contains(string(raw), "SECRET") {
		t.Fatalf("un secreto en la lista: %s", raw)
	}
}

// Sin nada configurado (motor local, sin webhooks) no hay destinos; el Lab apagado no cuenta.
func TestNoDestinationsByDefault(t *testing.T) {
	clear(t)
	if got := Destinations(ai.Config{Provider: ai.ProviderBuiltin}); len(got) != 0 {
		t.Fatalf("%+v", got)
	}
	t.Setenv("TRACEREPORTS_LAB_HOSTS", "api.test")
	t.Setenv("TRACEREPORTS_LAB", "off")
	if got := Destinations(ai.Config{}); len(got) != 0 {
		t.Fatalf("Lab apagado: %+v", got)
	}
}

func TestOllamaIsLocal(t *testing.T) {
	clear(t)
	got := Destinations(ai.Config{Provider: "ollama", BaseURL: "http://127.0.0.1:11434"})
	if len(got) != 2 || got[0].Scope != ScopeLoopback || got[1].Kind != "ai_status" || got[1].Host != "127.0.0.1:11434" {
		t.Fatalf("%+v", got)
	}
}

func TestScope(t *testing.T) {
	for host, want := range map[string]string{
		"localhost:11434": ScopeLoopback, "127.0.0.1": ScopeLoopback, "[::1]:8080": ScopeLoopback, "x.localhost": ScopeLoopback,
		"10.1.2.3": ScopePrivate, "192.168.0.5:443": ScopePrivate, "172.16.4.4": ScopePrivate, "[fd00::1]:80": ScopePrivate,
		"8.8.8.8": ScopePublic, "api.openai.com": ScopeName, "*.staging.test": ScopeName,
	} {
		if got := Scope(host); got != want {
			t.Errorf("%s: %s", host, got)
		}
	}
}
