package api

import (
	"encoding/json"
	"testing"
)

// El idioma de los diagnósticos: lo de Ajustes, si no TRACEREPORTS_AI_LANGUAGE, si no ("auto") el idioma
// del equipo; sin ninguno, el de cada test.
func TestAILanguagePrecedence(t *testing.T) {
	cases := []struct {
		name, saved, env, team, want string
	}{
		{"nada: el de cada test", "", "", "", ""},
		{"auto sigue al equipo", "", "", "es", "es"},
		{"auto explícito sigue al equipo", "auto", "auto", "es", "es"},
		{"el .env manda sobre el equipo", "", "en", "es", "en"},
		{"Ajustes manda sobre el .env", "es", "en", "en", "es"},
		{"valor del .env inválido no cuenta", "", "fr", "es", "es"},
		{"mayúsculas y espacios en el .env", "", " ES ", "", "es"},
	}
	for _, tc := range cases {
		t.Setenv("TRACEREPORTS_AI_LANGUAGE", tc.env)
		got := aiLanguage(map[string]string{setAILanguage: tc.saved, setLanguage: tc.team})
		if got != tc.want {
			t.Errorf("%s: %q, quería %q", tc.name, got, tc.want)
		}
	}
}

func TestValidAILanguage(t *testing.T) {
	for v, ok := range map[string]bool{"": true, "auto": true, "es": true, "EN": true, "fr": false, "spanish": false} {
		t.Setenv("TRACEREPORTS_AI_LANGUAGE", v)
		if err := ValidAILanguage(); (err == nil) != ok {
			t.Errorf("%q: %v", v, err)
		}
	}
}

// Elegir el idioma del equipo con los diagnósticos en "auto" los cambia en el momento, sin reiniciar,
// y la web sabe en qué idioma se están escribiendo.
func TestTeamLanguageAppliesToAutoDiagnoses(t *testing.T) {
	clearAIEnv(t)
	t.Setenv("TRACEREPORTS_AI_LANGUAGE", "")
	srv, _ := newTestServer(t)
	if srv.AI.Language() != "" {
		t.Fatalf("al inicio: %q", srv.AI.Language())
	}
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"language":"es"}`); rec.Code != 200 {
		t.Fatalf("idioma del equipo: %d %s", rec.Code, rec.Body)
	}
	if srv.AI.Language() != "es" {
		t.Fatalf("auto debería seguir al equipo: %q", srv.AI.Language())
	}
	var set map[string]any
	json.Unmarshal(call(t, srv, "GET", "/api/v1/settings", "").Body.Bytes(), &set)
	if set["ai_language"] != "auto" || set["ai_language_effective"] != "es" {
		t.Fatalf("ajustes: %v / %v", set["ai_language"], set["ai_language_effective"])
	}
	call(t, srv, "PUT", "/api/v1/settings", `{"ai_language":"en"}`)
	var cfg map[string]any
	json.Unmarshal(call(t, srv, "GET", "/api/v1/config", "").Body.Bytes(), &cfg)
	if srv.AI.Language() != "en" || cfg["ai_language"] != "en" {
		t.Fatalf("elegido en Ajustes: %q / %v", srv.AI.Language(), cfg["ai_language"])
	}
}

// Con Ajustes bloqueado, el .env decide al arrancar.
func TestAILanguageFromEnvAtStartup(t *testing.T) {
	clearAIEnv(t)
	t.Setenv("TRACEREPORTS_AI_LANGUAGE", "es")
	srv, _ := newTestServer(t)
	if err := srv.LoadSettings(); err != nil {
		t.Fatal(err)
	}
	if srv.AI.Language() != "es" {
		t.Fatalf("desde el .env: %q", srv.AI.Language())
	}
}

// La web sabe qué capturas guarda el servidor, para explicar por qué un test no tiene.
func TestConfigReportsScreenshotPolicy(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	for _, p := range []string{"", ScreenshotsFailures, ScreenshotsOff} {
		srv.Screenshots = p
		var cfg map[string]any
		json.Unmarshal(call(t, srv, "GET", "/api/v1/config", "").Body.Bytes(), &cfg)
		want := p
		if want == "" {
			want = ScreenshotsAll
		}
		if cfg["screenshots"] != want {
			t.Errorf("%q: %v", p, cfg["screenshots"])
		}
	}
}

// Trace Buddy solo ofrece fijar el idioma con un clic cuando Ajustes se puede editar.
func TestConfigReportsSettingsEditable(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	read := func() any {
		var cfg map[string]any
		json.Unmarshal(call(t, srv, "GET", "/api/v1/config", "").Body.Bytes(), &cfg)
		return cfg["settings_editable"]
	}
	if read() != true {
		t.Fatal("desde el mismo equipo, Ajustes es editable")
	}
	srv.SettingsLocked = true
	if read() != false {
		t.Fatal("con TRACEREPORTS_SETTINGS_LOCKED no se ofrece")
	}
}
