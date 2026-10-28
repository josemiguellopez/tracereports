package atrest

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/env"
	"github.com/josemiguellopez/tracereports/internal/secret"
)

// FromEnv builds the Vault of the server: TRACEREPORTS_ENCRYPT_AT_REST=1 turns encryption on and
// needs TRACEREPORTS_DB_KEY (or TRACEREPORTS_DB_KEY_FILE: a Docker/Kubernetes secret, a Key Vault
// CSI mount...). TRACEREPORTS_DB_KEY_PREVIOUS (or _FILE) reads what the previous key encrypted while
// rotating. A key file inside dataDir is refused: a stolen volume would carry its own key. Without
// encryption and without a key it returns nil (nothing changes); with a key but encryption off, the
// Vault only reads evidence encrypted before.
func FromEnv(dataDir string) (*Vault, error) {
	encrypt := env.Bool("ENCRYPT_AT_REST")
	current, err := keyFromEnv("DB_KEY", dataDir)
	if err != nil {
		return nil, err
	}
	previous, err := keyFromEnv("DB_KEY_PREVIOUS", dataDir)
	if err != nil {
		return nil, err
	}
	if current == nil {
		if encrypt {
			return nil, errors.New("TRACEREPORTS_ENCRYPT_AT_REST=1 needs TRACEREPORTS_DB_KEY or TRACEREPORTS_DB_KEY_FILE " +
				"(32 random bytes in base64 or hex: openssl rand -base64 32), outside DATA_DIR")
		}
		if previous != nil {
			return nil, errors.New("TRACEREPORTS_DB_KEY_PREVIOUS without TRACEREPORTS_DB_KEY")
		}
		return nil, nil
	}
	return New(current, previous, encrypt)
}

// keyFromEnv reads TRACEREPORTS_<name> or TRACEREPORTS_<name>_FILE (nil when neither is set).
func keyFromEnv(name, dataDir string) ([]byte, error) {
	value, file := strings.TrimSpace(env.Get(name)), strings.TrimSpace(env.Get(name+"_FILE"))
	switch {
	case value != "" && file != "":
		return nil, fmt.Errorf("set TRACEREPORTS_%s or TRACEREPORTS_%s_FILE, not both", name, name)
	case file != "":
		if inside(file, dataDir) {
			return nil, fmt.Errorf("TRACEREPORTS_%s_FILE is inside DATA_DIR: keep the key out of the volume it protects", name)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			return nil, fmt.Errorf("TRACEREPORTS_%s_FILE: %w", name, err)
		}
		value = strings.TrimSpace(string(raw))
	case value == "":
		return nil, nil
	}
	key, err := secret.ParseKey(value)
	if err != nil {
		return nil, fmt.Errorf("TRACEREPORTS_%s: %w", name, err)
	}
	return key, nil
}

// inside reports whether path is dir or below it (after resolving both).
func inside(path, dir string) bool {
	if dir == "" {
		return false
	}
	p, err1 := filepath.Abs(path)
	d, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return false
	}
	if rp, err := filepath.EvalSymlinks(p); err == nil {
		p = rp
	}
	if rd, err := filepath.EvalSymlinks(d); err == nil {
		d = rd
	}
	rel, err := filepath.Rel(d, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
