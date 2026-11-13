package notify

import (
	"context"
	"log/slog"
)

// LiveAlert sends an alert of Live (a test without signal, a test much slower than usual, too many
// failures in an execution) to the configured channels. key identifies the alert: the delivery queue
// sends it once, also across restarts. With NOTIFY_DETAIL=minimal it carries no test names.
func (n *Notifier) LiveAlert(ctx context.Context, key string, m *Message) {
	if !n.Enabled() {
		return
	}
	if n.minimal {
		c := *m
		c.Bullets, c.Summary = nil, ""
		m = &c
	}
	for _, ch := range []string{"teams", "slack"} {
		if n.urlFor(ch) == "" {
			continue
		}
		var payload any = TeamsPayload(m)
		if ch == "slack" {
			payload = SlackPayload(m)
		}
		if err := n.deliver(ctx, "live_alert", "live:"+key, ch, payload, false); err != nil {
			slog.Warn("live alert: deliver", "channel", ch, "key", key, "err", err)
		}
	}
}
