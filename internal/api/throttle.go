package api

import (
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"
)

// Freno a la fuerza bruta: una IP que falla las credenciales (login de la UI o token) muchas veces
// seguidas queda bloqueada un rato, aunque después acierte. Cuenta por IP de cliente real (la de
// TRACEREPORTS_TRUSTED_PROXIES si hay proxy), así un proxy no bloquea a todos sus usuarios.
const (
	authMaxFailures = 10
	authWindow      = 15 * time.Minute
	authMaxTracked  = 10000 // IPs recordadas a la vez (memoria acotada)
)

type authFailures struct {
	mu    sync.Mutex
	now   func() time.Time
	byIP  map[netip.Addr]*failCount
	limit int
}

type failCount struct {
	n     int
	first time.Time
}

func newAuthFailures() *authFailures {
	return &authFailures{now: time.Now, byIP: map[netip.Addr]*failCount{}, limit: authMaxFailures}
}

// blocked reports for how long ip may not try again (0 = it may).
func (a *authFailures) blocked(ip netip.Addr) time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	f := a.byIP[ip]
	if f == nil {
		return 0
	}
	left := authWindow - a.now().Sub(f.first)
	if left <= 0 {
		delete(a.byIP, ip)
		return 0
	}
	if f.n >= a.limit {
		return left
	}
	return 0
}

func (a *authFailures) fail(ip netip.Addr) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := a.now()
	f := a.byIP[ip]
	if f == nil || now.Sub(f.first) >= authWindow {
		if f == nil && len(a.byIP) >= authMaxTracked {
			for k, v := range a.byIP { // se olvidan las ventanas vencidas; si no alcanza, todas
				if now.Sub(v.first) >= authWindow {
					delete(a.byIP, k)
				}
			}
			if len(a.byIP) >= authMaxTracked {
				a.byIP = map[netip.Addr]*failCount{}
			}
		}
		f = &failCount{first: now}
		a.byIP[ip] = f
	}
	f.n++
}

func (a *authFailures) reset(ip netip.Addr) {
	a.mu.Lock()
	delete(a.byIP, ip)
	a.mu.Unlock()
}

// sentCredentials reports whether the request tried to authenticate (a wrong one counts as a failure).
func sentCredentials(r *http.Request) bool {
	_, _, basic := r.BasicAuth()
	return basic || sentToken(r) != ""
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush keeps the live stream working through the recorder.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// authThrottle answers 429 to an IP that failed the credentials too many times, and counts the 401
// answers to requests that sent credentials. It runs right before the authentication.
func (s *Server) authThrottle(fails *authFailures) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			peer, _ := r.Context().Value(peerKey{}).(string)
			ip := s.Net.ClientIP(peer, r.Header)
			if !sentCredentials(r) {
				next.ServeHTTP(w, r)
				return
			}
			if left := fails.blocked(ip); left > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int(left.Seconds())+1))
				writeError(w, http.StatusTooManyRequests, "too many failed logins from this address: try again later")
				return
			}
			rec := &statusRecorder{ResponseWriter: w}
			next.ServeHTTP(rec, r)
			switch {
			case rec.status == http.StatusUnauthorized:
				fails.fail(ip)
			case rec.status < 400:
				fails.reset(ip)
			}
		})
	}
}
