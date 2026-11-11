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

// svgElems: the shapes an icon is made of. svgAttrs: geometry and paint, nothing that loads or runs.
var svgElems = map[string]bool{"svg": true, "g": true, "path": true, "circle": true, "ellipse": true, "rect": true, "line": true,
	"polyline": true, "polygon": true, "title": false}
var svgAttrs = map[string]bool{"viewBox": true, "width": true, "height": true, "d": true, "cx": true, "cy": true, "r": true, "rx": true, "ry": true,
	"x": true, "y": true, "x1": true, "y1": true, "x2": true, "y2": true, "points": true, "transform": true, "fill": true, "stroke": true,
	"stroke-width": true, "opacity": true, "fill-opacity": true, "stroke-opacity": true, "fill-rule": true, "clip-rule": true,
	"stroke-linecap": true, "stroke-linejoin": true, "stroke-miterlimit": true, "preserveAspectRatio": true}
var svgValueRe = regexp.MustCompile(`^[a-zA-Z0-9 #%.,()+\-]*$`)

// cleanSVG rebuilds an inline <svg> from the whitelist above. Unknown elements are dropped with their
// content; a document that does not parse, or one that is not a single <svg>, gives "".
func cleanSVG(s string) string {
	if len(s) > maxSVGBytes {
		return ""
	}
	dec := xml.NewDecoder(strings.NewReader(s))
	dec.Strict = true
	var b strings.Builder
	depth, skip, roots, shapes := 0, 0, 0, 0
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		switch t := tok.(type) {
		case xml.StartElement:
			depth++
			if skip > 0 {
				skip++
				continue
			}
			name := t.Name.Local
			if !svgElems[name] || (depth == 1) != (name == "svg") {
				skip = 1
				continue
			}
			if depth == 1 {
				roots++
			}
			if name != "svg" && name != "g" {
				shapes++
			}
			b.WriteString("<" + name)
			if name == "svg" {
				b.WriteString(` xmlns="http://www.w3.org/2000/svg"`)
			}
			for _, a := range t.Attr {
				v := strings.TrimSpace(a.Value)
				if a.Name.Space != "" || !svgAttrs[a.Name.Local] || len(v) > 4000 || !svgValueRe.MatchString(v) || strings.Contains(strings.ToLower(v), "url(") {
					continue
				}
				b.WriteString(" " + a.Name.Local + `="` + v + `"`)
			}
			b.WriteString(">")
		case xml.EndElement:
			depth--
			if skip > 0 {
				skip--
				continue
			}
			b.WriteString("</" + t.Name.Local + ">")
		}
	}
	if roots != 1 || shapes == 0 || depth != 0 {
		return ""
	}
	return b.String()
}
