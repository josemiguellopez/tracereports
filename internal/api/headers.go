package api

import (
	"crypto/sha256"
	"encoding/base64"
	"io/fs"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

// inlineScript matches the inline <script> blocks of index.html (the ones without src).
var inlineScript = regexp.MustCompile(`(?s)<script>(.*?)</script>`)

// contentSecurityPolicy builds the CSP of the UI: only scripts of this server plus the inline ones
// of index.html, allowed by their hash (computed from the embedded file, so it never goes stale).
// Styles allow inline attributes (the UI sets widths and colors with style=""), never other origins.
func contentSecurityPolicy(web fs.FS) string {
	scripts := []string{"'self'"}
	if web != nil {
		if raw, err := fs.ReadFile(web, "index.html"); err == nil {
			for _, m := range inlineScript.FindAllSubmatch(raw, -1) {
				sum := sha256.Sum256(m[1])
				scripts = append(scripts, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
			}
		}
	}
	return strings.Join([]string{
		"default-src 'self'",
		"script-src " + strings.Join(scripts, " "),
		"style-src 'self' 'unsafe-inline'",
		"img-src 'self' data: blob:",
		"font-src 'self' data:",
		"connect-src 'self'",
		"object-src 'none'",
		"base-uri 'none'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	}, "; ")
}

// securityHeaders adds the browser protections to every response: the UI cannot be framed by
// another site (clickjacking), the browser does not guess content types, the Referer does not
// leave the server, and pages only run this server's scripts. A handler that needs a stricter
// policy (a captured response body) sets its own after this.
func securityHeaders(csp string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Frame-Options", "DENY")
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", "same-origin")
			h.Set("Content-Security-Policy", csp)
			next.ServeHTTP(w, r)
		})
	}
}

// sameSiteWrites rejects the writes a browser makes on behalf of another site (CSRF): a page on
// any origin can post a form to http://localhost:8080 while the developer browses. Browsers say
// where a request comes from (Origin, Sec-Fetch-Site); test clients and CI send neither and pass.
func sameSiteWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			cross := r.Header.Get("Sec-Fetch-Site") == "cross-site"
			if o := r.Header.Get("Origin"); o != "" {
				u, err := url.Parse(o)
				cross = cross || err != nil || o == "null" || !strings.EqualFold(u.Host, r.Host)
			}
			if cross {
				writeError(w, http.StatusForbidden, "cross-site request rejected")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}
