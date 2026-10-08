package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/secret"
)

// claves maestras ficticias, solo para pruebas
var (
	testMasterKey  = base64.StdEncoding.EncodeToString([]byte("test-master-key-0123456789abcdef"))
	otherMasterKey = base64.StdEncoding.EncodeToString([]byte("another-master-key-9876543210xyz"))
)

const fakeAIKey = "sk-test-FAKE-secret-key-4321"

func masterBox(t *testing.T, key string) *secret.Box {
	t.Helper()
	b, err := secret.New(key, "")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func storedKey(t *testing.T, srv *Server) string {
	t.Helper()
	saved, err := srv.Store.Settings()
	if err != nil {
		t.Fatal(err)
	}
	return saved[setAPIKey]
}

func settingsView(t *testing.T, srv *Server) (map[string]any, string) {
	t.Helper()
	rec := call(t, srv, "GET", "/api/v1/settings", "")
	var v map[string]any
	json.Unmarshal(rec.Body.Bytes(), &v)
	return v, rec.Body.String()
}

func savedKeyState(v map[string]any) string {
	s, _ := v["secrets"].(map[string]any)
	st, _ := s["saved_key"].(string)
	return st
}

func TestAIKeyIsStoredEncryptedAndSurvivesRestart(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Secrets = masterBox(t, testMasterKey)

	rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","api_key":"`+fakeAIKey+`"}}`)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), fakeAIKey) {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	stored := storedKey(t, srv)
	if !secret.IsSealed(stored) || strings.Contains(stored, fakeAIKey) || strings.Contains(stored, "FAKE") {
		t.Fatalf("the stored value must be ciphertext, not the key: %q", stored)
	}
	if srv.AI.Config().APIKey != fakeAIKey {
		t.Fatal("the running server uses the key")
	}
	v, body := settingsView(t, srv)
	if strings.Contains(body, fakeAIKey) || strings.Contains(body, stored) || savedKeyState(v) != "encrypted" {
		t.Fatalf("GET must not reveal the key nor the ciphertext: %s", body)
	}
	if v["ai"].(map[string]any)["key_hint"] != "••••4321" {
		t.Fatalf("hint: %v", v["ai"])
	}

	// reinicio: otro proceso con la misma clave maestra la lee
	srv2 := &Server{Store: srv.Store, AI: ai.New(srv.Store), Secrets: masterBox(t, testMasterKey)}
	if err := srv2.LoadSettings(); err != nil || srv2.AI.Config().APIKey != fakeAIKey || srv2.AI.Provider() != "openai" {
		t.Fatalf("restart: %v %+v", err, srv2.AI.Config().Provider)
	}
	// cambiar solo el modelo conserva la key cifrada
	call(t, srv2, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","model":"gpt-5"}}`)
	if srv2.AI.Config().APIKey != fakeAIKey || storedKey(t, srv2) != stored {
		t.Fatal("a model change must keep the stored key as is")
	}
}

func TestAIKeyWithoutMasterKeyIsRefused(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t) // sin TRACEREPORTS_SECRET_KEY
	rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","api_key":"`+fakeAIKey+`"}}`)
	if rec.Code != 409 || !strings.Contains(rec.Body.String(), "TRACEREPORTS_SECRET_KEY") || strings.Contains(rec.Body.String(), fakeAIKey) {
		t.Fatalf("must refuse with a clear error: %d %s", rec.Code, rec.Body)
	}
	if storedKey(t, srv) != "" || srv.AI.Enabled() {
		t.Fatal("nothing stored nor applied")
	}
	// la IA por variables de entorno sigue funcionando sin clave maestra
	t.Setenv("OPENAI_API_KEY", "sk-env-key-0000")
	if rec := call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai"}}`); rec.Code != 200 || srv.AI.Config().APIKey != "sk-env-key-0000" {
		t.Fatalf("env key: %d %s", rec.Code, rec.Body)
	}
}

func TestEncryptedKeyWithMissingOrWrongMasterKey(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Secrets = masterBox(t, testMasterKey)
	call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","api_key":"`+fakeAIKey+`"}}`)
	stored := storedKey(t, srv)

	for _, tc := range []struct {
		name  string
		box   *secret.Box
		state string
	}{{"missing", nil, "master_key_missing"}, {"wrong", masterBox(t, otherMasterKey), "master_key_wrong"}} {
		t.Run(tc.name, func(t *testing.T) {
			s2 := &Server{Store: srv.Store, AI: ai.New(srv.Store), Secrets: tc.box}
			if err := s2.LoadSettings(); err != nil {
				t.Fatalf("startup must not fail because of the AI key: %v", err)
			}
			if s2.AI.Config().APIKey == fakeAIKey {
				t.Fatal("cannot have decrypted it")
			}
			v, body := settingsView(t, s2)
			if savedKeyState(v) != tc.state || strings.Contains(body, stored) {
				t.Fatalf("state: %v", v["secrets"])
			}
			// cambiar el modelo sin key: error claro, no se usa otra key en silencio
			rec := call(t, s2, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","model":"gpt-5"}}`)
			if rec.Code != 409 || !strings.Contains(rec.Body.String(), "cannot be decrypted") {
				t.Fatalf("PUT: %d %s", rec.Code, rec.Body)
			}
			var res map[string]any
			json.Unmarshal(call(t, s2, "POST", "/api/v1/settings/ai/test", `{"provider":"openai"}`).Body.Bytes(), &res)
			if res["ok"] != false || !strings.Contains(res["error"].(string), "cannot be decrypted") {
				t.Fatalf("test connection: %v", res)
			}
			// cambiar solo el idioma no toca la credencial
			call(t, s2, "PUT", "/api/v1/settings", `{"language":"en"}`)
			if storedKey(t, s2) != stored {
				t.Fatal("the stored credential must stay untouched")
			}
		})
	}
	// con la clave correcta vuelve a funcionar
	s3 := &Server{Store: srv.Store, AI: ai.New(srv.Store), Secrets: masterBox(t, testMasterKey)}
	if err := s3.LoadSettings(); err != nil || s3.AI.Config().APIKey != fakeAIKey {
		t.Fatalf("back with the right key: %v", err)
	}
}

func TestLegacyPlaintextKeyStillWorksUntilMigrated(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	// así lo guardaban las versiones anteriores
	if err := srv.Store.SaveSettings(map[string]string{setProvider: "openai", setAPIKey: fakeAIKey}); err != nil {
		t.Fatal(err)
	}
	for _, box := range []*secret.Box{nil, masterBox(t, testMasterKey)} {
		s2 := &Server{Store: srv.Store, AI: ai.New(srv.Store), Secrets: box}
		if err := s2.LoadSettings(); err != nil || s2.AI.Config().APIKey != fakeAIKey {
			t.Fatalf("legacy key must keep working (box=%v): %v", box.Enabled(), err)
		}
		v, body := settingsView(t, s2)
		if savedKeyState(v) != "plaintext" || strings.Contains(body, fakeAIKey) {
			t.Fatalf("legacy state: %v", v["secrets"])
		}
	}
	if storedKey(t, srv) != fakeAIKey {
		t.Fatal("startup must not migrate by itself")
	}
}

func TestAPIResponsesNeverRevealTheKey(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	srv.Secrets = masterBox(t, testMasterKey)
	call(t, srv, "PUT", "/api/v1/settings", `{"ai":{"provider":"openai","api_key":"`+fakeAIKey+`"}}`)
	stored := storedKey(t, srv)
	for _, path := range []string{"/api/v1/settings", "/api/v1/config"} {
		body := call(t, srv, "GET", path, "").Body.String()
		if strings.Contains(body, fakeAIKey) || strings.Contains(body, stored) {
			t.Fatalf("%s reveals the key: %s", path, body)
		}
	}
	// un error del proveedor tampoco la devuelve
	rec := call(t, srv, "POST", "/api/v1/settings/ai/test", `{"provider":"openai","base_url":"http://127.0.0.1:1"}`)
	if bytes.Contains(rec.Body.Bytes(), []byte(fakeAIKey)) {
		t.Fatalf("test error reveals the key: %s", rec.Body)
	}
}
