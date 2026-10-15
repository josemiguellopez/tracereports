package main

import (
	"strings"
	"testing"
)

// The release build sets main.version (goreleaser -X): that is the version it reports.
func TestBuildVersionUsesTheReleaseVersion(t *testing.T) {
	prev := version
	t.Cleanup(func() { version = prev })
	version = "v1.2.3"
	if got := buildVersion(); got != "v1.2.3" {
		t.Fatalf("buildVersion() = %q", got)
	}
	version = ""
	if got := buildVersion(); got == "" {
		t.Fatal("without a release version it still says something (dev or the module version)")
	}
}

// secrets -h prints its usage and is not an error.
func TestSecretsHelp(t *testing.T) {
	var out strings.Builder
	if err := runSecrets([]string{"-h"}, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "status|migrate") {
		t.Fatalf("usage: %q", out.String())
	}
}
