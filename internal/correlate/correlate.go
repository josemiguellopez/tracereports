// Package correlate finds the identifiers that link a browser call to the backend logs and
// traces: the distributed trace id (W3C traceparent/traceresponse, B3, Jaeger, AWS X-Ray,
// Datadog, Google Cloud) and the request id (X-Request-Id and friends). It also expands the
// link templates that open those logs or traces (Grafana, Kibana, Datadog, Jaeger...).
package correlate

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// IDs found in the headers of one call.
type IDs struct {
	TraceID   string
	RequestID string
}

var (
	hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
	hexID = regexp.MustCompile(`^[0-9a-f]{16}([0-9a-f]{16})?$`)
	// headers de request id, en orden de preferencia
	requestHeaders = []string{"x-request-id", "x-correlation-id", "request-id", "correlation-id", "x-amzn-requestid",
		"x-amz-request-id", "x-ms-request-id", "x-github-request-id", "cf-ray"}
)

// Extract reads the ids from the response headers first (what the backend says it used), then
// from the request headers (what the page sent). Header names are case-insensitive.
func Extract(request, response map[string]string) IDs {
	var ids IDs
	for _, h := range []map[string]string{response, request} {
		get := lower(h)
		if ids.TraceID == "" {
			ids.TraceID = traceID(get)
		}
		if ids.RequestID == "" {
			for _, name := range requestHeaders {
				if v := clean(get[name]); v != "" {
					ids.RequestID = v
					break
				}
			}
		}
	}
	return ids
}

func lower(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(strings.TrimSpace(k))] = v
	}
	return out
}

// clean keeps an id usable in a URL and a log search: no spaces or masked values, sane length.
func clean(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || len(v) > 200 || strings.ContainsAny(v, " \t\r\n<>\"") || strings.Contains(v, "masked") {
		return ""
	}
	return v
}

func traceID(h map[string]string) string {
	// W3C: 00-<trace 32 hex>-<span 16 hex>-<flags>
	for _, name := range []string{"traceparent", "traceresponse"} {
		if parts := strings.Split(strings.ToLower(strings.TrimSpace(h[name])), "-"); len(parts) >= 4 && hex32.MatchString(parts[1]) && parts[1] != strings.Repeat("0", 32) {
			return parts[1]
		}
	}
	// B3 (Zipkin): x-b3-traceid, o el header único "b3: trace-span-sampled"
	if v := strings.ToLower(strings.TrimSpace(h["x-b3-traceid"])); hexID.MatchString(v) {
		return v
	}
	if v := strings.ToLower(strings.TrimSpace(h["b3"])); v != "" {
		if t := strings.Split(v, "-")[0]; hexID.MatchString(t) {
			return t
		}
	}
	// Jaeger: trace:span:parent:flags (el trace puede venir sin ceros a la izquierda)
	if v := strings.ToLower(strings.TrimSpace(h["uber-trace-id"])); v != "" {
		if t := strings.Split(v, ":")[0]; t != "" && len(t) <= 32 && strings.Trim(t, "0123456789abcdef") == "" {
			return t
		}
	}
	// AWS X-Ray: Root=1-5759e988-bd862e3fe1be46a994272793;Parent=...;Sampled=1
	if v := h["x-amzn-trace-id"]; v != "" {
		for _, part := range strings.Split(v, ";") {
			if k, val, ok := strings.Cut(strings.TrimSpace(part), "="); ok && strings.EqualFold(k, "Root") {
				return clean(val)
			}
		}
	}
	// Datadog: decimal
	if v := strings.TrimSpace(h["x-datadog-trace-id"]); v != "" {
		if _, err := strconv.ParseUint(v, 10, 64); err == nil {
			return v
		}
	}
	// Google Cloud: TRACE_ID/SPAN_ID;o=1
	if v := strings.ToLower(strings.TrimSpace(h["x-cloud-trace-context"])); v != "" {
		if t := strings.Split(v, "/")[0]; hex32.MatchString(t) {
			return t
		}
	}
	return clean(h["x-trace-id"])
}

// Call is what a link template can use.
type Call struct {
	IDs
	Method    string
	URL       string
	Status    int
	StartedAt int64 // Unix ms
	Duration  int64 // ms
}

// Window around a call used for {from} and {to}: logs a little before and after it.
const Window = 2 * time.Minute

// Link expands a template with the call's values, URL-encoded. Placeholders: {trace_id},
// {request_id}, {from} and {to} (Unix ms), {from_iso} and {to_iso} (RFC 3339), {from_s} and {to_s}
// (Unix seconds), {host}, {path}, {method}, {status}. It returns "" when the template needs an id
// the call does not have, so the UI never offers a link that opens an empty search.
func Link(template string, c Call) string {
	template = strings.TrimSpace(template)
	if template == "" {
		return ""
	}
	if (strings.Contains(template, "{trace_id}") && c.TraceID == "") || (strings.Contains(template, "{request_id}") && c.RequestID == "") {
		return ""
	}
	if c.StartedAt <= 0 && strings.Contains(template, "{from") {
		return ""
	}
	start := time.UnixMilli(c.StartedAt)
	from, to := start.Add(-Window), start.Add(time.Duration(c.Duration)*time.Millisecond+Window)
	host, path := "", ""
	if u, err := url.Parse(c.URL); err == nil {
		host, path = u.Host, u.Path
	}
	r := strings.NewReplacer(
		"{trace_id}", url.QueryEscape(c.TraceID),
		"{request_id}", url.QueryEscape(c.RequestID),
		"{from}", strconv.FormatInt(from.UnixMilli(), 10),
		"{to}", strconv.FormatInt(to.UnixMilli(), 10),
		"{from_s}", strconv.FormatInt(from.Unix(), 10),
		"{to_s}", strconv.FormatInt(to.Unix(), 10),
		"{from_iso}", url.QueryEscape(from.UTC().Format(time.RFC3339)),
		"{to_iso}", url.QueryEscape(to.UTC().Format(time.RFC3339)),
		"{host}", url.QueryEscape(host),
		"{path}", url.QueryEscape(path),
		"{method}", url.QueryEscape(c.Method),
		"{status}", strconv.Itoa(c.Status),
	)
	return r.Replace(template)
}
