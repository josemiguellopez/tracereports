package ai

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/locator"
)

// Un elemento que "no se encontró" mientras la página todavía cargaba no es un selector roto: el test no
// esperó lo suficiente. Antes de concluir LOCATOR_CHANGED se miran las señales de carga del momento del
// fallo (el DOM y la red) y si el mismo selector encontró el elemento la última vez que el test pasó.

// pendingWindowMs: how far back a request still in flight at the failure counts as part of the load.
const pendingWindowMs = 30_000

var (
	// clases o ids de indicadores de carga de las librerías comunes (Bootstrap, MUI, Ant, react-content-loader…)
	loaderTokenRe = regexp.MustCompile(`(?i)^(?:.*[-_])?(?:skeleton|spinner|loader|loading|shimmer|content-loader|placeholder-glow|placeholder-wave|progress-circular|spin)(?:[-_].*)?$`)
	// lo que dibuja un ícono o un gráfico; un skeleton son solo rectángulos
	svgDrawsRe = regexp.MustCompile(`(?i)<(?:path|circle|ellipse|polygon|polyline|line|text|use)\b`)
)

// loadingTexts: the facts in each language of the engine.
var loadingTexts = map[string]map[string]string{
	"es": {
		"skeleton": "%d bloques de carga (skeleton) visibles",
		"animated": "%d animaciones de carga visibles",
		"loader":   "un indicador de carga visible (%s)",
		"busy":     "una zona marcada como cargando (aria-busy) en %s",
		"pending":  "%d llamadas al backend seguían en curso al fallar (%s)",
		"before":   "el mismo selector encontró el elemento la última vez que el test pasó (ejecución #%d)",
	},
	"en": {
		"skeleton": "%d loading blocks (skeleton) on screen",
		"animated": "%d loading animations on screen",
		"loader":   "a loading indicator on screen (%s)",
		"busy":     "an area marked as loading (aria-busy) in %s",
		"pending":  "%d backend calls were still in flight at the failure (%s)",
		"before":   "the same selector found the element the last time the test passed (run #%d)",
	},
}

func ltext(lang, key string) string {
	if t, ok := loadingTexts[lang]; ok {
		return t[key]
	}
	return loadingTexts["es"][key]
}

// domLoading: what on the failure page says it was still loading (skeletons, spinners, aria-busy).
func domLoading(snap locator.Snapshot, lang string) []string {
	var facts []string
	nodes := snap.Nodes
	shown := func(n locator.Node) bool { return n.Visible && n.W > 0 && n.H > 0 }
	// skeleton: SVG de solo rectángulos, del tamaño de una fila, varios en el mismo contenedor
	skel := map[int]int{}
	anim, busy, loader := 0, "", ""
	for _, n := range nodes {
		if !shown(n) {
			continue
		}
		if n.Tag == "svg" {
			if n.Anim {
				anim++
			}
			if n.W >= 60 && n.H >= 12 && n.SVG != "" && strings.Contains(n.SVG, "<rect") && !svgDrawsRe.MatchString(n.SVG) {
				skel[n.P]++
			}
		}
		if busy == "" && strings.EqualFold(n.Attrs["aria-busy"], "true") {
			busy = nodeLabel(n)
		}
		if loader == "" && (n.Attrs["role"] == "progressbar" || n.Tag == "progress" || loaderToken(n.Attrs["class"]) != "" || loaderToken(n.Attrs["id"]) != "") {
			loader = nodeLabel(n)
		}
	}
	blocks := 0
	for _, c := range skel {
		if c >= 2 {
			blocks += c
		}
	}
	if blocks > 0 {
		facts = append(facts, fmt.Sprintf(ltext(lang, "skeleton"), blocks))
	} else if anim > 0 {
		facts = append(facts, fmt.Sprintf(ltext(lang, "animated"), anim))
	}
	if loader != "" {
		facts = append(facts, fmt.Sprintf(ltext(lang, "loader"), loader))
	}
	if busy != "" {
		facts = append(facts, fmt.Sprintf(ltext(lang, "busy"), busy))
	}
	return facts
}

func loaderToken(s string) string {
	for _, f := range strings.Fields(s) {
		if loaderTokenRe.MatchString(f) {
			return f
		}
	}
	return ""
}

// nodeLabel: tag#id.class (short), to name an element in a fact.
func nodeLabel(n locator.Node) string {
	s := n.Tag
	if id := n.Attrs["id"]; id != "" {
		s += "#" + id
	}
	if c := loaderToken(n.Attrs["class"]); c != "" {
		s += "." + c
	} else if cls := strings.Fields(n.Attrs["class"]); len(cls) > 0 {
		s += "." + cls[0]
	}
	return clip(s, 60)
}

// pendingCalls: backend calls started shortly before the failure that ended after it (the page was
// still waiting for them). Calls that failed are the backend rules' business, not this one's.
func pendingCalls(conns []db.NetConn, failAt int64, lang string) string {
	if failAt <= 0 {
		return ""
	}
	n, first := 0, ""
	for _, c := range conns {
		if c.Expected || c.Failed || c.Status == 0 || c.Status >= 400 || c.DurationMs == nil || c.StartedAt <= 0 {
			continue
		}
		if c.StartedAt <= failAt && c.StartedAt >= failAt-pendingWindowMs && c.StartedAt+*c.DurationMs > failAt {
			if n == 0 {
				host, path := db.NormalizeEndpoint(c.URL)
				first = c.Method + " " + host + path
			}
			n++
		}
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf(ltext(lang, "pending"), n, clip(first, 80))
}

// loadingSignals: the loading facts of a failed test, from its failure page and its network.
func loadingSignals(store *db.Store, t *db.Test, conns []db.NetConn, lang string) []string {
	var facts []string
	if raw, err := store.GetDOM(t.ID); err == nil && raw != "" {
		var snap locator.Snapshot
		if json.Unmarshal([]byte(raw), &snap) == nil {
			facts = domLoading(snap, lang)
		}
	}
	if p := pendingCalls(conns, failureTime(t), lang); p != "" {
		facts = append(facts, p)
	}
	return facts
}

// lastPassHistory: how many earlier runs are looked at for the last PASS with pages.
const lastPassHistory = 30

// selectorBefore: the same selector found the element in a page of the last PASS run of the test that
// kept its pages (TRACEREPORTS_DOM_STEPS). Only a sure answer counts ("" otherwise).
func selectorBefore(store *db.Store, t *db.Test, sel, lang string) string {
	if sel == "" || t.Key == "" || locator.Truncated(sel) {
		return ""
	}
	hist, err := store.TestHistory(t.Key, t.RunID, lastPassHistory)
	if err != nil {
		return ""
	}
	for _, h := range hist {
		if h.TestID == t.ID || h.RunID >= t.RunID || h.Status != "PASS" {
			continue
		}
		steps, err := store.ListDOMSteps(h.TestID)
		if err != nil || len(steps) == 0 {
			continue
		}
		for _, st := range steps {
			raw, err := store.GetDOMStep(h.TestID, st.Seq)
			if err != nil || raw == "" {
				continue
			}
			var snap locator.Snapshot
			if json.Unmarshal([]byte(raw), &snap) != nil {
				continue
			}
			if found, sure := locator.Present(sel, snap.Nodes); found && sure {
				return fmt.Sprintf(ltext(lang, "before"), h.RunID)
			}
		}
		return "" // el último PASS con páginas es el que dice
	}
	return ""
}
