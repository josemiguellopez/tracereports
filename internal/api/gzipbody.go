package api

import (
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"
)

// Las subidas de evidencia (red, DOM, pasos) son JSON que se comprime entre 5 y 10 veces. Un cliente
// que lo sabe las manda con Content-Encoding: gzip: con muchos workers subiendo a la vez, la red (y el
// reenvío de puertos de Docker) deja de ser el cuello de botella. El servidor avisa en cada respuesta
// que lo acepta (acceptEncodingHeader); un cliente que no vio el aviso manda sin comprimir, así un
// cliente nuevo sigue funcionando con un servidor viejo.

const (
	acceptEncodingHeader = "X-TraceReports-Accept-Encoding"
	// maxInflatedBody: tope de lo descomprimido (el más grande, un lote de red, es de 48 MiB). Cada
	// handler aplica además su propio límite sobre lo descomprimido: un gzip chico que se infla a
	// gigas no pasa de acá.
	maxInflatedBody = 64 << 20
)

var errBadGzip = errors.New("invalid gzip body")

func gzipBodies(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(acceptEncodingHeader, "gzip")
		enc := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding")))
		if enc == "" || enc == "identity" {
			next.ServeHTTP(w, r)
			return
		}
		if enc != "gzip" {
			writeError(w, http.StatusUnsupportedMediaType, "unsupported Content-Encoding (use gzip or none)")
			return
		}
		zr, err := gzip.NewReader(http.MaxBytesReader(w, r.Body, maxInflatedBody))
		if err != nil {
			writeError(w, http.StatusBadRequest, errBadGzip.Error())
			return
		}
		defer zr.Close()
		r.Header.Del("Content-Encoding")
		r.Header.Del("Content-Length")
		r.ContentLength = -1
		r.Body = struct {
			io.Reader
			io.Closer
		}{http.MaxBytesReader(w, io.NopCloser(zr), maxInflatedBody), r.Body}
		next.ServeHTTP(w, r)
	})
}
