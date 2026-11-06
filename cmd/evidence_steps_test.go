package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Los snapshots por paso (Inspector) entran al cifrado y a la rotación de clave igual que el DOM del
// fallo: el snapshot y la etiqueta del paso, que dice qué hacía el test.
func TestEvidenceEncryptAndRekeyStepSnapshots(t *testing.T) {
	dir, testID := evidenceData(t)
	store, err := db.Open(filepath.Join(dir, "tracereports.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveDOMStep(testID, "paso "+evidenceMarker, 0, `{"nodes":[{"i":0,"p":-1,"tag":"body","text":"`+evidenceMarker+`"}]}`); err != nil {
		t.Fatal(err)
	}
	store.Close()

	setKeys(t, key1, "")
	if out, err := evidence(t, dir, "encrypt"); err != nil || !strings.Contains(out, "1 files, 2 DOM snapshots") {
		t.Fatalf("encrypt: %v\n%s", err, out)
	}
	if hits := grepFiles(t, dir, evidenceMarker); len(hits) > 0 {
		t.Fatalf("queda evidencia en claro: %v", hits)
	}
	setKeys(t, key2, key1)
	if out, err := evidence(t, dir, "rekey"); err != nil || !strings.Contains(out, "2 DOM snapshots") {
		t.Fatalf("rekey: %v\n%s", err, out)
	}
	read := func(key string) (label, snap string) {
		t.Helper()
		s, err := db.Open(filepath.Join(dir, "tracereports.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		s.SetVault(vaultOf(t, key))
		steps, _ := s.ListDOMSteps(testID)
		snap, _ = s.GetDOMStep(testID, 1)
		if len(steps) == 1 {
			label = steps[0].Label
		}
		return label, snap
	}
	if label, snap := read(key2); !strings.Contains(label, evidenceMarker) || !strings.Contains(snap, evidenceMarker) {
		t.Fatalf("con la clave nueva: %q / %q", label, snap)
	}
	if label, snap := read(key1); strings.Contains(label, evidenceMarker) || strings.Contains(snap, evidenceMarker) {
		t.Fatal("la clave vieja todavía abre el snapshot del paso")
	}
}
