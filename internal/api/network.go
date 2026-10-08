package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/josemiguellopez/tracereports/internal/correlate"
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
	// pocos lotes grandes a la vez: cada uno puede traer hasta maxNetworkBody
	select {
	case networkSlots <- struct{}{}:
		defer func() { <-networkSlots }()
	case <-r.Context().Done():
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxNetworkBody)
	// se lee de a una conexión y cada una se recorta al leerla: el lote no queda entero en
	// memoria con sus bodies completos (el límite del request sigue igual: los clientes envían
	// hasta 200 conexiones de hasta 256 KB)
	var conns []db.NetConn
	errors := 0
	red := s.redactor()
	err := decodeConnections(r.Body, func(c db.NetConn) error {
		if len(conns) >= maxNetworkConns {
			return errTooManyConns
		}
		// primero se enmascara y después se recorta (ver redactThenCut); antes, un recorte holgado
		// acota el trabajo de la redacción sin acercarse al corte final
		if size := int64(len(c.ResponseBody)); size > c.BodySize {
			c.BodySize = size
		}
		if len(c.ResponseBody) > maxResponseBodyChars+redactSlack {
			c.ResponseBody = strings.Clone(truncate(c.ResponseBody, maxResponseBodyChars+redactSlack))
			// lo descartado no vuelve, aunque la redacción achique después lo que queda
			c.BodyTruncated = true
		}
		if len(c.PostData) > maxPostDataChars+redactSlack {
			c.PostData = strings.Clone(truncate(c.PostData, maxPostDataChars+redactSlack))
		}
		redactConn(red, &c)
		normalizeConn(&c)
		if (c.Failed || c.Status >= 400) && !c.Expected {
			errors++
		}
		conns = append(conns, c)
		return nil
	})
	if err == errTooManyConns {
		writeError(w, http.StatusRequestEntityTooLarge, "too many connections in one batch (max 5000)")
		return
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	res := map[string]int{"stored": len(conns), "errors": errors}
	err = s.commit(w, r, func(tx *db.Store) (int, any, error) {
		return http.StatusCreated, res, tx.AddNetwork(id, conns)
	})
	if respondErr(w, err, "test") {
		return
	}
	s.publishTest("network", id, res)
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
	s.linkCalls(conns)
	writeJSON(w, http.StatusOK, conns)
}

// networkBaseline returns the same call in the last run where the test passed (204 if none):
// the UI compares status, headers and body between "it worked" and "it failed".
func (s *Server) networkBaseline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "conn_id")
	if !ok {
		return
	}
	b, err := s.Store.BaselineFor(id)
	if respondErr(w, err, "network call") {
		return
	}
	if b == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	one := []db.NetConn{b.Conn}
	s.linkCalls(one)
	b.Conn = one[0]
	writeJSON(w, http.StatusOK, b)
}

// linkCalls fills the links to the backend logs and trace of each call (when configured and
// the call has what the template needs).
func (s *Server) linkCalls(conns []db.NetConn) {
	if s.LogsURL == "" && s.TraceURL == "" {
		return
	}
	for i := range conns {
		c := callOf(&conns[i])
		conns[i].LogsURL, conns[i].TraceURL = correlate.Link(s.LogsURL, c), correlate.Link(s.TraceURL, c)
	}
}

func callOf(c *db.NetConn) correlate.Call {
	call := correlate.Call{IDs: correlate.IDs{TraceID: c.TraceID, RequestID: c.RequestID}, Method: c.Method, URL: c.URL,
		Status: c.Status, StartedAt: c.StartedAt}
	if c.DurationMs != nil {
		call.Duration = *c.DurationMs
	}
	return call
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

var (
	networkSlots    = make(chan struct{}, 4) // lotes de red procesándose a la vez
	errTooManyConns = errors.New("too many connections")
)

// decodeConnections reads {"connections": [...]} calling each for every connection as soon as it
// is decoded. Other keys are skipped; an empty body has no connections.
func decodeConnections(r io.Reader, each func(db.NetConn) error) error {
	dec := json.NewDecoder(r)
	tok, err := dec.Token()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return errors.New(`expected an object {"connections": [...]}`)
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		if key, _ := tok.(string); key != "connections" {
			var skip json.RawMessage
			if err := dec.Decode(&skip); err != nil {
				return err
			}
			continue
		}
		tok, err = dec.Token()
		if err != nil {
			return err
		}
		if tok == nil {
			continue // "connections": null
		}
		if d, ok := tok.(json.Delim); !ok || d != '[' {
			return errors.New(`"connections" must be an array`)
		}
		for dec.More() {
			var c db.NetConn
			if err := dec.Decode(&c); err != nil {
				return err
			}
			if err := each(c); err != nil {
				return err
			}
		}
		if _, err := dec.Token(); err != nil { // ]
			return err
		}
	}
	_, err = dec.Token() // }
	return err
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
	if limit := min(maxPostDataChars, max(maxResponseBodyChars, 0)); len(c.PostData) > limit {
		c.PostData = strings.Clone(truncate(c.PostData, limit)) // copia: no retiene el original entero
	}
	c.RequestHeaders = truncateHeaders(c.RequestHeaders)
	c.ResponseHeaders = truncateHeaders(c.ResponseHeaders)
	if c.Status < 0 || c.Status > 999 {
		c.Status = 0
	}
	if c.DurationMs != nil && *c.DurationMs < 0 {
		c.DurationMs = nil
	}
	// BodySize ya es el tamaño original (addNetwork lo toma antes de enmascarar: el texto
	// enmascarado puede ser más largo o más corto)
	if len(c.ResponseBody) > maxResponseBodyChars {
		c.ResponseBody = strings.Clone(truncate(c.ResponseBody, maxResponseBodyChars)) // copia: libera el body completo
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

// redactThenCut masks s and then cuts it to n bytes. Never the other way around: the cut could
// leave a secret without the closing quote its pattern needs. The text is first bounded to n plus
// redactSlack (the work stays bounded): a secret cut there is masked by redact (value cut at the
// end) and lies past the final cut anyway.
func redactThenCut(p *redact.Policy, s string, n int) string {
	return truncate(p.Text(truncate(s, n+redactSlack)), n)
}

// redactSlack is the margin kept past a limit while redacting, before the final cut.
const redactSlack = 64 << 10

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
