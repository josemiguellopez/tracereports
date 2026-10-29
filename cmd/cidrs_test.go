package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// Un TRACEREPORTS_ALLOWED_CIDRS mal escrito no se ignora: el servidor no arranca.
func TestServerRejectsInvalidCIDRs(t *testing.T) {
	for name, value := range map[string]string{"ALLOWED_CIDRS": "10.0.0.0/33", "TRUSTED_PROXIES": "proxy.interno"} {
		t.Run(name, func(t *testing.T) {
			restoreEnv(t)
			port, err := freePort(0, 8460)
			if err != nil {
				t.Fatal(err)
			}
			demoEnv(t.TempDir(), port, false)
			os.Setenv("TRACEREPORTS_"+name, value)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if err := runServer(ctx); err == nil || !strings.Contains(err.Error(), "TRACEREPORTS_"+name) {
				t.Fatalf("debe negarse a arrancar: %v", err)
			}
		})
	}
}
