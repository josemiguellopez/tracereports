package api

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"regexp"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/josemiguellopez/tracereports/internal/locator"
	"github.com/josemiguellopez/tracereports/internal/redact"
	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Modo HTML del Inspector (TRACEREPORTS_DOM_MODE=html): el cliente manda el HTML de la página y sus
// hojas de estilo, y el visor deja que el navegador la dibuje en un iframe aislado (sandbox sin
// scripts, CSP sin red). Es HTML de otra aplicación: al recibirlo se rehace sin nada que ejecute o
// cargue algo, sin lo que alguien escribió y con los datos personales enmascarados como el resto del
// snapshot. El CSS se guarda una vez por hash y sin @import ni url() externas.

const (
	maxHTMLBytes     = 3 << 20   // el HTML de una página
	maxSheetBytes    = 2 << 20   // una hoja de estilo
	maxSheetsBytes   = 6 << 20   // las hojas nuevas de un snapshot
	maxHTMLElements  = 30000     // elementos de una página
	maxSheetRefs     = 300       // hojas que usa una página
	maxCSSDataURL    = 256 << 10 // una imagen o fuente embebida en el CSS
	maxMissingSheets = 50
	maxHTMLDepth     = 1000
)

var (
	sheetHashRe = regexp.MustCompile(`^[0-9a-f]{64}$`)
	cssImportRe = regexp.MustCompile(`(?i)@import[^;]*;?`)
	cssURLRe    = regexp.MustCompile(`(?i)url\(\s*("[^"]*"|'[^']*'|[^)]*)\s*\)`)
	cssDataOK   = regexp.MustCompile(`(?i)^data:(image/(png|gif|jpeg|webp|svg\+xml)|font/[a-z0-9.+-]+|application/(x-)?font-[a-z0-9.+-]+|application/vnd\.ms-fontobject)[;,]`)
	cssFragRe   = regexp.MustCompile(`^#[A-Za-z_][A-Za-z0-9_.:-]{0,80}$`)
	cssEvilRe   = regexp.MustCompile(`(?i)expression\s*\(|-moz-binding|behavior\s*:|javascript:`)
	imgDataOK   = regexp.MustCompile(`^data:image/(png|gif|jpeg|webp);base64,[A-Za-z0-9+/]+=*$`)
)

// htmlDrop: lo que se quita con todo su contenido: lo que ejecuta, carga otro documento o cambia
// cómo se interpreta la página. El <head> se vacía entero (sus hojas viajan aparte).
var htmlDrop = map[atom.Atom]bool{atom.Script: true, atom.Noscript: true, atom.Iframe: true, atom.Frame: true,
	atom.Frameset: true, atom.Object: true, atom.Embed: true, atom.Applet: true, atom.Base: true, atom.Meta: true,
	atom.Link: true, atom.Math: true, atom.Title: true}

// svgDrop: dentro de un <svg>, lo que ejecuta, mete HTML, anima o carga una imagen.
var svgDrop = map[string]bool{"script": true, "foreignobject": true, "animate": true, "set": true, "animatetransform": true,
	"animatemotion": true, "discard": true, "handler": true, "listener": true, "iframe": true, "audio": true, "video": true}

// htmlAttrDrop: atributos que mandan a otra parte o hacen algo, además de los on*.
var htmlAttrDrop = map[string]bool{"srcdoc": true, "formaction": true, "action": true, "ping": true, "background": true,
	"lowsrc": true, "dynsrc": true, "codebase": true, "http-equiv": true, "nonce": true, "integrity": true, "srcset": true,
	"poster": true, "manifest": true, "longdesc": true, "cite": true, "data": true, "archive": true, "classid": true,
	"autofocus": true, "contenteditable": true, "popovertarget": true, "itemref": true, "attributionsrc": true}

// typedInputs: los input donde alguien escribe o elige algo propio; su valor no viaja (se ve •••).
var keepValueInputs = map[string]bool{"button": true, "submit": true, "reset": true, "checkbox": true, "radio": true,
	"image": true, "color": true, "range": true}

// cleanHTMLDoc rehace el HTML de la página con la lista de arriba. Un documento que no se puede leer o
// que es demasiado grande da "" (el Inspector dibuja la estructura).
func cleanHTMLDoc(p *redact.Policy, doc string) string {
	if doc == "" || len(doc) > maxHTMLBytes || !htmlShapeOK(doc) {
		return ""
	}
	root, err := html.Parse(strings.NewReader(doc))
	if err != nil {
		return ""
	}
	elements := 0
	var walk func(n *html.Node, inSVG bool) bool
	walk = func(n *html.Node, inSVG bool) bool {
		for c := n.FirstChild; c != nil; {
			next := c.NextSibling
			switch c.Type {
			case html.CommentNode:
				n.RemoveChild(c)
			case html.TextNode:
				if n.Type == html.ElementNode && n.DataAtom == atom.Style {
					c.Data = cleanCSS(c.Data)
				} else if n.Type == html.ElementNode && n.DataAtom == atom.Textarea {
					if strings.TrimSpace(c.Data) != "" {
						c.Data = "•••" // lo escrito no viaja
					}
				} else {
					c.Data = personal(redactThenCut(p, c.Data, maxHTMLBytes))
				}
			case html.ElementNode:
				elements++
				if elements > maxHTMLElements {
					return false
				}
				svg := inSVG || c.Namespace == "svg"
				name := strings.ToLower(c.Data)
				if (c.Namespace == "" && htmlDrop[c.DataAtom]) || c.Namespace == "math" || (svg && svgDrop[name]) ||
					(c.DataAtom == atom.Template && !shadowTemplate(c)) || (svg && name == "image" && !svgImageOK(c)) {
					n.RemoveChild(c)
					break
				}
				if c.DataAtom == atom.Head && c.Namespace == "" {
					for k := c.FirstChild; k != nil; k = c.FirstChild {
						c.RemoveChild(k)
					}
					c.Attr = nil
					break
				}
				c.Attr = cleanHTMLAttrs(p, c, svg)
				if !walk(c, svg) {
					return false
				}
			}
			c = next
		}
		return true
	}
	if !walk(root, false) {
		return ""
	}
	var b strings.Builder
	if err := html.Render(&b, root); err != nil {
		return ""
	}
	return b.String()
}

// htmlShapeOK: una pasada lineal antes de armar el árbol: un documento con demasiados elementos o
// anidado sin fin (el parser se vuelve lento) no se usa. El cliente serializa con las etiquetas cerradas.
func htmlShapeOK(doc string) bool {
	z := html.NewTokenizer(strings.NewReader(doc))
	depth, count := 0, 0
	for {
		switch z.Next() {
		case html.ErrorToken:
			return true
		case html.StartTagToken:
			count++
			name, _ := z.TagName()
			if !htmlVoid[string(name)] {
				depth++
			}
			if count > maxHTMLElements || depth > maxHTMLDepth {
				return false
			}
		case html.EndTagToken:
			if depth > 0 {
				depth--
			}
		}
	}
}

var htmlVoid = map[string]bool{"area": true, "base": true, "br": true, "col": true, "embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "source": true, "track": true, "wbr": true, "param": true}

// shadowTemplate: un <template shadowrootmode="open">, el shadow DOM abierto de la página.
func shadowTemplate(n *html.Node) bool {
	for _, a := range n.Attr {
		if a.Key == "shadowrootmode" && a.Val == "open" {
			return true
		}
	}
	return false
}

func svgImageOK(n *html.Node) bool {
	for _, a := range n.Attr {
		if a.Key == "href" && imgDataOK.MatchString(strings.TrimSpace(a.Val)) {
			return true
		}
	}
	return false
}

func cleanHTMLAttrs(p *redact.Policy, n *html.Node, svg bool) []html.Attribute {
	inputType := ""
	if n.DataAtom == atom.Input {
		for _, a := range n.Attr {
			if strings.EqualFold(a.Key, "type") {
				inputType = strings.ToLower(strings.TrimSpace(a.Val))
			}
		}
	}
	out := n.Attr[:0]
	for _, a := range n.Attr {
		k := strings.ToLower(a.Key) // para comparar; el nombre queda como viene (viewBox en un SVG)
		v := a.Val
		if a.Namespace == "xlink" && k == "href" {
			a.Namespace, a.Key = "", "href"
		}
		if a.Namespace != "" || strings.HasPrefix(k, "on") || htmlAttrDrop[k] || len(v) > 100000 {
			continue
		}
		lv := strings.ToLower(strings.TrimSpace(v))
		switch {
		case k == "style":
			v = cleanCSS(v)
		case k == "href":
			switch {
			case svg && !strings.HasPrefix(lv, "#") && !(n.Data == "image" && imgDataOK.MatchString(strings.TrimSpace(v))):
				continue // un <use> o <image> solo apunta adentro del mismo documento
			case !svg && !(strings.HasPrefix(lv, "#") || strings.HasPrefix(lv, "/") || strings.HasPrefix(lv, "http://") || strings.HasPrefix(lv, "https://")):
				continue // javascript:, data: y similares
			}
			v = personal(redactThenCut(p, v, maxURLChars))
		case k == "src":
			if !imgDataOK.MatchString(strings.TrimSpace(v)) || len(v) > maxGlyphBytes*8 {
				continue // la aplicación pide login: el visor dibuja el lugar de la imagen
			}
		case k == "value" && n.DataAtom == atom.Input:
			if inputType == "hidden" || inputType == "password" {
				continue
			}
			if !keepValueInputs[inputType] && v != "" {
				v = "•••"
			}
			v = redactThenCut(p, v, maxAttrValue)
		case strings.HasPrefix(k, "data-tr-") || k == "class" || k == "id" || svg:
			v = redactThenCut(p, v, 100000)
		default:
			v = redactThenCut(p, v, 2000)
			if !locatorAttrs[k] {
				v = personal(v)
			}
		}
		a.Val = v
		out = append(out, a)
	}
	return out
}

// cleanCSS deja una hoja (o un style="…") sin nada que cargue algo de afuera: sin @import, con url()
// solo de imágenes y fuentes embebidas (data:) de tamaño razonable, sin expression() ni bindings, y
// con "<" escapado para que nunca cierre el <style> donde se pone.
func cleanCSS(s string) string {
	s = cssImportRe.ReplaceAllString(s, "")
	s = cssURLRe.ReplaceAllStringFunc(s, func(m string) string {
		v := strings.TrimSpace(cssURLRe.FindStringSubmatch(m)[1])
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') {
			v = v[1 : len(v)-1]
		}
		if len(v) <= maxCSSDataURL && cssDataOK.MatchString(v) && !strings.ContainsAny(v, "\"\\\n") {
			return `url("` + v + `")`
		}
		if cssFragRe.MatchString(v) { // un gradiente, recorte o máscara del mismo documento: no carga nada
			return "url(" + v + ")"
		}
		return "none"
	})
	s = cssEvilRe.ReplaceAllString(s, "x")
	return strings.ReplaceAll(s, "<", `\3c `)
}

// cleanHTMLSnapshot limpia la parte HTML de un snapshot y devuelve las hojas nuevas (hash → CSS limpio)
// que hay que guardar aparte. Sin un HTML usable el snapshot queda en el modo estructura.
func cleanHTMLSnapshot(p *redact.Policy, snap *locator.Snapshot) map[string]string {
	h := snap.HTML
	snap.Mode = ""
	if h == nil {
		return nil
	}
	sheets := map[string]string{}
	total := 0
	for hash, css := range h.Sheets {
		sum := sha256.Sum256([]byte(css))
		if !sheetHashRe.MatchString(hash) || hex.EncodeToString(sum[:]) != hash || len(css) > maxSheetBytes || total+len(css) > maxSheetsBytes {
			continue // una hoja que no es la de su hash no se guarda con ese nombre
		}
		total += len(css)
		sheets[hash] = cleanCSS(css)
	}
	h.Sheets = nil
	var refs []string
	seen := map[string]bool{}
	for _, hash := range h.CSS {
		if sheetHashRe.MatchString(hash) && !seen[hash] && len(refs) < maxSheetRefs {
			seen[hash] = true
			refs = append(refs, hash)
		}
	}
	h.CSS = refs
	var missing []string
	for _, m := range h.Missing {
		if len(missing) < maxMissingSheets {
			missing = append(missing, personal(redactThenCut(p, m, maxURLChars)))
		}
	}
	h.Missing = missing
	if h.DPR < 0 || h.DPR > 10 {
		h.DPR = 0
	}
	h.Doc = cleanHTMLDoc(p, h.Doc)
	if h.Doc == "" {
		snap.HTML = nil
		return sheets
	}
	snap.Mode = "html"
	return sheets
}

// emptySheetHash is the SHA-256 of "": an empty <style> of the page is a sheet that is there, not a lost one.
const emptySheetHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// getDOMCSS returns one shared style sheet of the HTML mode (GET /dom/css/{hash}), already cleaned.
func (s *Server) getDOMCSS(w http.ResponseWriter, r *http.Request) {
	hash := chi.URLParam(r, "hash")
	if !sheetHashRe.MatchString(hash) {
		writeError(w, http.StatusBadRequest, "not a sheet hash")
		return
	}
	css, ok, err := s.Store.GetDOMCSS(hash)
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok && hash != emptySheetHash {
		w.WriteHeader(http.StatusNoContent) // como GET /dom sin snapshot: el visor avisa la hoja que falta
		return
	}
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable") // el contenido es su hash
	_, _ = w.Write([]byte(css))
}
