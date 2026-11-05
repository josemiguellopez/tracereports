package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// El servidor deja DATA_DIR y la carpeta de capturas solo para su usuario, también si ya existían.
func TestServerDataDirIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permisos POSIX")
	}
	port, dataDir := startEnv(t)
	if err := os.MkdirAll(dataDir, 0o755); err != nil { // creada por una versión anterior
		t.Fatal(err)
	}
	startServer(t, fmt.Sprintf("127.0.0.1:%d", port))
	for _, d := range []string{dataDir, filepath.Join(dataDir, "screenshots")} {
		info, err := os.Stat(d)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: %v %v", d, info.Mode(), err)
		}
	}
}

// `tracereports health` responde 0 con el servidor arriba y error sin él.
func TestHealthCommand(t *testing.T) {
	port, _ := startEnv(t)
	startServer(t, fmt.Sprintf("127.0.0.1:%d", port))
	os.Setenv("PORT", fmt.Sprint(port))
	if err := runHealth(nil); err != nil {
		t.Fatalf("server up: %v", err)
	}
	free, _ := freePort(0, 8490)
	if err := runHealth([]string{"-url", fmt.Sprintf("http://127.0.0.1:%d", free)}); err == nil {
		t.Fatal("sin servidor debe fallar")
	}
}

func TestLocalHealthURL(t *testing.T) {
	for bind, want := range map[string]string{
		"":             "http://127.0.0.1:8080",
		"0.0.0.0":      "http://127.0.0.1:8080",
		"::":           "http://127.0.0.1:8080",
		"[::]":         "http://127.0.0.1:8080",
		"192.168.1.10": "http://192.168.1.10:8080",
		"::1":          "http://[::1]:8080",
	} {
		if got := localHealthURL(bind, "8080"); got != want {
			t.Errorf("%q: %s", bind, got)
		}
	}
}
