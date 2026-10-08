package notify

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"strings"
	"syscall"
)

// A webhook URL is a credential: Slack and Teams carry the secret in the path, others in the
// query. Errors of net/http repeat the URL ("Post \"https://…\": …"), and a receiver may echo it
// in its answer, so every error text that leaves postRaw (stored in deliveries.last_error,
// logged or returned to the UI) is rebuilt from what is safe: the host, the kind of failure and
// the HTTP status. The original error is never kept.

const webhookMask = "<webhook>"

// transportError is a network failure talking to the webhook, without its URL. It implements
// net.Error so transient() keeps classifying it as retryable.
type transportError struct {
	msg     string
	timeout bool
}

func (e *transportError) Error() string   { return e.msg }
func (e *transportError) Timeout() bool   { return e.timeout }
func (e *transportError) Temporary() bool { return true }

// requestError: the request could not even be built (a malformed webhook URL). Permanent.
type requestError struct{ msg string }

func (e *requestError) Error() string { return e.msg }

// hostOf is the host of the webhook, the only part of the URL that is shown.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "webhook"
}

// transportFailure turns an error of http.Client.Do into a transportError: what failed (timeout,
// refused connection, DNS, TLS, cancelled) and the host, never the error text itself (it may
// contain the URL, or anything a custom transport put there).
func transportFailure(err error, rawURL string) error {
	kind := "error de red"
	var dnsErr *net.DNSError
	var tlsErr *tls.CertificateVerificationError
	var ne net.Error
	timeout := errors.As(err, &ne) && ne.Timeout()
	switch {
	case errors.Is(err, context.Canceled):
		kind = "envío cancelado"
	case timeout || errors.Is(err, context.DeadlineExceeded):
		kind, timeout = "tiempo de espera agotado", true
	case errors.As(err, &dnsErr):
		kind = "no se pudo resolver el nombre del servidor"
	case errors.Is(err, syscall.ECONNREFUSED):
		kind = "conexión rechazada"
	case errors.Is(err, syscall.ECONNRESET):
		kind = "conexión cortada"
	case errors.As(err, &tlsErr):
		kind = "certificado TLS no válido"
	}
	return &transportError{msg: kind + " al contactar " + hostOf(rawURL), timeout: timeout}
}

// scrubWebhook removes from s every part of the webhook URL that may be secret: the whole URL
// (raw and escaped), its path, its query and each path segment or query value long enough to be
// a token. Used on what the receiver answers, which is stored as the error detail.
func scrubWebhook(s, rawURL string) string {
	if s == "" || rawURL == "" {
		return s
	}
	parts := []string{rawURL, url.QueryEscape(rawURL), strings.TrimPrefix(strings.TrimPrefix(rawURL, "https://"), "http://")}
	if u, err := url.Parse(rawURL); err == nil {
		parts = append(parts, u.RawQuery, u.EscapedPath(), u.Path)
		for _, seg := range strings.Split(u.Path, "/") {
			if len(seg) >= 6 {
				parts = append(parts, seg, url.PathEscape(seg))
			}
		}
		for _, vs := range u.Query() {
			for _, v := range vs {
				if len(v) >= 6 {
					parts = append(parts, v, url.QueryEscape(v))
				}
			}
		}
		if u.User != nil {
			parts = append(parts, u.User.String())
		}
	}
	for _, p := range parts {
		if len(p) >= 6 && p != "/" {
			s = strings.ReplaceAll(s, p, webhookMask)
		}
	}
	return s
}
