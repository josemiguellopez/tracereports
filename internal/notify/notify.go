// Package notify sends the summary of a finished run to Microsoft Teams and/or Slack.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Notifier posts run summaries to chat webhooks configured by environment variables:
//
//	TEAMS_WEBHOOK_URL  Teams Workflows webhook ("When a Teams webhook request is received")
//	SLACK_WEBHOOK_URL  Slack Incoming Webhook
//	PUBLIC_URL         base URL of this server, for the "Ver reporte" button
//	NOTIFY_ON          "always" (default) or "failures"
type Notifier struct {
	store     *db.Store
	teams     string
	slack     string
	publicURL string
	onlyFails bool
	client    *http.Client
}

// New builds a Notifier from the environment. It is a no-op when no webhook is set.
func New(store *db.Store) *Notifier {
	return &Notifier{
		store:     store,
		teams:     strings.TrimSpace(os.Getenv("TEAMS_WEBHOOK_URL")),
		slack:     strings.TrimSpace(os.Getenv("SLACK_WEBHOOK_URL")),
		publicURL: strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_URL")), "/"),
		onlyFails: strings.EqualFold(os.Getenv("NOTIFY_ON"), "failures"),
		client:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Enabled reports whether at least one webhook is configured.
func (n *Notifier) Enabled() bool { return n.teams != "" || n.slack != "" }

// Message is the channel-agnostic content of a run notification.
type Message struct {
	Title   string
	Passed  bool
	Stats   string
	Summary string
	Bullets []string
	Changes string
	URL     string
}

// RunFinished sends the summary of a run to every configured webhook.
func (n *Notifier) RunFinished(runID int64) {
	if !n.Enabled() {
		return
	}
	msg, err := n.build(runID)
	if err != nil {
		slog.Error("notify: build message", "run_id", runID, "err", err)
		return
	}
	if msg == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// cada canal por separado: uno que ya lo recibió no se reenvía si el otro falla. El mismo
	// resumen de la misma ejecución no se envía dos veces; uno distinto (evidencia tardía) sí.
	for _, ch := range []struct {
		name    string
		payload any
	}{{"teams", TeamsPayload(msg)}, {"slack", SlackPayload(msg)}} {
		if n.urlFor(ch.name) == "" {
			continue
		}
		raw, _ := json.Marshal(ch.payload)
		if err := n.deliver(ctx, "run", payloadKey(fmt.Sprintf("run:%d", runID), raw), ch.name, ch.payload, false); err != nil {
			slog.Warn("notify: "+ch.name, "run_id", runID, "err", err)
		}
	}
}

func (n *Notifier) build(runID int64) (*Message, error) {
	run, err := n.store.GetRunDetail(runID)
	if err != nil {
		return nil, err
	}
	if n.onlyFails && run.Failed == 0 {
		return nil, nil
	}
	m := &Message{Passed: run.Failed == 0}
	icon := "✅"
	if !m.Passed {
		icon = "❌"
	}
	m.Title = fmt.Sprintf("%s %s", icon, run.Name)
	pct := 0
	if run.Total > 0 {
		pct = run.Passed * 100 / run.Total
	}
	dur := ""
	if run.EndedAt != nil {
		dur = " · " + (time.Duration(*run.EndedAt-run.StartedAt) * time.Millisecond).Round(time.Second).String()
	}
	m.Stats = fmt.Sprintf("%d tests · %d OK · %d fallidos · %d omitidos · %d%% correctos%s",
		run.Total, run.Passed, run.Failed, run.Skipped, pct, dur)
	if run.Summary != nil {
		m.Summary = strings.TrimSpace(run.Summary.Headline + " " + run.Summary.Summary)
		for i, in := range run.Summary.Incidents {
			if i == 3 {
				m.Bullets = append(m.Bullets, fmt.Sprintf("… y %d causas más", len(run.Summary.Incidents)-3))
				break
			}
			b := fmt.Sprintf("%s (%s)", in.Title, plural(len(in.TestIDs), "test", "tests"))
			if in.Action != "" {
				b += ": " + in.Action
			}
			m.Bullets = append(m.Bullets, b)
		}
	}
	if cmp, err := n.store.CompareRuns(runID, 0); err == nil && cmp.BaseRun != nil {
		flaky := 0
		for _, t := range run.Tests {
			if t.Flaky {
				flaky++
			}
		}
		m.Changes = fmt.Sprintf("Respecto de #%d: %d fallos nuevos · %d arreglados · %d siguen fallando · %d flaky",
			cmp.BaseRun.ID, len(cmp.NewFailures), len(cmp.Fixed), len(cmp.StillFailing), flaky)
	}
	if n.publicURL != "" {
		m.URL = fmt.Sprintf("%s/#run=%d&view=dashboard", n.publicURL, runID)
	}
	return m, nil
}

// TeamsPayload renders an Adaptive Card, accepted by Teams Workflows webhooks.
func TeamsPayload(m *Message) map[string]any {
	color := "Good"
	if !m.Passed {
		color = "Attention"
	}
	body := []map[string]any{
		{"type": "TextBlock", "text": m.Title, "weight": "Bolder", "size": "Medium", "color": color, "wrap": true},
		{"type": "TextBlock", "text": m.Stats, "isSubtle": true, "spacing": "None", "wrap": true},
	}
	if m.Summary != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": m.Summary, "wrap": true})
	}
	if len(m.Bullets) > 0 {
		body = append(body, map[string]any{"type": "TextBlock", "text": "- " + strings.Join(m.Bullets, "\n- "), "wrap": true})
	}
	if m.Changes != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": m.Changes, "isSubtle": true, "wrap": true})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"body":    body,
	}
	if m.URL != "" {
		card["actions"] = []map[string]any{{"type": "Action.OpenUrl", "title": "Ver reporte", "url": m.URL}}
	}
	return map[string]any{
		"type":        "message",
		"attachments": []map[string]any{{"contentType": "application/vnd.microsoft.card.adaptive", "content": card}},
	}
}

// SlackPayload renders Block Kit blocks with a plain-text fallback.
func SlackPayload(m *Message) map[string]any {
	text := "*" + m.Title + "*\n" + m.Stats
	if m.Summary != "" {
		text += "\n\n" + m.Summary
	}
	if len(m.Bullets) > 0 {
		text += "\n• " + strings.Join(m.Bullets, "\n• ")
	}
	if m.Changes != "" {
		text += "\n_" + m.Changes + "_"
	}
	blocks := []map[string]any{{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": text}}}
	if m.URL != "" {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": []map[string]any{{
			"type": "button", "text": map[string]any{"type": "plain_text", "text": "Ver reporte"}, "url": m.URL,
		}}})
	}
	return map[string]any{"text": m.Title + " — " + m.Stats, "blocks": blocks}
}

// postRaw makes one POST of a JSON body; a non-2xx answer is an *httpError (with Retry-After).
// No error it returns contains the webhook URL (a credential): see sanitize.go.
func (n *Notifier) postRaw(ctx context.Context, url string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return &requestError{msg: "la URL del webhook no es válida (" + hostOf(url) + ")"}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		return transportFailure(err, url)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &httpError{status: resp.StatusCode, body: scrubWebhook(strings.ToValidUTF8(string(raw), ""), url),
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// ---------- escalamientos (resumen de un fallo, enviado a pedido desde la UI) ----------

// Escalation is the channel-agnostic content of an escalation summary.
type Escalation struct {
	Title     string
	Severity  string // etiqueta ya traducida, p. ej. "Severidad alta"
	Critical  bool
	Headline  string
	Sections  [][2]string // [título, texto]
	Lists     [][2]any    // [título, []string]
	ImageURL  string      // absoluta y pública (solo con PUBLIC_URL)
	URL       string      // link al reporte (solo con PUBLIC_URL)
	LinkLabel string
}

// Channels tells which webhooks are configured.
func (n *Notifier) Channels() (teams, slack bool) { return n.teams != "", n.slack != "" }

// PublicURL is the server's public base URL ("" if not configured).
func (n *Notifier) PublicURL() string { return n.publicURL }

// SendEscalation posts an escalation to "teams" or "slack". If the channel fails for a transient
// reason the message stays queued and is retried (the error says so); asking again for the same
// message while it is queued does not queue a second one.
func (n *Notifier) SendEscalation(ctx context.Context, channel string, e *Escalation) error {
	return n.sendOnDemand(ctx, "escalation", channel, e)
}

// sendOnDemand sends a message someone asked for (an escalation, the weekly summary "send now"):
// once sent, asking again sends it again.
func (n *Notifier) sendOnDemand(ctx context.Context, kind, channel string, e *Escalation) error {
	var payload any
	switch channel {
	case "teams":
		if n.teams == "" {
			return fmt.Errorf("TEAMS_WEBHOOK_URL no está configurado")
		}
		payload = TeamsEscalation(e)
	case "slack":
		if n.slack == "" {
			return fmt.Errorf("SLACK_WEBHOOK_URL no está configurado")
		}
		payload = SlackEscalation(e)
	default:
		return fmt.Errorf("canal desconocido: %q", channel)
	}
	raw, _ := json.Marshal(payload)
	return n.deliver(ctx, kind, payloadKey(kind, raw), channel, payload, true)
}

// TeamsEscalation renders an escalation as an Adaptive Card.
func TeamsEscalation(e *Escalation) map[string]any {
	color := "Warning"
	if e.Critical {
		color = "Attention"
	}
	body := []map[string]any{
		{"type": "TextBlock", "text": e.Severity, "weight": "Bolder", "color": color, "size": "Small", "spacing": "None"},
		{"type": "TextBlock", "text": e.Title, "weight": "Bolder", "size": "Large", "wrap": true, "spacing": "Small"},
		{"type": "TextBlock", "text": e.Headline, "wrap": true},
	}
	for _, s := range e.Sections {
		if s[1] == "" {
			continue
		}
		body = append(body,
			map[string]any{"type": "TextBlock", "text": s[0], "weight": "Bolder", "spacing": "Medium", "wrap": true},
			map[string]any{"type": "TextBlock", "text": s[1], "wrap": true, "spacing": "None"})
	}
	for _, l := range e.Lists {
		items, _ := l[1].([]string)
		if len(items) == 0 {
			continue
		}
		body = append(body,
			map[string]any{"type": "TextBlock", "text": l[0].(string), "weight": "Bolder", "spacing": "Medium", "wrap": true},
			map[string]any{"type": "TextBlock", "text": "- " + strings.Join(items, "\n- "), "wrap": true, "spacing": "None"})
	}
	if e.ImageURL != "" {
		body = append(body, map[string]any{"type": "Image", "url": e.ImageURL, "size": "Stretch", "spacing": "Medium"})
	}
	card := map[string]any{"$schema": "http://adaptivecards.io/schemas/adaptive-card.json", "type": "AdaptiveCard", "version": "1.4", "body": body}
	if e.URL != "" {
		card["actions"] = []map[string]any{{"type": "Action.OpenUrl", "title": e.LinkLabel, "url": e.URL}}
	}
	return map[string]any{"type": "message",
		"attachments": []map[string]any{{"contentType": "application/vnd.microsoft.card.adaptive", "content": card}}}
}

// SlackEscalation renders an escalation as Block Kit blocks.
func SlackEscalation(e *Escalation) map[string]any {
	blocks := []map[string]any{
		{"type": "header", "text": map[string]any{"type": "plain_text", "text": clipText(e.Title, 150)}},
		{"type": "context", "elements": []map[string]any{{"type": "mrkdwn", "text": "*" + e.Severity + "*"}}},
		{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": e.Headline}},
	}
	for _, s := range e.Sections {
		if s[1] != "" {
			blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": "*" + s[0] + "*\n" + s[1]}})
		}
	}
	for _, l := range e.Lists {
		if items, _ := l[1].([]string); len(items) > 0 {
			blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn",
				"text": "*" + l[0].(string) + "*\n• " + strings.Join(items, "\n• ")}})
		}
	}
	if e.ImageURL != "" {
		blocks = append(blocks, map[string]any{"type": "image", "image_url": e.ImageURL, "alt_text": e.Title})
	}
	if e.URL != "" {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": []map[string]any{{
			"type": "button", "text": map[string]any{"type": "plain_text", "text": e.LinkLabel}, "url": e.URL}}})
	}
	return map[string]any{"text": e.Title + " — " + e.Headline, "blocks": blocks}
}

func clipText(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n-1]) + "…"
}
