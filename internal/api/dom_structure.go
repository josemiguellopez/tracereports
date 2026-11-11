package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/josemiguellopez/tracereports/internal/locator"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

// Snapshot estructural (Inspector): la página entera como árbol, para redibujarla y evaluar
// selectores sobre ella sin reproducir el fallo. Lo que llega es dato de un cliente cualquiera: se
// limita, se valida el árbol y solo pasan los atributos y estilos de una lista blanca, con los
// secretos enmascarados como en el resto de la evidencia.

const (
	maxDOMNodes   = 6000 // nodos de un snapshot estructural
	maxNodeText   = 300
	maxAttrValue  = 200
	maxStyleValue = 120
)

// nodeAttrs: los atributos que sirven para armar selectores y entender la página. Nunca value
// (lo que alguien escribió), on* (scripts) ni src/srcset (recursos externos).
var nodeAttrs = map[string]bool{"id": true, "class": true, "name": true, "role": true, "type": true, "placeholder": true,
	"href": true, "title": true, "for": true, "alt": true, "data-testid": true, "data-test": true, "data-test-id": true,
	"data-tracereports-mask": true}

// nodeStyles: lo justo para dibujar la caja. Sin background-image ni nada que cargue una URL.
var nodeStyles = map[string]bool{"display": true, "position": true, "backgroundColor": true, "color": true, "fontSize": true,
	"fontWeight": true, "fontFamily": true, "borderTopWidth": true, "borderTopColor": true, "borderRadius": true,
	"opacity": true, "textAlign": true, "lineHeight": true,
	// el orden de las capas (un modal encima de la página) y el recorte de los paneles con scroll
	"zIndex": true, "overflowX": true, "overflowY": true}

var (
	tagRe        = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)
	ariaAttrRe   = regexp.MustCompile(`^aria-[a-z]{1,30}$`)
	styleValueRe = regexp.MustCompile(`^[a-zA-Z0-9 #%.,()'"_-]*$`)
)

// cleanNodes validates and redacts a structural snapshot in place. A tree that does not hold (a
// parent after its child, an index out of order) is dropped whole: the Inspector could not draw it.
func cleanNodes(p *redact.Policy, snap *locator.Snapshot) {
	if len(snap.Nodes) > maxDOMNodes {
		snap.Nodes = snap.Nodes[:maxDOMNodes]
	}
	snap.Glyphs = cleanGlyphs(snap.Glyphs)
	for i := range snap.Nodes {
		n := &snap.Nodes[i]
		if n.I != i || n.P >= i || n.P < -1 || !tagRe.MatchString(n.Tag) {
			snap.Nodes = nil
			return
		}
		n.Text = personal(redactThenCut(p, n.Text, maxNodeText))
		attrs := map[string]string{}
		for k, v := range n.Attrs {
			if nodeAttrs[k] || ariaAttrRe.MatchString(k) {
				if k == "href" && !strings.HasPrefix(v, "/") && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") && !strings.HasPrefix(v, "#") {
					continue // javascript:, data: y similares: no son un dato de la página
				}
				v = redactThenCut(p, v, maxAttrValue)
				if !locatorAttrs[k] {
					v = personal(v) // title, alt, aria-* (salvo aria-label), href: texto de la página
				}
				attrs[k] = v
			}
		}
		n.Attrs = attrs
		style := map[string]string{}
		for k, v := range n.Style {
			if nodeStyles[k] && len(v) <= maxStyleValue && styleValueRe.MatchString(v) && !strings.Contains(strings.ToLower(v), "url(") {
				style[k] = v
			}
		}
		n.Style = style
		n.Icons = cleanIcons(n.Icons, snap.Glyphs)
		if n.SVG != "" {
			n.SVG = cleanSVG(n.SVG)
		}
		for _, c := range []**int{&n.TX, &n.TY} {
			if *c != nil && (**c < 0 || **c > 100000) {
				*c = nil
			}
		}
	}
	if snap.Page != nil && (snap.Page.W <= 0 || snap.Page.H <= 0 || snap.Page.W > 100000 || snap.Page.H > 200000) {
		snap.Page = nil
	}
	snap.Label = redactThenCut(p, snap.Label, 300)
}

// getDOM returns the snapshot of the moment of the failure (GET /tests/{id}/dom; 204 without one).
func (s *Server) getDOM(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	raw, err := s.Store.GetDOM(id)
	if err != nil {
		serverError(w, err)
		return
	}
	if raw == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write([]byte(raw))
}

// domSteps lists the snapshots of the pages a test went through (GET /tests/{id}/dom/steps).
func (s *Server) domSteps(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	steps, err := s.Store.ListDOMSteps(id)
	if err != nil {
		serverError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, steps)
}

// domStep returns one of them (GET /tests/{id}/dom/steps/{seq}).
func (s *Server) domStep(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	seq, err := strconv.Atoi(chi.URLParam(r, "seq"))
	if err != nil || seq < 1 {
		writeError(w, http.StatusBadRequest, "seq must be a positive number")
		return
	}
	raw, err := s.Store.GetDOMStep(id, seq)
	if respondErr(w, err, "snapshot") {
		return
	}
	var check json.RawMessage = []byte(raw)
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(check)
}

// domBaseline is the page to compare with (GET /tests/{id}/dom/baseline?label=…): the same test in its
// last PASS run of the same context that kept its pages (TRACEREPORTS_DOM_STEPS), the page with the
// same step label, or its last one. 204 when there is none. The Inspector shows what was added, removed
// or changed since then: the most direct way to see a locator drift.
func (s *Server) domBaseline(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	t, err := s.Store.GetTest(id)
	if respondErr(w, err, "test") {
		return
	}
	hist, err := s.Store.TestHistory(t.Key, t.RunID, baselineHistory)
	if err != nil {
		serverError(w, err)
		return
	}
	label := r.URL.Query().Get("label")
	for _, h := range hist {
		if h.TestID == id || h.RunID >= t.RunID || h.Status != "PASS" {
			continue
		}
		steps, err := s.Store.ListDOMSteps(h.TestID)
		if err != nil {
			serverError(w, err)
			return
		}
		if len(steps) == 0 {
			continue
		}
		pick := steps[len(steps)-1]
		for _, st := range steps {
			if label != "" && st.Label == label {
				pick = st
			}
		}
		raw, err := s.Store.GetDOMStep(h.TestID, pick.Seq)
		if err != nil {
			serverError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"test_id": h.TestID, "run_id": h.RunID, "run_name": h.RunName, "started_at": h.StartedAt,
			"seq": pick.Seq, "label": pick.Label, "same_step": label != "" && pick.Label == label, "snapshot": json.RawMessage(raw)})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// baselineHistory: how many earlier runs of the test are looked at for a PASS with pages.
const baselineHistory = 30

// locatorAttrs identify an element for a locator: the personal-data mask leaves them as they are.
// placeholder and aria-label too: they are fixed text of the interface (getByPlaceholder, getByLabel),
// never what someone typed.
var locatorAttrs = map[string]bool{"id": true, "class": true, "name": true, "role": true, "type": true, "for": true,
	"data-testid": true, "data-test": true, "data-test-id": true, "data-tracereports-mask": true,
	"placeholder": true, "aria-label": true}

// personal masks RUTs, phone numbers and e-mails in what a page shows (TRACEREPORTS_REDACT_PII=off
// turns it off). Only the DOM snapshot: steps and errors keep the secrets-only redaction.
func personal(s string) string {
	if !redact.PersonalDataEnabled() {
		return s
	}
	return redact.PersonalData(s)
}
