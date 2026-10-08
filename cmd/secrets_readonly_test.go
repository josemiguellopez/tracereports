package main

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/secret"
)

// minimalDB crea una base con un esquema mínimo (no el del proyecto). withSettings = false: una
// base de una versión anterior a la tabla settings.
func minimalDB(t *testing.T, withSettings bool, apiKey string) string {
	t.Helper()
	dir := t.TempDir()
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "tracereports.db")))
	if err != nil {
		t.Fatal(err)
	}
	q := `CREATE TABLE runs (id INTEGER PRIMARY KEY, name TEXT);`
	if withSettings {
		q += `CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL);`
	}
	if _, err := conn.Exec(q); err != nil {
		t.Fatal(err)
	}
	if withSettings && apiKey != "" {
		conn.Exec(`INSERT INTO settings VALUES ('ai.provider', 'openai', 1), ('ai.api_key', ?, 1)`, apiKey)
	}
	conn.Close()
	t.Setenv("TRACEREPORTS_ENV_FILE", filepath.Join(dir, "none.env"))
	t.Setenv("TRACEREPORTS_SECRET_KEY_PREVIOUS", "")
	return dir
}

// snapshot: hash del archivo, esquema (sqlite_master) y archivos de la carpeta.
func snapshot(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "tracereports.db"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	conn, _ := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "tracereports.db"))+"?mode=ro")
	defer conn.Close()
	rows, err := conn.Query(`SELECT type || ':' || name FROM sqlite_master ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var objs []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		objs = append(objs, s)
	}
	rows.Close()
	entries, _ := os.ReadDir(dir)
	var files []string
	for _, e := range entries {
		files = append(files, e.Name())
	}
	sort.Strings(files)
	return hex.EncodeToString(sum[:]) + "|" + strings.Join(objs, ",") + "|" + strings.Join(files, ",")
}

func TestStatusAndDryRunDoNotTouchTheDatabase(t *testing.T) {
	dir := minimalDB(t, true, "sk-legacy-plain-4242")
	t.Setenv("TRACEREPORTS_SECRET_KEY", migKeyA)
	before := snapshot(t, dir)
	for _, args := range [][]string{{"status"}, {"migrate", "-dry-run"}} {
		var out bytes.Buffer
		if err := runSecrets(append(args, "-data-dir", dir), &out); err != nil {
			t.Fatalf("%v: %v %s", args, err, out.String())
		}
		if strings.Contains(out.String(), "sk-legacy") {
			t.Fatalf("%v prints the key", args)
		}
		if after := snapshot(t, dir); after != before {
			t.Fatalf("%v changed the database:\nbefore %s\nafter  %s", args, before, after)
		}
	}
}

func TestSecretsNeverCreatesADatabase(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TRACEREPORTS_ENV_FILE", filepath.Join(dir, "none.env"))
	t.Setenv("TRACEREPORTS_SECRET_KEY", migKeyA)
	for _, args := range [][]string{{"status"}, {"migrate", "-dry-run"}, {"migrate"}} {
		var out bytes.Buffer
		if err := runSecrets(append(args, "-data-dir", dir), &out); err == nil || !strings.Contains(err.Error(), "no database") {
			t.Fatalf("%v without a database: %v", args, err)
		}
		if _, err := os.Stat(filepath.Join(dir, "tracereports.db")); err == nil {
			t.Fatalf("%v created a database", args)
		}
	}
}

func TestOldSchemaWithoutSettingsTable(t *testing.T) {
	dir := minimalDB(t, false, "")
	t.Setenv("TRACEREPORTS_SECRET_KEY", migKeyA)
	before := snapshot(t, dir)
	for _, args := range [][]string{{"status"}, {"migrate", "-dry-run"}, {"migrate"}} {
		var out bytes.Buffer
		if err := runSecrets(append(args, "-data-dir", dir), &out); err != nil || !strings.Contains(out.String(), "no settings table") {
			t.Fatalf("%v: clear answer for an old database: %v %s", args, err, out.String())
		}
	}
	if snapshot(t, dir) != before {
		t.Fatal("nothing changes on a database without settings")
	}
}

func TestRealMigrateOnlyRewritesTheCredential(t *testing.T) {
	dir := minimalDB(t, true, "sk-legacy-plain-4242")
	t.Setenv("TRACEREPORTS_SECRET_KEY", migKeyA)
	schemaOf := func() string { s := snapshot(t, dir); return s[strings.Index(s, "|")+1:] }
	schemaBefore := schemaOf()
	var out bytes.Buffer
	if err := runSecrets([]string{"migrate", "-data-dir", dir}, &out); err != nil {
		t.Fatalf("migrate: %v %s", err, out.String())
	}
	if !strings.Contains(schemaOf(), "table:runs,table:settings") || strings.Contains(schemaOf(), "table:tests") {
		t.Fatalf("migrate must not apply the project schema: %s (before %s)", schemaOf(), schemaBefore)
	}
	conn, _ := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "tracereports.db"))+"?mode=ro")
	var v string
	conn.QueryRow(`SELECT value FROM settings WHERE key = 'ai.api_key'`).Scan(&v)
	conn.Close()
	if !secret.IsSealed(v) || strings.Contains(v, "sk-legacy") {
		t.Fatalf("encrypted: %q", v)
	}
	if plain, err := box(t, migKeyA, "").Open("ai.api_key", v); err != nil || plain != "sk-legacy-plain-4242" {
		t.Fatalf("readable: %v", err)
	}
	// rotación por el comando: nueva B, anterior A
	t.Setenv("TRACEREPORTS_SECRET_KEY", migKeyB)
	t.Setenv("TRACEREPORTS_SECRET_KEY_PREVIOUS", migKeyA)
	if err := runSecrets([]string{"migrate", "-data-dir", dir}, &out); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	conn, _ = sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "tracereports.db"))+"?mode=ro")
	conn.QueryRow(`SELECT value FROM settings WHERE key = 'ai.api_key'`).Scan(&v)
	conn.Close()
	if plain, err := box(t, migKeyB, "").Open("ai.api_key", v); err != nil || plain != "sk-legacy-plain-4242" {
		t.Fatalf("rotated to the new key: %v", err)
	}
	// con una clave equivocada falla y no toca nada
	t.Setenv("TRACEREPORTS_SECRET_KEY", migKeyA)
	t.Setenv("TRACEREPORTS_SECRET_KEY_PREVIOUS", "")
	before := snapshot(t, dir)
	if err := runSecrets([]string{"migrate", "-data-dir", dir}, &out); err == nil {
		t.Fatal("a value it cannot read must fail")
	}
	if snapshot(t, dir) != before {
		t.Fatal("and stay untouched")
	}
}
