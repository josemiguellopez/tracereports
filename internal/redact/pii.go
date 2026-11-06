package redact

import (
	"os"
	"regexp"
	"strings"
)

// Datos personales en el texto de una página (el snapshot del DOM): RUT, teléfonos y correos. Una ficha
// clínica los muestra en texto plano, y con TRACEREPORTS_DOM_STEPS se guardan muchas páginas por test.
// Las fechas no se tocan: no hay forma de distinguir una fecha de nacimiento de la hora de una atención
// o de un paso, y sin ellas el Inspector y Trace Buddy pierden la agenda entera; para esos campos está
// la máscara del cliente ([data-tracereports-mask] o TRACEREPORTS_SCREENSHOT_MASK).
var (
	rutRe   = regexp.MustCompile(`\b\d{1,2}\.\d{3}\.\d{3}-[\dkK]\b|\b\d{7,8}-[\dkK]\b`)
	emailRe = regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)
	// +56 9 1234 5678 · +56 (9) 58720108 · +56 2 2345 6789 · (9) 1234 5678 · +1 415 555 0100
	phoneRe = regexp.MustCompile(`\+\d{1,3}[\s.-]*\(?\d{1,4}\)?(?:[\s.-]*\d{2,4}){2,4}|\(\d{1,2}\)[\s.-]*\d{4}[\s.-]*\d{4}`)
)

// PersonalData masks RUTs, phone numbers and e-mail addresses in s ("<rut>", "<telefono>", "<correo>").
func PersonalData(s string) string {
	if s == "" || !strings.ContainsAny(s, "0123456789@") {
		return s
	}
	s = emailRe.ReplaceAllString(s, "<correo>")
	s = rutRe.ReplaceAllString(s, "<rut>")
	return phoneRe.ReplaceAllString(s, "<telefono>")
}

// PersonalDataEnabled is TRACEREPORTS_REDACT_PII: on unless "off" (or 0, false, no).
func PersonalDataEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("TRACEREPORTS_REDACT_PII"))) {
	case "off", "0", "false", "no":
		return false
	}
	return true
}
