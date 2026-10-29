package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/atrest"
	"github.com/josemiguellopez/tracereports/internal/db"
)

const evidenceMarker = "EVIDENCE_MARKER_51d0"

var (
	key1 = strings.Repeat("01", 32)
	key2 = strings.Repeat("02", 32)
	key3 = strings.Repeat("03", 32)
)

// evidenceData crea un DATA_DIR con una captura y un DOM guardados en claro (antes de activar el cifrado).
func evidenceData(t *testing.T) (dir string, testID int64) {
	t.Helper()
	dir = t.TempDir()
	store, err := db.Open(filepath.Join(dir, "tracereports.db"))
	if err != nil {
		t.Fatal(err)
	}
	runID, _ := store.CreateRun("r", "")
	testID, _ = store.CreateTest(runID, "t", "", "")
	if err := store.SaveDOM(testID, `{"elements":[{"text":"`+evidenceMarker+`"}]}`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	os.MkdirAll(filepath.Join(dir, "screenshots"), 0o700)
	os.WriteFile(filepath.Join(dir, "screenshots", "t1_1.png"), []byte("\x89PNG "+evidenceMarker), 0o600)
	t.Setenv("TRACEREPORTS_ENV_FILE", filepath.Join(dir, "none.env"))
	setKeys(t, "", "")
	return dir, testID
}

func setKeys(t *testing.T, current, previous string) {
	for k, v := range map[string]string{"DB_KEY": current, "DB_KEY_PREVIOUS": previous, "DB_KEY_FILE": "", "DB_KEY_PREVIOUS_FILE": "", "ENCRYPT_AT_REST": ""} {
		t.Setenv("TRACEREPORTS_"+k, v)
	}
}

func evidence(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	var out bytes.Buffer
	err := runEvidence(append(args, "-data-dir", dir), &out)
	return out.String(), err
}

func vaultOf(t *testing.T, hexKey string) *atrest.Vault {
	t.Helper()
	k := make([]byte, 32)
	for i := range k {
		k[i] = hexKey[1] - '0'
	}
	v, err := atrest.New(k, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func readDOM(t *testing.T, dir string, testID int64, v *atrest.Vault) (string, error) {
	t.Helper()
	store, err := db.OpenExisting(filepath.Join(dir, "tracereports.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.SetVault(v)
	return store.GetDOM(testID)
}

func TestEvidenceEncryptThenRekey(t *testing.T) {
	dir, testID := evidenceData(t)

	out, err := evidence(t, dir, "status")
	if err != nil || !strings.Contains(out, "files: 1 in clear") || !strings.Contains(out, "DOM snapshots: 1 in clear") {
		t.Fatalf("status: %v\n%s", err, out)
	}
	if _, err := evidence(t, dir, "encrypt"); err == nil || !strings.Contains(err.Error(), "TRACEREPORTS_DB_KEY is not set") {
		t.Fatalf("sin clave: %v", err)
	}

	setKeys(t, key1, "")
	before := snapshot(t, dir) + hashFile(t, filepath.Join(dir, "screenshots", "t1_1.png"))
	if out, err := evidence(t, dir, "encrypt", "-dry-run"); err != nil || !strings.Contains(out, "1 files and 1 DOM snapshots would be") {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
	if after := snapshot(t, dir) + hashFile(t, filepath.Join(dir, "screenshots", "t1_1.png")); after != before {
		t.Fatal("-dry-run cambió algo")
	}
	if out, err := evidence(t, dir, "encrypt"); err != nil || !strings.Contains(out, "1 files, 1 DOM snapshots") {
		t.Fatalf("encrypt: %v\n%s", err, out)
	}
	if hits := grepFiles(t, dir, evidenceMarker); len(hits) > 0 {
		t.Fatalf("queda evidencia en claro: %v", hits)
	}
	v1 := vaultOf(t, key1)
	if dom, err := readDOM(t, dir, testID, v1); err != nil || !strings.Contains(dom, evidenceMarker) {
		t.Fatalf("DOM tras cifrar: %v", err)
	}
	// de nuevo: nada que hacer
	if out, _ := evidence(t, dir, "encrypt"); !strings.Contains(out, "0 files, 0 DOM snapshots") {
		t.Fatalf("idempotente: %s", out)
	}

	// rotación: clave nueva + la anterior; al terminar, la nueva sola abre todo
	setKeys(t, key2, key1)
	if out, err := evidence(t, dir, "rekey"); err != nil || !strings.Contains(out, "1 files, 1 DOM snapshots") {
		t.Fatalf("rekey: %v\n%s", err, out)
	}
	v2 := vaultOf(t, key2)
	shots := atrest.Dir{Path: filepath.Join(dir, "screenshots"), Vault: v2}
	if got, err := shots.ReadFile("t1_1.png"); err != nil || !strings.Contains(string(got), evidenceMarker) {
		t.Fatalf("captura tras rotar: %v", err)
	}
	if _, err := readDOM(t, dir, testID, v1); err == nil {
		t.Fatal("la clave vieja todavía abre el DOM")
	}
	if dom, err := readDOM(t, dir, testID, v2); err != nil || !strings.Contains(dom, evidenceMarker) {
		t.Fatalf("DOM tras rotar: %v", err)
	}

	// con otra clave sin la anterior: no se pierde nada, se avisa
	setKeys(t, key3, "")
	out, err = evidence(t, dir, "status")
	if err != nil || !strings.Contains(out, "files: 1 encrypted with an unknown key") {
		t.Fatalf("status con otra clave: %v\n%s", err, out)
	}
	if _, err := evidence(t, dir, "rekey"); err == nil || !strings.Contains(err.Error(), "TRACEREPORTS_DB_KEY_PREVIOUS") {
		t.Fatalf("clave desconocida: %v", err)
	}
	if got, err := shots.ReadFile("t1_1.png"); err != nil || !strings.Contains(string(got), evidenceMarker) {
		t.Fatalf("la captura sigue intacta: %v", err)
	}
	if strings.Contains(out, key1) || strings.Contains(out, key2) || strings.Contains(out, key3) {
		t.Fatal("una clave apareció en la salida")
	}
}

func TestEvidenceRefusesKeyInsideDataDir(t *testing.T) {
	dir, _ := evidenceData(t)
	p := filepath.Join(dir, "key.txt")
	os.WriteFile(p, []byte(key1), 0o600)
	t.Setenv("TRACEREPORTS_DB_KEY_FILE", p)
	if _, err := evidence(t, dir, "encrypt"); err == nil || !strings.Contains(err.Error(), "inside DATA_DIR") {
		t.Fatalf("%v", err)
	}
}

func hashFile(t *testing.T, p string) string {
	raw, _ := os.ReadFile(p)
	return string(raw)
}

func grepFiles(t *testing.T, dir, needle string) []string {
	var hits []string
	filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if raw, _ := os.ReadFile(p); bytes.Contains(raw, []byte(needle)) {
				hits = append(hits, p)
			}
		}
		return nil
	})
	return hits
}
