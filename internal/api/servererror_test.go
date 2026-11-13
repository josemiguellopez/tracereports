package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
)

// Un error interno no muestra su causa al navegador, pero lleva una referencia para encontrarlo en el
// log y, si es algo que se puede arreglar (disco lleno), lo dice.
func TestServerErrorSaysWhatToDoAndHowToFindIt(t *testing.T) {
	rec := httptest.NewRecorder()
	serverError(rec, fmt.Errorf("delete runs: %w", syscall.ENOSPC))
	var body struct{ Error, Ref, Kind string }
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != http.StatusInternalServerError || body.Kind != "disk_full" || len(body.Ref) != 8 ||
		!strings.Contains(body.Error, "disk is full") || !strings.Contains(body.Error, body.Ref) {
		t.Fatalf("disk full: %d %s", rec.Code, rec.Body)
	}
	rec = httptest.NewRecorder()
	serverError(rec, errors.New("no such table: secret_stuff"))
	if rec.Code != 500 || strings.Contains(rec.Body.String(), "secret_stuff") || !strings.Contains(rec.Body.String(), "internal error (ref ") {
		t.Fatalf("unknown error: %d %s", rec.Code, rec.Body)
	}
}
