package tracereports

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// La grabación lleva la evidencia sin redactar (eso lo hace el servidor): nadie más del equipo la lee.
func TestRecordingIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sin permisos POSIX")
	}
	dir := filepath.Join(t.TempDir(), "grabacion")
	r, err := newRecorder(dir)
	if err != nil {
		t.Fatal(err)
	}
	r.record("POST", "/api/v1/runs", []byte(`{"name":"x"}`), "application/json")
	r.record("POST", "/api/v1/tests/-1/screenshot", []byte("\x89PNG secreto"), "image/png")
	r.close()
	n := 0
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		info, _ := os.Stat(p)
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: %#o, quería %#o", p, info.Mode().Perm(), want)
		}
		n++
		return nil
	})
	if n < 5 {
		t.Fatalf("solo %d entradas", n)
	}
}
