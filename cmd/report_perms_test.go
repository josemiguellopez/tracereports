package main

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// El reporte estático trae la evidencia: queda solo para el usuario (0700/0600).
func TestStaticReportIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("sin permisos POSIX")
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, name := range []string{"reporte/index.html", "reporte/screenshots/t1.png"} {
		w, _ := zw.Create(name)
		w.Write([]byte("contenido"))
	}
	zw.Close()
	dir := filepath.Join(t.TempDir(), "salida")
	if err := unzipTo(buf.Bytes(), dir); err != nil {
		t.Fatal(err)
	}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		info, _ := os.Stat(p)
		want := fs.FileMode(0o600)
		if d.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Errorf("%s: %#o, quería %#o", p, info.Mode().Perm(), want)
		}
		return nil
	})
}

// Una entrada con ".." no escribe fuera de la carpeta del reporte (zip slip).
func TestStaticReportRejectsZipSlip(t *testing.T) {
	for _, name := range []string{"reporte/../../fuera.txt", "../fuera.txt", "reporte/a/../../../fuera.txt"} {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		w, _ := zw.Create(name)
		w.Write([]byte("x"))
		zw.Close()
		base := t.TempDir()
		if err := unzipTo(buf.Bytes(), filepath.Join(base, "salida")); err == nil {
			t.Errorf("%s: aceptada", name)
		}
		if _, err := os.Stat(filepath.Join(base, "fuera.txt")); err == nil {
			t.Errorf("%s: escribió fuera", name)
		}
	}
}
