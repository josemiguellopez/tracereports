// Package secret encrypts credentials kept in the database (the AI API key saved from Settings)
// with AES-256-GCM. The master key never goes to the database: it comes from
// TRACEREPORTS_SECRET_KEY (32 random bytes in base64 or hex). Without it, nothing is encrypted
// and the server refuses to store new credentials instead of storing them in clear.
//
// Stored format: "enc:v1:<key id>:<base64(nonce || ciphertext)>". The key id is the first 4 bytes
// of SHA-256(master key): it tells "encrypted with another master key" apart from "corrupted"
// without revealing the key. The setting name is the additional data, so a value cannot be moved
// to another setting.
package secret

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/env"
)

const prefix = "enc:v1:"

var (
	// ErrNoKey: there is an encrypted value (or one must be stored) and no master key is configured.
	ErrNoKey = errors.New("TRACEREPORTS_SECRET_KEY is not set")
	// ErrWrongKey: the value was encrypted with a master key that is not configured now.
	ErrWrongKey = errors.New("the value was encrypted with another master key (TRACEREPORTS_SECRET_KEY changed?)")
	// ErrCorrupt: the stored value is not valid ciphertext (or was altered).
	ErrCorrupt = errors.New("the encrypted value is damaged or was altered")
)

type key struct {
	id   string
	aead cipher.AEAD
}

// Box seals and opens values. The zero value and nil have no key: Seal fails and Open only
// returns values that were never encrypted.
type Box struct {
	current  *key
	previous *key // TRACEREPORTS_SECRET_KEY_PREVIOUS: to read values while rotating the key
}

// ParseKey reads a master key: 32 bytes in standard/URL base64 (with or without padding) or hex.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if b, err := enc.DecodeString(s); err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, errors.New("the master key must be 32 random bytes in base64 or hex (e.g. openssl rand -base64 32)")
}

func newKey(raw []byte) (*key, error) {
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return &key{id: hex.EncodeToString(sum[:4]), aead: aead}, nil
}

// New builds a Box. current may be empty (no encryption); previous is optional.
func New(current, previous string) (*Box, error) {
	b := &Box{}
	for _, k := range []struct {
		raw string
		dst **key
		env string
	}{{current, &b.current, "TRACEREPORTS_SECRET_KEY"}, {previous, &b.previous, "TRACEREPORTS_SECRET_KEY_PREVIOUS"}} {
		if strings.TrimSpace(k.raw) == "" {
			continue
		}
		raw, err := ParseKey(k.raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k.env, err)
		}
		if *k.dst, err = newKey(raw); err != nil {
			return nil, err
		}
	}
	if b.current == nil && b.previous != nil {
		return nil, errors.New("TRACEREPORTS_SECRET_KEY_PREVIOUS needs TRACEREPORTS_SECRET_KEY (the new key)")
	}
	return b, nil
}

// FromEnv reads TRACEREPORTS_SECRET_KEY and TRACEREPORTS_SECRET_KEY_PREVIOUS.
func FromEnv() (*Box, error) {
	return New(env.Get("SECRET_KEY"), env.Get("SECRET_KEY_PREVIOUS"))
}

// Enabled reports whether there is a master key to encrypt with.
func (b *Box) Enabled() bool { return b != nil && b.current != nil }

// KeyID is the public id of the current master key ("" without key), for logs and the UI.
func (b *Box) KeyID() string {
	if !b.Enabled() {
		return ""
	}
	return b.current.id
}

// IsSealed reports whether a stored value is encrypted (otherwise it is a legacy clear value).
func IsSealed(stored string) bool { return strings.HasPrefix(stored, prefix) }

// Seal encrypts plain for the setting name.
func (b *Box) Seal(name, plain string) (string, error) {
	if !b.Enabled() {
		return "", ErrNoKey
	}
	nonce := make([]byte, b.current.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	ct := b.current.aead.Seal(nonce, nonce, []byte(plain), []byte(name))
	return prefix + b.current.id + ":" + base64.RawStdEncoding.EncodeToString(ct), nil
}

// Open decrypts a stored value of the setting name. A value that was never encrypted (legacy)
// comes back as is: check IsSealed to tell them apart.
func (b *Box) Open(name, stored string) (string, error) {
	if !IsSealed(stored) {
		return stored, nil
	}
	id, data, ok := strings.Cut(strings.TrimPrefix(stored, prefix), ":")
	if !ok {
		return "", ErrCorrupt
	}
	if b == nil || b.current == nil {
		return "", ErrNoKey
	}
	k := b.current
	if id != k.id {
		if b.previous == nil || id != b.previous.id {
			return "", ErrWrongKey
		}
		k = b.previous
	}
	raw, err := base64.RawStdEncoding.DecodeString(data)
	if err != nil || len(raw) < k.aead.NonceSize() {
		return "", ErrCorrupt
	}
	plain, err := k.aead.Open(nil, raw[:k.aead.NonceSize()], raw[k.aead.NonceSize():], []byte(name))
	if err != nil {
		return "", ErrCorrupt
	}
	return string(plain), nil
}

// NeedsReseal reports whether a stored value should be rewritten with the current key: it is
// in clear (legacy) or encrypted with the previous key.
func (b *Box) NeedsReseal(stored string) bool {
	if stored == "" || !b.Enabled() {
		return false
	}
	if !IsSealed(stored) {
		return true
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(stored, prefix), ":")
	return id != b.current.id
}

// KeyIDOf returns the key id a stored value was encrypted with ("" if it is in clear).
func KeyIDOf(stored string) string {
	if !IsSealed(stored) {
		return ""
	}
	id, _, _ := strings.Cut(strings.TrimPrefix(stored, prefix), ":")
	return id
}
