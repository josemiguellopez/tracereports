package api

import (
	"fmt"
	"net/http"
	"strings"
)

// Política de capturas del servidor (TRACEREPORTS_SCREENSHOTS). Una captura puede mostrar datos
// personales o credenciales en pantalla, y la redacción de texto no la alcanza: el servidor decide
// qué imágenes guarda, sin depender de que cada suite tenga bien configurados sus clientes.
//   - all (por defecto): todas.
//   - failures: solo las de un paso o un test que falló.
//   - off: ninguna. Tampoco el trace ni el video de Playwright, que muestran la pantalla.
// La imagen se rechaza antes de escribirla: nunca queda un archivo en ScreenshotsDir.

// Screenshot policies.
const (
	ScreenshotsAll      = "all"
	ScreenshotsFailures = "failures"
	ScreenshotsOff      = "off"
)

// ParseScreenshotPolicy reads TRACEREPORTS_SCREENSHOTS ("" = all).
func ParseScreenshotPolicy(v string) (string, error) {
	switch p := strings.ToLower(strings.TrimSpace(v)); p {
	case "", ScreenshotsAll:
		return ScreenshotsAll, nil
	case ScreenshotsFailures, ScreenshotsOff:
		return p, nil
	}
	return "", fmt.Errorf("TRACEREPORTS_SCREENSHOTS must be all, failures or off (got %q)", v)
}

// screenshotPolicy is the active policy (empty Screenshots = all).
func (s *Server) screenshotPolicy() string {
	if s.Screenshots == "" {
		return ScreenshotsAll
	}
	return s.Screenshots
}

// screenshotAllowed reports whether an image may be stored: stepStatus is the status of the step
// it belongs to (FAIL for the capture of a failure) and testStatus the status of its test.
func (s *Server) screenshotAllowed(stepStatus, testStatus string) bool {
	switch s.screenshotPolicy() {
	case ScreenshotsOff:
		return false
	case ScreenshotsFailures:
		return stepStatus == "FAIL" || testStatus == "FAIL"
	}
	return true
}

// screenshotRefused answers an upload the policy does not allow.
func (s *Server) screenshotRefused(w http.ResponseWriter) {
	msg := "screenshots are disabled on this server (TRACEREPORTS_SCREENSHOTS=off)"
	if s.screenshotPolicy() == ScreenshotsFailures {
		msg = "this server only keeps screenshots of failures (TRACEREPORTS_SCREENSHOTS=failures): send it with status FAIL"
	}
	writeError(w, http.StatusForbidden, msg)
}
