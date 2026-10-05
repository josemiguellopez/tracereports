package orangehrm

import (
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/mxschmitt/playwright-go"

	tracereports "github.com/josemiguellopez/tracereports/client/go"
)

// netCapture records the network calls of one page (the test's "Red" tab in TraceReports).
// Attach it right after creating the page, before the first Goto: Playwright has no
// retroactive buffer.
type netCapture struct {
	mu    sync.Mutex
	conns []*tracereports.Conn
	byReq map[playwright.Request]*tracereports.Conn
	start map[playwright.Request]time.Time
}

var sensitiveHeaders = map[string]bool{"authorization": true, "cookie": true, "set-cookie": true, "x-auth-token": true}
var sensitiveFields = regexp.MustCompile(`(?i)("(?:token|password|pwd|session|auth)"\s*:\s*")[^"]*(")|((?:token|password|pwd|session|auth)=)[^&\s"]+`)

func mask(s string) string {
	return sensitiveFields.ReplaceAllString(s, "$1$3<masked>$2")
}

func maskHeaders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if sensitiveHeaders[strings.ToLower(k)] {
			v = "<masked>"
		}
		out[k] = mask(v)
	}
	return out
}

func captureNetwork(page playwright.Page) *netCapture {
	nc := &netCapture{byReq: map[playwright.Request]*tracereports.Conn{}, start: map[playwright.Request]time.Time{}}
	page.OnRequest(func(r playwright.Request) {
		post, _ := r.PostData()
		c := &tracereports.Conn{
			Method: r.Method(), URL: mask(r.URL()), ResourceType: r.ResourceType(),
			RequestHeaders: maskHeaders(r.Headers()), PostData: mask(post), StartedAt: time.Now().UnixMilli(),
		}
		nc.mu.Lock()
		nc.conns = append(nc.conns, c)
		nc.byReq[r], nc.start[r] = c, time.Now()
		nc.mu.Unlock()
	})
	page.OnResponse(func(resp playwright.Response) {
		headers := resp.Headers()
		nc.mu.Lock()
		defer nc.mu.Unlock()
		if c := nc.byReq[resp.Request()]; c != nil {
			c.Status, c.StatusText, c.MimeType = resp.Status(), resp.StatusText(), headers["content-type"]
			c.ResponseHeaders = maskHeaders(headers)
		}
	})
	done := func(r playwright.Request, failure error) {
		nc.mu.Lock()
		defer nc.mu.Unlock()
		c := nc.byReq[r]
		if c == nil {
			return
		}
		d := time.Since(nc.start[r]).Milliseconds()
		c.DurationMs = &d
		if failure != nil {
			c.Failed, c.ErrorText = true, failure.Error()
		}
	}
	page.OnRequestFinished(func(r playwright.Request) {
		done(r, nil)
		nc.readBody(r)
	})
	page.OnRequestFailed(func(r playwright.Request) { done(r, r.Failure()) })
	return nc
}

// readBody guarda el body de llamadas API, XHR/fetch y errores. Se lee en RequestFinished,
// dentro del evento: ya está completo y el navegador todavía lo tiene (después lo descarta).
func (nc *netCapture) readBody(r playwright.Request) {
	nc.mu.Lock()
	c := nc.byReq[r]
	nc.mu.Unlock()
	if c == nil || !(strings.Contains(c.URL, "/api/") || c.ResourceType == "xhr" || c.ResourceType == "fetch" || c.Status >= 400) {
		return
	}
	resp, err := r.Response()
	if err != nil || resp == nil {
		return
	}
	if body, err := resp.Text(); err == nil { // los redirects no tienen body
		nc.mu.Lock()
		c.BodySize, c.ResponseBody = int64(len(body)), mask(body)
		nc.mu.Unlock()
	}
}

// all returns a copy of the captured calls (safe to send while events keep arriving).
func (nc *netCapture) all() []tracereports.Conn {
	nc.mu.Lock()
	defer nc.mu.Unlock()
	out := make([]tracereports.Conn, len(nc.conns))
	for i, c := range nc.conns {
		out[i] = *c
	}
	return out
}
