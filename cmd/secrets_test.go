package main

import (
	"bytes"
	"encoding/base64"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/secret"
)

// claves maestras ficticias de prueba
var (
	migKeyA = base64.StdEncoding.EncodeToString([]byte("migrate-test-key-A-0123456789abc"))
	migKeyB = base64.StdEncoding.EncodeToString([]byte("migrate-test-key-B-0123456789abc"))
)

func tempStore(t *testing.T) (*db.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := db.Open(filepath.Join(dir, "tracereports.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store, dir
}

func box(t *testing.T, cur, prev string) *secret.Box {
	t.Helper()
	b, err := secret.New(cur, prev)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func stored(t *testing.T, s *db.Store) string {
	t.Helper()
	saved, _ := s.Settings()
	return saved["ai.api_key"]
}

func TestSecretsMigrateEncryptsLegacyPlaintext(t *testing.T) {
	store, _ := tempStore(t)
	store.SaveSettings(map[string]string{"ai.provider": "openai", "ai.api_key": "sk-legacy-plain-9999"})

	var out bytes.Buffer
	if err := secretsOn(store, box(t, migKeyA, ""), "status", false, &out); err != nil || !strings.Contains(out.String(), "ai.api_key: in clear") {
		t.Fatalf("status: %v %s", err, out.String())
	}
	if strings.Contains(out.String(), "sk-legacy") {
		t.Fatal("status must not print the key")
	}
	out.Reset()
	if err := secretsOn(store, box(t, migKeyA, ""), "migrate", true, &out); err != nil || stored(t, store) != "sk-legacy-plain-9999" {
		t.Fatalf("dry-run must not change anything: %v", err)
	}
	out.Reset()
	if err := secretsOn(store, box(t, migKeyA, ""), "migrate", false, &out); err != nil {
		t.Fatalf("migrate: %v %s", err, out.String())
	}
	v := stored(t, store)
	if !secret.IsSealed(v) || strings.Contains(v, "sk-legacy") || strings.Contains(out.String(), "sk-legacy") {
		t.Fatalf("after migrate: %q / %s", v, out.String())
	}
	if plain, err := box(t, migKeyA, "").Open("ai.api_key", v); err != nil || plain != "sk-legacy-plain-9999" {
		t.Fatalf("readable after migrate: %q %v", plain, err)
	}
	// otra vez: no hay nada que migrar
	out.Reset()
	secretsOn(store, box(t, migKeyA, ""), "migrate", false, &out)
	if stored(t, store) != v || !strings.Contains(out.String(), "0 credential(s) migrated") {
		t.Fatalf("idempotent: %s", out.String())
	}
}

func TestSecretsMigrateRotatesAndRefusesWhatItCannotRead(t *testing.T) {
	store, _ := tempStore(t)
	sealedA, _ := box(t, migKeyA, "").Seal("ai.api_key", "sk-rotate-1111")
	store.SaveSettings(map[string]string{"ai.api_key": sealedA})

	// con una clave equivocada: error y el valor queda igual
	var out bytes.Buffer
	if err := secretsOn(store, box(t, migKeyB, ""), "migrate", false, &out); err == nil || stored(t, store) != sealedA {
		t.Fatalf("wrong key must fail without touching: %v", err)
	}
	// rotación: nueva B, anterior A
	out.Reset()
	if err := secretsOn(store, box(t, migKeyB, migKeyA), "migrate", false, &out); err != nil {
		t.Fatalf("rotate: %v %s", err, out.String())
	}
	v := stored(t, store)
	if v == sealedA || secret.KeyIDOf(v) != box(t, migKeyB, "").KeyID() {
		t.Fatalf("must be re-encrypted with the new key: %q", v)
	}
	if plain, err := box(t, migKeyB, "").Open("ai.api_key", v); err != nil || plain != "sk-rotate-1111" {
		t.Fatalf("new key reads it: %v", err)
	}
}

func TestSecretsCommandNeedsKeyAndDatabase(t *testing.T) {
	_, dir := tempStore(t)
	t.Setenv("TRACEREPORTS_ENV_FILE", filepath.Join(dir, "no.env"))
	t.Setenv("TRACEREPORTS_SECRET_KEY", "")
	t.Setenv("TRACEREPORTS_SECRET_KEY_PREVIOUS", "")
	var out bytes.Buffer
	if err := runSecrets([]string{"migrate", "-data-dir", dir}, &out); err == nil || !strings.Contains(err.Error(), "TRACEREPORTS_SECRET_KEY") {
		t.Fatalf("migrate without key: %v", err)
	}
	if err := runSecrets([]string{"status", "-data-dir", filepath.Join(dir, "missing")}, &out); err == nil {
		t.Fatal("status without database must fail (it must not create one)")
	}
	if err := runSecrets([]string{"status", "-data-dir", dir}, &out); err != nil || !strings.Contains(out.String(), "master key: not set") {
		t.Fatalf("status: %v %s", err, out.String())
	}
}
