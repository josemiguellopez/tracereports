package db

import "database/sql"

// Sesiones del login de la UI (la página de ingreso en vez del cuadro de HTTP Basic). Se guarda solo el
// SHA-256 del token: quien lea la base no puede usar una sesión. Sobreviven a un reinicio del servidor
// (un "recordarme" de días no se pierde al reconstruir el contenedor).
const sessionsSchema = `
CREATE TABLE IF NOT EXISTS ui_sessions (
	token_hash TEXT PRIMARY KEY,
	user       TEXT    NOT NULL,
	role       TEXT    NOT NULL,          -- admin | viewer
	created_at INTEGER NOT NULL,
	last_seen  INTEGER NOT NULL,
	expires_at INTEGER NOT NULL,          -- tope absoluto
	idle_ms    INTEGER NOT NULL,          -- sin uso por más que esto, vence
	remember   INTEGER NOT NULL DEFAULT 0,
	cred       TEXT    NOT NULL DEFAULT '' -- huella de usuario+clave: si cambian en la config, la sesión deja de valer
);
`

// UISession is a login session of the web UI.
type UISession struct {
	User      string `json:"user"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"created_at"`
	LastSeen  int64  `json:"last_seen"`
	ExpiresAt int64  `json:"expires_at"`
	IdleMs    int64  `json:"idle_ms"`
	Remember  bool   `json:"remember"`
	Cred      string `json:"-"`
}

// Valid reports whether the session is still in force at now (neither past its cap nor idle too long).
func (u UISession) Valid(now int64) bool {
	return now < u.ExpiresAt && now-u.LastSeen < u.IdleMs
}

// CreateSession stores a new session under the hash of its token, and drops the expired ones.
func (s *Store) CreateSession(tokenHash string, u UISession) error {
	now := NowMs()
	if _, err := s.db.Exec(`DELETE FROM ui_sessions WHERE expires_at <= ? OR last_seen + idle_ms <= ?`, now, now); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO ui_sessions(token_hash, user, role, created_at, last_seen, expires_at, idle_ms, remember, cred)
		VALUES(?,?,?,?,?,?,?,?,?)`, tokenHash, u.User, u.Role, u.CreatedAt, u.LastSeen, u.ExpiresAt, u.IdleMs, u.Remember, u.Cred)
	return err
}

// Session returns the session of a token hash (ok=false if there is none; expiry is the caller's call).
func (s *Store) Session(tokenHash string) (UISession, bool, error) {
	var u UISession
	// por las conexiones de lectura: cada pedido con sesión la lee, y no debe esperar a una escritura larga
	err := s.reader().QueryRow(`SELECT user, role, created_at, last_seen, expires_at, idle_ms, remember, cred FROM ui_sessions WHERE token_hash = ?`, tokenHash).
		Scan(&u.User, &u.Role, &u.CreatedAt, &u.LastSeen, &u.ExpiresAt, &u.IdleMs, &u.Remember, &u.Cred)
	if err == sql.ErrNoRows {
		return u, false, nil
	}
	return u, err == nil, err
}

// TouchSession records activity on a session (sliding the idle window).
func (s *Store) TouchSession(tokenHash string, at int64) error {
	_, err := s.db.Exec(`UPDATE ui_sessions SET last_seen = ? WHERE token_hash = ? AND last_seen < ?`, at, tokenHash, at)
	return err
}

// DeleteSession ends a session (logout, or found expired).
func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.db.Exec(`DELETE FROM ui_sessions WHERE token_hash = ?`, tokenHash)
	return err
}
