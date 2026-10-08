package notify

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// marcador ficticio: hace de credencial en el path y en la query del webhook
const secretMark = "SECRET-MARK-9f3a1c"

// rtFunc es un http.RoundTripper simulado: nada sale a la red.
type rtFunc func(*http.Request) (*http.Response, error)

func (f rtFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(old) })
	return buf
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func secretNotifier(t *testing.T, rt http.RoundTripper) (*Notifier, *db.Store, int64) {
	t.Helper()
	t.Setenv("TEAMS_WEBHOOK_URL", "https://hooks.example.test/workflows/"+secretMark+"/run?sig="+secretMark+"-q")
	t.Setenv("SLACK_WEBHOOK_URL", "")
	t.Setenv("NOTIFY_ON", "")
	store, runID := failedRun(t)
	n := New(store)
	n.client = &http.Client{Transport: rt, Timeout: 5 * time.Second}
	return n, store, runID
}

func assertNoSecret(t *testing.T, where, s string) {
	t.Helper()
	if strings.Contains(s, secretMark) || strings.Contains(s, "workflows/") {
		t.Fatalf("%s leaks the webhook URL: %s", where, s)
	}
}

func TestTransportErrorsDoNotLeakTheWebhookURL(t *testing.T) {
	noBackoff(t)
	logs := captureLogs(t)
	var fail = true
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		if fail {
			// un transporte que repite la URL en su error (como hace net/http con *url.Error)
			return nil, fmt.Errorf("dial to %s: %w", r.URL, syscall.ECONNREFUSED)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{}}, nil
	})
	n, store, runID := secretNotifier(t, rt)

	n.RunFinished(runID)
	d := deliveriesOf(t, store, 1)[0]
	assertNoSecret(t, "deliveries.last_error", d.LastError)
	if d.State != db.DeliveryPending || !strings.Contains(d.LastError, "conexión rechazada") || !strings.Contains(d.LastError, "hooks.example.test") {
		t.Fatalf("transient, with a useful reason and the host: %+v", d)
	}
	// error devuelto (envío a pedido)
	err := n.SendEscalation(context.Background(), "teams", &Escalation{Title: "x"})
	if err == nil {
		t.Fatal("must fail")
	}
	assertNoSecret(t, "returned error", err.Error())
	if !strings.Contains(err.Error(), "se reintentará") {
		t.Fatalf("still classified as transient: %v", err)
	}
	// el reintento sigue funcionando
	fail = false
	n.RetryDue(context.Background())
	if deliveriesOf(t, store, 1)[0].State != db.DeliverySent {
		t.Fatal("retried and sent")
	}
	assertNoSecret(t, "logs", logs.String())
	if !strings.Contains(logs.String(), "channel=teams") {
		t.Fatalf("the log keeps the channel: %s", logs.String())
	}
}

func TestReceiverAnswerAndBadURLAreScrubbed(t *testing.T) {
	noBackoff(t)
	logs := captureLogs(t)
	// el receptor devuelve la URL (y Retry-After) en su respuesta
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		h := http.Header{}
		h.Set("Retry-After", "90")
		return &http.Response{StatusCode: 503, Header: h,
			Body: io.NopCloser(strings.NewReader("overloaded, retry " + r.URL.String() + " path=" + r.URL.Path))}, nil
	})
	n, store, runID := secretNotifier(t, rt)
	before := time.Now()
	n.RunFinished(runID)
	d := deliveriesOf(t, store, 1)[0]
	assertNoSecret(t, "last_error (receiver echo)", d.LastError)
	if !strings.Contains(d.LastError, "HTTP 503") || d.State != db.DeliveryPending {
		t.Fatalf("status kept: %+v", d)
	}
	if wait := time.UnixMilli(d.NextAt).Sub(before); wait < 85*time.Second {
		t.Fatalf("Retry-After still honored: %s", wait)
	}
	assertNoSecret(t, "logs", logs.String())

	// una URL que ni siquiera se puede armar: permanente y sin repetirla
	t.Setenv("TEAMS_WEBHOOK_URL", "https://hooks.example.test/x/"+secretMark+"\x7f")
	n2 := New(store)
	err := n2.SendEscalation(context.Background(), "teams", &Escalation{Title: "y"})
	if err == nil {
		t.Fatal("must fail")
	}
	assertNoSecret(t, "bad URL error", err.Error())
	var reqErr *requestError
	if retry, _ := transient(&requestError{}); retry || !errors.As(fmt.Errorf("w: %w", &requestError{}), &reqErr) {
		t.Fatal("a malformed URL is permanent")
	}
}

func TestTimeoutsStayTransient(t *testing.T) {
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	n, _, _ := secretNotifier(t, rt)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := n.postRaw(ctx, n.teams, []byte(`{}`))
	assertNoSecret(t, "timeout error", err.Error())
	if retry, _ := transient(err); !retry || !strings.Contains(err.Error(), "tiempo de espera") {
		t.Fatalf("timeout: %v", err)
	}
}
