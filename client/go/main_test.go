package tracereports

import (
	"os"
	"testing"
)

// TestMain keeps the tests from downloading the report binary from GitHub: a test that needs it
// uses $TRACEREPORTS_BIN, and the download itself is tested against a local release server.
func TestMain(m *testing.M) {
	if os.Getenv("TRACEREPORTS_BIN_DOWNLOAD") == "" {
		os.Setenv("TRACEREPORTS_BIN_DOWNLOAD", "0")
	}
	os.Exit(m.Run())
}
