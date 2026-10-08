package db

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
)

const settingsSchema = `
CREATE TABLE IF NOT EXISTS settings (
	key        TEXT PRIMARY KEY,
	value      TEXT    NOT NULL,
	updated_at INTEGER NOT NULL
);
`

// Settings returns every stored setting (the ones changed from the Settings screen).
func (s *Store) Settings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

// SaveSettings upserts values; an empty value deletes the key (back to the .env default).
func (s *Store) SaveSettings(values map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := NowMs()
	for k, v := range values {
		if v == "" {
			_, err = tx.Exec(`DELETE FROM settings WHERE key = ?`, k)
		} else {
			_, err = tx.Exec(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
				ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`, k, v, now)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ReplaceSetting changes key from old to new only if it still holds old (compare-and-swap):
// a server running at the same time that saved another value is not overwritten.
func (s *Store) ReplaceSetting(key, old, new string) (bool, error) {
	res, err := s.db.Exec(`UPDATE settings SET value = ?, updated_at = ? WHERE key = ? AND value = ?`, new, NowMs(), key, old)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

// DeleteSettings removes every key starting with prefix (e.g. "ai." to go back to the .env).
func (s *Store) DeleteSettings(prefix string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE substr(key, 1, ?) = ?`, len(prefix), strings.TrimSpace(prefix))
	return err
}

// identitySaltKey keeps the per-installation secret that turns test keys carrying a secret into
// opaque identities (see api.testIdentity). It is never returned by the settings API.
const identitySaltKey = "internal.identity_salt"

// IdentitySalt returns the per-installation salt, creating it on first use.
func (s *Store) IdentitySalt() ([]byte, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`INSERT OR IGNORE INTO settings (key, value, updated_at) VALUES (?, ?, ?)`,
		identitySaltKey, hex.EncodeToString(b), NowMs()); err != nil {
		return nil, err
	}
	var v string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, identitySaltKey).Scan(&v); err != nil {
		return nil, err
	}
	return hex.DecodeString(v)
}
