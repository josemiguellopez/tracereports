package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	os.WriteFile(path, []byte(`# comentario
TRACEREPORTS_UI_USER=qa-admin
export TRACEREPORTS_UI_PASSWORD="Clave con espacios"
AI_PROVIDER=gemini   # comentario al final
AI_MODEL='gemini-flash-lite-latest'
EMPTY=
PORT=9999
not a line
`), 0o600)
	for _, k := range []string{"TRACEREPORTS_UI_USER", "TRACEREPORTS_UI_PASSWORD", "AI_PROVIDER", "AI_MODEL", "EMPTY"} {
		t.Setenv(k, "")
		os.Unsetenv(k)
	}
	t.Setenv("PORT", "8080") // ya definida: gana el entorno

	n, err := loadDotEnv(path)
	if err != nil || n != 5 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	want := map[string]string{"TRACEREPORTS_UI_USER": "qa-admin", "TRACEREPORTS_UI_PASSWORD": "Clave con espacios",
		"AI_PROVIDER": "gemini", "AI_MODEL": "gemini-flash-lite-latest", "EMPTY": "", "PORT": "8080"}
	for k, v := range want {
		if got := os.Getenv(k); got != v {
			t.Errorf("%s = %q, want %q", k, got, v)
		}
	}
	if n, err := loadDotEnv(filepath.Join(t.TempDir(), "missing.env")); n != 0 || err != nil {
		t.Fatalf("missing file should be ignored: %d %v", n, err)
	}
}
