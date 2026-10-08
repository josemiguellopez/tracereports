package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Retries of webhook sends. Each message is stored per channel (db.Delivery) before the first
// attempt. Transient failures (network, timeouts, 408, 425, 429 and 5xx) are retried with
// exponential backoff and jitter, honoring Retry-After; other 4xx (a revoked webhook, a payload
// the channel rejects) and a channel that is no longer configured fail at once. Deliveries left
// pending by a restart are resumed by Start.
//
// It is "at least once": if the process dies after the channel received a message but before
// it was marked sent, the retry sends it again. Within a run the same message is not queued twice.
const (
	maxDeliveryAttempts = 8
	deliveryLease       = 2 * time.Minute // plazo de un intento en curso: si el proceso muere, se reintenta después
	deliveryPoll        = 15 * time.Second
	deliveryKeep        = 30 * 24 * time.Hour // los enviados o fallidos se borran después
)

// Backoff is the wait before attempt n+1 (n >= 1): 30 s, 1 min, 2 min... up to 1 h, ±20 %.
// A variable so tests can shorten it.
var Backoff = func(n int) time.Duration {
	d := 30 * time.Second << min(n-1, 7)
	d = min(d, time.Hour)
	return time.Duration(float64(d) * (0.8 + 0.4*rand.Float64()))
}

// maxRetryAfter caps the wait a channel can ask for with Retry-After.
const maxRetryAfter = 6 * time.Hour

// httpError is a webhook answer that is not 2xx.
type httpError struct {
	status     int
	body       string
	retryAfter time.Duration
}

func (e *httpError) Error() string { return fmt.Sprintf("HTTP %d: %s", e.status, e.body) }

// transient reports whether a failed send may work later, and how long the channel asked to wait.
func transient(err error) (bool, time.Duration) {
	var he *httpError
	if errors.As(err, &he) {
		switch {
		case he.status == http.StatusTooManyRequests, he.status == http.StatusRequestTimeout, he.status == http.StatusTooEarly,
			he.status >= 500:
			return true, he.retryAfter
		}
		return false, 0
	}
	// red: conexión rechazada o cortada, DNS, timeouts (el cliente HTTP los envuelve en url.Error)
	var ne net.Error
	return errors.As(err, &ne) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled), 0
}

// parseRetryAfter reads seconds or an HTTP date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(v); err == nil && secs > 0 {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(v); err == nil {
		d = t.Sub(now)
	}
	if d < 0 {
		return 0
	}
	return min(d, maxRetryAfter)
}

func (n *Notifier) urlFor(channel string) string {
	switch channel {
	case "teams":
		return n.teams
	case "slack":
		return n.slack
	}
	return ""
}

func payloadKey(prefix string, body []byte) string {
	sum := sha256.Sum256(body)
	return prefix + ":" + hex.EncodeToString(sum[:8])
}

// errQueued: the send failed for a transient reason and will be retried.
type errQueued struct {
	err  error
	next time.Time
}

func (e errQueued) Error() string {
	return fmt.Sprintf("%v (se reintentará automáticamente desde las %s)", e.err, e.next.Format("15:04"))
}
func (e errQueued) Unwrap() error { return e.err }

// deliver stores the message for channel (unless the same one is already there) and attempts
// it now. onlyPending: see db.EnqueueDelivery. nil = sent.
func (n *Notifier) deliver(ctx context.Context, kind, key, channel string, payload any, onlyPending bool) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if n.store == nil { // sin base (p. ej. herramientas): un solo intento, como antes
		return n.postRaw(ctx, n.urlFor(channel), body) // ya sin la URL (ver sanitize.go)
	}
	d, created, err := n.store.EnqueueDelivery(&db.Delivery{DedupKey: key, Channel: channel, Kind: kind, Payload: body}, onlyPending)
	if err != nil {
		return err
	}
	if !created {
		switch d.State {
		case db.DeliverySent:
			return nil // ya llegó: no se repite
		case db.DeliveryFailed:
			return errors.New(d.LastError)
		}
	}
	return n.attempt(ctx, d.ID)
}

// attempt sends one pending delivery if it is due and records the outcome.
func (n *Notifier) attempt(ctx context.Context, id int64) error {
	now := time.Now()
	d, err := n.store.ClaimDelivery(id, now.Add(deliveryLease).UnixMilli())
	if err != nil {
		return err
	}
	if d == nil { // otro intento en curso, o esperando su próximo turno
		cur, err := n.store.GetDelivery(id)
		if err != nil {
			return err
		}
		if cur.State == db.DeliverySent {
			return nil
		}
		return errQueued{err: errors.New(strings.TrimSpace("pendiente " + cur.LastError)), next: time.UnixMilli(cur.NextAt)}
	}
	url := n.urlFor(d.Channel)
	// lo que se guarda, se registra o se devuelve pasa otra vez por el filtro (segunda capa)
	safe := func(err error) string { return clipText(scrubWebhook(err.Error(), url), 500) }
	if url == "" {
		err := fmt.Errorf("el canal %s ya no está configurado", d.Channel)
		_ = n.store.FinishDelivery(d.ID, db.DeliveryFailed, 0, err.Error())
		return err
	}
	err = n.postRaw(ctx, url, d.Payload)
	if err == nil {
		return n.store.FinishDelivery(d.ID, db.DeliverySent, 0, "")
	}
	retry, wait := transient(err) // se clasifica el error original (status, Retry-After, red)
	msg := safe(err)
	if !retry || d.Attempts >= maxDeliveryAttempts {
		if retry {
			msg = fmt.Sprintf("%s (sin éxito tras %d intentos)", msg, d.Attempts)
		}
		_ = n.store.FinishDelivery(d.ID, db.DeliveryFailed, 0, msg)
		slog.Warn("notify: delivery failed", "channel", d.Channel, "kind", d.Kind, "attempts", d.Attempts, "err", msg)
		return errors.New(msg)
	}
	next := time.Now().Add(max(wait, Backoff(d.Attempts)))
	_ = n.store.FinishDelivery(d.ID, db.DeliveryPending, next.UnixMilli(), msg)
	slog.Info("notify: delivery will be retried", "channel", d.Channel, "kind", d.Kind, "attempt", d.Attempts, "next", next.Format(time.RFC3339), "err", msg)
	return errQueued{err: errors.New(msg), next: next}
}

// RetryDue attempts every pending delivery whose time has come (also those a restart left).
func (n *Notifier) RetryDue(ctx context.Context) {
	if n.store == nil {
		return
	}
	ids, err := n.store.DueDeliveries(50)
	if err != nil {
		slog.Error("notify: due deliveries", "err", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		actx, cancel := context.WithTimeout(ctx, 30*time.Second)
		_ = n.attempt(actx, id)
		cancel()
	}
}

// Start retries pending deliveries in the background until ctx ends (call once at startup).
func (n *Notifier) Start(ctx context.Context) {
	if n.store == nil {
		return
	}
	go func() {
		for {
			n.RetryDue(ctx)
			_ = n.store.PruneDeliveries(time.Now().Add(-deliveryKeep).UnixMilli())
			select {
			case <-ctx.Done():
				return
			case <-time.After(deliveryPoll):
			}
		}
	}()
}
