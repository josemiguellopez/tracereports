// Package env reads the server configuration from environment variables named TRACEREPORTS_<NAME>.
package env

import (
	"os"
	"strings"
)

// Prefix of the configuration variables.
const Prefix = "TRACEREPORTS_"

// Get returns TRACEREPORTS_<name>.
func Get(name string) string {
	return os.Getenv(Prefix + name)
}

// Bool reports whether TRACEREPORTS_<name> is 1, true or yes.
func Bool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(Get(name))) {
	case "1", "true", "yes":
		return true
	}
	return false
}
