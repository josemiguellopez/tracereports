package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Una allowlist con un proveedor mal escrito no arranca: no deja al servidor sin diagnóstico en silencio.
func TestServerRejectsAnUnknownAllowedProvider(t *testing.T) {
	restoreEnv(t)
	port, err := freePort(0, 8480)
	if err != nil {
		t.Fatal(err)
	}
	demoEnv(t.TempDir(), port, false)
	os.Setenv("TRACEREPORTS_AI_ALLOWED_PROVIDERS", "builtin,olama")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runServer(ctx); err == nil || !strings.Contains(err.Error(), "unknown provider olama") {
		t.Fatalf("debe negarse a arrancar: %v", err)
	}
}
