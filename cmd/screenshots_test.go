package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Un valor de TRACEREPORTS_SCREENSHOTS mal escrito no se ignora: el servidor no arranca.
func TestServerRejectsAnUnknownScreenshotPolicy(t *testing.T) {
	restoreEnv(t)
	port, err := freePort(0, 8470)
	if err != nil {
		t.Fatal(err)
	}
	demoEnv(t.TempDir(), port, false)
	os.Setenv("TRACEREPORTS_SCREENSHOTS", "none")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := runServer(ctx); err == nil || !strings.Contains(err.Error(), "TRACEREPORTS_SCREENSHOTS must be all, failures or off") {
		t.Fatalf("debe negarse a arrancar: %v", err)
	}
}
