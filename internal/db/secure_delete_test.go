package db

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSecureDeletePragmaIsOn(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var on int
	if err := s.db.QueryRow(`PRAGMA secure_delete`).Scan(&on); err != nil || on != 1 {
		t.Fatalf("secure_delete = %d %v", on, err)
	}
}

// fileHas reports whether the database file or its WAL contain needle.
func fileHas(t *testing.T, path string, needle []byte) bool {
	t.Helper()
	for _, p := range []string{path, path + "-wal"} {
		data, err := os.ReadFile(p)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(data, needle) {
			return true
		}
	}
	return false
}

// Lo que borra la retención no queda en el archivo .db ni en la WAL: ni en páginas libres, ni en
// copias viejas de las páginas.
func TestPurgedEvidenceLeavesNoBytesOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	marker := "SECRETO-DE-RETENCION-" + strings.Repeat("Q7", 40)
	runID, _ := s.CreateRun("vieja", "qa")
	testID, _ := s.CreateTest(runID, "pago", "", "")
	s.AddLog(testID, "FAIL", "paso con "+marker, 0, "")
	s.AddNetwork(testID, []NetConn{{Method: "POST", URL: "https://shop.test/api/pay", Status: 500, ResponseBody: `{"tarjeta":"` + marker + `"}`}})
	s.FinishTest(testID, "FAIL", "error con "+marker, "")
	s.FinishRun(runID)
	keep, _ := s.CreateRun("nueva", "qa")
	s.FinishRun(keep)
	if _, err := s.db.Exec(`UPDATE runs SET started_at = 1 WHERE id = ?`, runID); err != nil {
		t.Fatal(err)
	}
	// control: antes de purgar, el texto está en disco (si no, la prueba no probaría nada)
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	if !fileHas(t, path, []byte(marker)) {
		t.Fatal("el texto debería estar en el archivo antes de purgar")
	}

	n, _, err := s.PurgeRunsBefore(1000)
	if err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if err := s.Compact(); err != nil {
		t.Fatal(err)
	}
	if fileHas(t, path, []byte(marker)) {
		t.Fatal("el texto purgado sigue en los bytes del .db o la WAL")
	}
	// la base sigue sana y con la ejecución que no se purgó
	if got, err := s.GetRun(keep); err != nil || got.Name != "nueva" {
		t.Fatalf("run conservada: %+v %v", got, err)
	}
	s.Close()
	if fileHas(t, path, []byte(marker)) {
		t.Fatal("el texto purgado reapareció al cerrar")
	}
}

func TestDataDirIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permisos POSIX")
	}
	dir := filepath.Join(t.TempDir(), "data")
	s, err := Open(filepath.Join(dir, "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := os.Stat(dir)
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("DATA_DIR: %v %v", info.Mode(), err)
	}
}
