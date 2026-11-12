package api

import (
	"sync"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// Cada pedido del navegador con sesión (también un .css o una fuente) leía su sesión en la base. Ahora se
// lee por las conexiones de lectura y se guarda en memoria unos segundos: una escritura larga (medir las
// páginas, compactar, la retención) no frena la interfaz. El pulso de actividad tampoco espera a la
// conexión que escribe: si está ocupada, se deja para el próximo pedido.

// sessionCacheFor: how long a session read from the database is reused.
const sessionCacheFor = 15 * time.Second

type cachedSession struct {
	sess db.UISession
	ok   bool
	at   time.Time
}

// sessionCache keeps the sessions read lately, by token hash (the zero value is ready to use).
type sessionCache struct {
	mu    sync.Mutex
	items map[string]cachedSession
	touch chan struct{} // un pulso de actividad a la vez
}

func (c *sessionCache) get(h string, now time.Time) (cachedSession, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[h]
	if !ok || now.Sub(e.at) > sessionCacheFor {
		return cachedSession{}, false
	}
	return e, true
}

func (c *sessionCache) put(h string, e cachedSession) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.items == nil {
		c.items = map[string]cachedSession{}
	}
	if len(c.items) > 10_000 { // muchas cookies distintas: se empieza de nuevo
		c.items = map[string]cachedSession{}
	}
	c.items[h] = e
}

func (c *sessionCache) forget(h string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.items, h)
}

func (c *sessionCache) setLastSeen(h string, at int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.items[h]; ok {
		e.sess.LastSeen = at
		c.items[h] = e
	}
}

// session returns the session of a token hash, from memory when it was read in the last seconds.
func (s *Server) session(h string) (db.UISession, bool, error) {
	now := time.Now()
	if e, ok := s.sessions.get(h, now); ok {
		return e.sess, e.ok, nil
	}
	sess, ok, err := s.Store.Session(h)
	if err != nil {
		return sess, ok, err
	}
	s.sessions.put(h, cachedSession{sess: sess, ok: ok, at: now})
	return sess, ok, nil
}

// touchSession slides a session's idle window without making the request wait: one at a time, in the
// background; when one is already going (the writer is busy) this one is skipped, the next request tries.
func (s *Server) touchSession(h string, at int64) {
	s.sessions.mu.Lock()
	if s.sessions.touch == nil {
		s.sessions.touch = make(chan struct{}, 1)
	}
	sem := s.sessions.touch
	s.sessions.mu.Unlock()
	select {
	case sem <- struct{}{}:
	default:
		return
	}
	s.sessions.setLastSeen(h, at)
	go func() {
		defer func() { <-sem }()
		_ = s.Store.TouchSession(h, at)
	}()
}

// dropSession deletes a session (logout, expired) from the database and from memory.
func (s *Server) dropSession(h string) {
	s.sessions.forget(h)
	_ = s.Store.DeleteSession(h)
}
