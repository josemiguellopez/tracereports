package redact

import "testing"

func TestPersonalData(t *testing.T) {
	cases := map[string]string{
		"RUN 13.905.701-5":                         "RUN <rut>",
		"rut 12345678-K del cliente":              "rut <rut> del cliente",
		"Teléfono móvil +56 (9) 58720108":          "Teléfono móvil <telefono>",
		"Fijo +56 2 2345 6789":                     "Fijo <telefono>",
		"Llamar al (9) 1234 5678":                  "Llamar al <telefono>",
		"US +1 415 555 0100":                       "US <telefono>",
		"escribir a juana.perez@correo.cl hoy":     "escribir a <correo> hoy",
		"06-11-2026 17:26 Pedido no completado":  "06-11-2026 17:26 Pedido no completado", // las horas de la agenda quedan
		"28-08-1980 00:00":                         "28-08-1980 00:00",
		"Edad 46 años 2 meses 9 días · Box 12":     "Edad 46 años 2 meses 9 días · Box 12",
		"Pedido 1234-5678 y ficha 98765":           "Pedido 1234-5678 y ficha 98765",
		"Tumor maligno de la columna (Confirmada)": "Tumor maligno de la columna (Confirmada)",
	}
	for in, want := range cases {
		if got := PersonalData(in); got != want {
			t.Errorf("PersonalData(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPersonalDataEnabled(t *testing.T) {
	t.Setenv("TRACEREPORTS_REDACT_PII", "")
	if !PersonalDataEnabled() {
		t.Fatal("encendido por defecto")
	}
	t.Setenv("TRACEREPORTS_REDACT_PII", "off")
	if PersonalDataEnabled() {
		t.Fatal("off lo apaga")
	}
}
