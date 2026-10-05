package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

const (
	maxNetworkBody   = 48 << 20 // request size of one POST /network batch
	maxNetworkConns  = 5000     // connections per batch
	maxURLChars      = 4096
	maxHeaderChars   = 4096
	maxPostDataChars = 64 << 10
)

// maxResponseBodyChars caps each stored response body (NETWORK_MAX_BODY_KB, default 256 KB):
// catalog responses of several MB would otherwise bloat SQLite, the UI and the exported ZIP.
// NETWORK_MAX_BODY_KB=0 stores no response or request bodies at all (only status and headers).
var maxResponseBodyChars = func() int {
	if kb, err := strconv.Atoi(os.Getenv("NETWORK_MAX_BODY_KB")); err == nil && kb >= 0 {
		return kb << 10
	}
	return 256 << 10
}()

// addNetwork stores a batch of captured connections: {"connections": [...]}.
func (s *Server) addNetwork(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxNetworkBody)
	var in struct {
		Connections []db.NetConn `json:"connections"`
	}
	if !decodeLimited(w, r, &in) {
		return
	}
	if len(in.Connections) > maxNetworkConns {
		writeError(w, http.StatusRequestEntityTooLarge, "too many connections in one batch (max 5000)")
		return
	}
	errors := 0
	for i := range in.Connections {
		normalizeConn(&in.Connections[i])
		redactConn(s.redactor(), &in.Connections[i])
		if c := in.Connections[i]; (c.Failed || c.Status >= 400) && !c.Expected {
			errors++
		}
	}
	if err := s.Store.AddNetwork(id, in.Connections); respondErr(w, err, "test") {
		return
	}
	s.publishTest("network", id, map[string]int{"stored": len(in.Connections), "errors": errors})
	writeJSON(w, http.StatusCreated, map[string]int{"stored": len(in.Connections), "errors": errors})
}

func (s *Server) listNetwork(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	if _, err := s.Store.GetTest(id); respondErr(w, err, "test") {
		return
	}
	conns, err := s.Store.ListNetwork(id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, conns)
}

// networkBody serves the stored response body of one connection, so the UI can link to
// "the file with all the data" when its preview is cut. Never served as HTML (captured
// pages could carry scripts): JSON keeps the browser's JSON viewer, the rest is plain text.
func (s *Server) networkBody(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "conn_id")
	if !ok {
		return
	}
	c, err := s.Store.GetNetConn(id)
	if respondErr(w, err, "connection") {
		return
	}
	ext, ctype := bodyFileType(c)
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "sandbox")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="conexion_%d%s"`, c.ID, ext))
	_, _ = w.Write([]byte(c.ResponseBody))
}

// bodyFileType returns the file extension and safe content type for a stored body.
func bodyFileType(c *db.NetConn) (string, string) {
	if strings.Contains(strings.ToLower(c.MimeType), "json") || json.Valid([]byte(c.ResponseBody)) {
		return ".json", "application/json; charset=utf-8"
	}
	return ".txt", "text/plain; charset=utf-8"
}

// redactConn masks credentials and secrets of a connection before it is stored.
func redactConn(p *redact.Policy, c *db.NetConn) {
	c.URL = p.Text(c.URL)
	c.ErrorText = p.Text(c.ErrorText)
	c.RequestHeaders = p.Headers(c.RequestHeaders)
	c.ResponseHeaders = p.Headers(c.ResponseHeaders)
	c.PostData = p.Text(c.PostData)
	c.ResponseBody = p.Text(c.ResponseBody)
}

func normalizeConn(c *db.NetConn) {
	c.Method = strings.ToUpper(truncate(strings.TrimSpace(c.Method), 16))
	c.URL = truncate(c.URL, maxURLChars)
	c.StatusText = truncate(c.StatusText, 256)
	c.MimeType = truncate(c.MimeType, 256)
	c.ResourceType = truncate(c.ResourceType, 32)
	c.ErrorText = truncate(c.ErrorText, 2048)
	c.EvidenceFile = truncate(c.EvidenceFile, 1024)
	c.BodyFile = "" // only the ZIP export sets it
	c.PostData = truncate(c.PostData, min(maxPostDataChars, max(maxResponseBodyChars, 0)))
	c.RequestHeaders = truncateHeaders(c.RequestHeaders)
	c.ResponseHeaders = truncateHeaders(c.ResponseHeaders)
	if c.Status < 0 || c.Status > 999 {
		c.Status = 0
	}
	if c.DurationMs != nil && *c.DurationMs < 0 {
		c.DurationMs = nil
	}
	if size := int64(len(c.ResponseBody)); size > c.BodySize {
		c.BodySize = size
	}
	if len(c.ResponseBody) > maxResponseBodyChars {
		c.ResponseBody = truncate(c.ResponseBody, maxResponseBodyChars)
		c.BodyTruncated = true
	}
}

func truncateHeaders(h map[string]string) map[string]string {
	if len(h) > 200 {
		return map[string]string{"<truncated>": "more than 200 headers"}
	}
	for k, v := range h {
		h[k] = truncate(v, maxHeaderChars)
	}
	return h
}

// truncate cuts s to at most n bytes without splitting a UTF-8 rune.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
