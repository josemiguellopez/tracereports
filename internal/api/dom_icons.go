package api

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"regexp"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/locator"
)

// Icons in the structural snapshot (the Inspector draws the page as it looked, icons included). They
// come from any client, so they are data, never markup that runs: glyphs are only PNG data URLs within
// a size budget, and an inline <svg> is rebuilt from a whitelist of shapes and paint attributes (no
// scripts, links, foreignObject, styles or url() references). The viewer still shows it as an <img>.

const (
	maxGlyphs      = 300
	maxGlyphBytes  = 24000
	maxGlyphsTotal = 2 << 20
	maxNodeIcons   = 4
	maxIconSide    = 256
	maxSVGBytes    = 12000
)

var (
	glyphIDRe = regexp.MustCompile(`^g\d{1,4}$`)
	b64Re     = regexp.MustCompile(`^[A-Za-z0-9+/]+=*$`)
	iconPos   = map[string]bool{"before": true, "after": true, "text": true, "img": true}
)

const pngPrefix = "data:image/png;base64,"

// cleanGlyphs keeps the glyphs that are real PNG data URLs (by their signature, not only the prefix),
// within the per-glyph and total budgets. The rest is dropped: the node keeps its box.
func cleanGlyphs(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := map[string]string{}
	total := 0
	for id, v := range in {
		if len(out) >= maxGlyphs || !glyphIDRe.MatchString(id) || len(v) > maxGlyphBytes || !strings.HasPrefix(v, pngPrefix) {
			continue
		}
		data := v[len(pngPrefix):]
		if !b64Re.MatchString(data) {
			continue
		}
		head, err := base64.StdEncoding.DecodeString(data[:min(len(data), 12)])
		if err != nil || len(head) < 8 || string(head[:8]) != "\x89PNG\r\n\x1a\n" {
			continue
		}
		if total+len(v) > maxGlyphsTotal {
			break
		}
		total += len(v)
		out[id] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// cleanIcons keeps up to 4 icons per node that point to a kept glyph, with a known position and a sane size.
func cleanIcons(in []locator.NodeIcon, glyphs map[string]string) []locator.NodeIcon {
	var out []locator.NodeIcon
	for _, ic := range in {
		if len(out) >= maxNodeIcons {
			break
		}
		if _, ok := glyphs[ic.G]; !ok || !iconPos[ic.Pos] || ic.W < 1 || ic.H < 1 || ic.W > maxIconSide || ic.H > maxIconSide {
			continue
		}
		out = append(out, ic)
	}
	return out
}

// svgElems: what an icon, a chart or a loading skeleton is made of: shapes, groups, and the paint
// servers and clips they point to with url(#id) inside the same <svg> (gradients, clipPath, mask,
// pattern, symbol + use). Animations are dropped (a snapshot is one moment) and so is everything
// that runs or loads: script, foreignObject, image, style, a/links. svgShapes: what draws something.
var svgElems = map[string]bool{"svg": true, "g": true, "path": true, "circle": true, "ellipse": true, "rect": true, "line": true,
	"polyline": true, "polygon": true, "defs": true, "clipPath": true, "mask": true, "linearGradient": true,
	"radialGradient": true, "stop": true, "pattern": true, "symbol": true, "use": true}
var svgShapes = map[string]bool{"path": true, "circle": true, "ellipse": true, "rect": true, "line": true, "polyline": true,
	"polygon": true, "use": true}
var svgAttrs = map[string]bool{"viewBox": true, "width": true, "height": true, "d": true, "cx": true, "cy": true, "r": true, "rx": true, "ry": true,
	"x": true, "y": true, "x1": true, "y1": true, "x2": true, "y2": true, "points": true, "transform": true, "fill": true, "stroke": true,
	"stroke-width": true, "opacity": true, "fill-opacity": true, "stroke-opacity": true, "fill-rule": true, "clip-rule": true,
	"stroke-linecap": true, "stroke-linejoin": true, "stroke-miterlimit": true, "preserveAspectRatio": true,
	// referencias internas y lo que definen los gradientes, recortes, máscaras y patrones
	"id": true, "clip-path": true, "mask": true, "href": true, "offset": true, "stop-color": true, "stop-opacity": true,
	"gradientUnits": true, "gradientTransform": true, "spreadMethod": true, "fx": true, "fy": true, "fr": true,
	"clipPathUnits": true, "maskUnits": true, "maskContentUnits": true, "patternUnits": true,
	"patternContentUnits": true, "patternTransform": true, "visibility": true, "display": true}
var (
	svgValueRe = regexp.MustCompile(`^[a-zA-Z0-9 #%.,()+\-]*$`)
	svgIDRe    = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.\-]{0,80}$`)
	// url(#id), url("#id"), url('#id') and the absolute form some libraries build so a <base href>
	// does not break them (url("https://host/page#id")): only the fragment counts, and only when it
	// names an element of this same <svg>; nothing is ever loaded.
	svgURLRe = regexp.MustCompile(`^url\(\s*["']?[^"'()\s#]*#([A-Za-z_][A-Za-z0-9_.\-]{0,80})["']?\s*\)$`)
)

const maxSVGUses = 64 // <use> per <svg>: each one draws a copy, never a copy of a copy

type svgNode struct {
	name  string
	attrs []xml.Attr
	kids  []*svgNode
}

// cleanSVG rebuilds an inline <svg> from the whitelist above. Unknown elements are dropped with their
// content; a document that does not parse, or one that is not a single <svg>, gives "". A url(#id)
// or href="#id" stays only when the id is an element of the same <svg>; a fill or stroke whose
// reference is dropped becomes "none", never the inherited color (black, by default).
func cleanSVG(s string) string {
	if len(s) > maxSVGBytes {
		return ""
	}
	root, ok := parseSVG(s)
	if !ok {
		return ""
	}
	ids := map[string]*svgNode{}
	shapes, uses := 0, 0
	var index func(n *svgNode)
	index = func(n *svgNode) {
		if svgShapes[n.name] {
			shapes++
		}
		if n.name == "use" {
			uses++
		}
		for _, a := range n.attrs {
			if a.Name.Local == "id" && svgIDRe.MatchString(a.Value) {
				if _, dup := ids[a.Value]; !dup {
					ids[a.Value] = n
				}
			}
		}
		for _, k := range n.kids {
			index(k)
		}
	}
	index(root)
	if shapes == 0 || uses > maxSVGUses {
		return ""
	}
	var b strings.Builder
	writeSVG(&b, root, ids)
	return b.String()
}

// parseSVG reads the whitelisted tree. Strict XML: no DTD, entities or a second root.
func parseSVG(s string) (*svgNode, bool) {
	dec := xml.NewDecoder(strings.NewReader(s))
	dec.Strict = true
	var root *svgNode
	var stack []*svgNode
	skip := 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if skip > 0 {
				skip++
				continue
			}
			name := t.Name.Local
			if t.Name.Space != "" && t.Name.Space != "http://www.w3.org/2000/svg" {
				skip = 1
				continue
			}
			if !svgElems[name] || (len(stack) == 0) != (name == "svg") {
				if len(stack) == 0 {
					return nil, false // the root is not an <svg>
				}
				skip = 1
				continue
			}
			n := &svgNode{name: name, attrs: t.Attr}
			if len(stack) == 0 {
				if root != nil {
					return nil, false // two roots
				}
				root = n
			} else {
				p := stack[len(stack)-1]
				p.kids = append(p.kids, n)
			}
			stack = append(stack, n)
		case xml.EndElement:
			if skip > 0 {
				skip--
				continue
			}
			stack = stack[:len(stack)-1]
		case xml.Directive:
			return nil, false
		}
	}
	return root, root != nil && len(stack) == 0
}

// hasUse: a <use> may draw an element that has no <use> inside (no copies of copies, no loops).
func hasUse(n *svgNode) bool {
	if n.name == "use" {
		return true
	}
	for _, k := range n.kids {
		if hasUse(k) {
			return true
		}
	}
	return false
}

func writeSVG(b *strings.Builder, n *svgNode, ids map[string]*svgNode) {
	if n.name == "use" {
		if t := useTarget(n, ids); t == nil || hasUse(t) {
			return
		}
	}
	b.WriteString("<" + n.name)
	if n.name == "svg" {
		b.WriteString(` xmlns="http://www.w3.org/2000/svg"`)
	}
	seen := map[string]bool{}
	for _, a := range n.attrs {
		k, v := a.Name.Local, strings.TrimSpace(a.Value)
		if a.Name.Space != "" && !(k == "href" && a.Name.Space == "http://www.w3.org/1999/xlink") {
			continue
		}
		if !svgAttrs[k] || seen[k] || len(v) > 4000 {
			continue
		}
		switch {
		case k == "href":
			if !strings.HasPrefix(v, "#") || ids[v[1:]] == nil {
				continue
			}
		case k == "id":
			if !svgIDRe.MatchString(v) {
				continue
			}
		case strings.Contains(strings.ToLower(v), "url("):
			m := svgURLRe.FindStringSubmatch(v)
			switch {
			case m != nil && ids[m[1]] != nil:
				v = "url(#" + m[1] + ")"
			case k == "fill" || k == "stroke":
				v = "none" // un gradiente o patrón que no viene: un hueco, no un bloque negro
			default:
				continue
			}
		case !svgValueRe.MatchString(v):
			if k == "fill" || k == "stroke" {
				v = "none"
			} else {
				continue
			}
		}
		seen[k] = true
		b.WriteString(" " + k + `="` + v + `"`)
	}
	b.WriteString(">")
	for _, k := range n.kids {
		writeSVG(b, k, ids)
	}
	b.WriteString("</" + n.name + ">")
}

func useTarget(n *svgNode, ids map[string]*svgNode) *svgNode {
	for _, a := range n.attrs {
		if a.Name.Local == "href" && (a.Name.Space == "" || a.Name.Space == "http://www.w3.org/1999/xlink") {
			if v := strings.TrimSpace(a.Value); strings.HasPrefix(v, "#") {
				return ids[v[1:]]
			}
		}
	}
	return nil
}
