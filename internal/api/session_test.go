package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// La página de ingreso: una sesión en cookie (HttpOnly, SameSite=Strict, sin token en la base) que da
// los mismos permisos que HTTP Basic, vence sin uso y con el cambio de clave, se cierra de verdad y
// comparte el freno a la fuerza bruta. Un navegador sin login va a /login/ y la UI no recibe el
// WWW-Authenticate que abriría el cuadro del navegador; HTTP Basic y los tokens siguen igual.
type sessionClient struct {
	t      *testing.T
	h      http.Handler
	cookie *http.Cookie
}

func (c *sessionClient) do(method, path, body string, hdr ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.RemoteAddr = "127.0.0.1:50000"
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		req.Header.Set(hdr[i], hdr[i+1])
	}
	if c.cookie != nil {
		req.AddCookie(c.cookie)
	}
	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, req)
	for _, k := range rec.Result().Cookies() {
		if k.Name == sessionCookie {
			if k.MaxAge < 0 {
				c.cookie = nil
			} else {
				c.cookie = k
			}
		}
	}
	return rec
}

func sessionServer(t *testing.T) (*Server, *sessionClient) {
	srv, _ := newTestServer(t)
	srv.Auth = Auth{Token: "tok", UIUser: "qa", UIPass: "pw", ViewerUser: "team", ViewerPass: "ro"}
	return srv, &sessionClient{t: t, h: srv.Router()}
}

func TestSessionLoginGrantsTheUI(t *testing.T) {
	srv, c := sessionServer(t)
	// sin login: la página va al ingreso (con la vuelta), la API de la UI responde 401 sin el cuadro del navegador
	rec := c.do("GET", "/?x=1", "", "Accept", "text/html")
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/login/?next=%2F%3Fx%3D1" {
		t.Fatalf("página sin login: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := c.do("GET", "/api/v1/runs", "", "X-TraceReports-UI", "1"); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") != "" {
		t.Fatalf("la UI sin login: %d %q", rec.Code, rec.Header().Get("WWW-Authenticate"))
	}
	if rec := c.do("GET", "/api/v1/runs", ""); rec.Code != 401 || rec.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("un cliente de la API sigue recibiendo el desafío Basic: %d", rec.Code)
	}
	if rec := c.do("GET", "/api/v1/auth/session", ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"login_required":true`) || strings.Contains(rec.Body.String(), `"logged_in":true`) {
		t.Fatalf("estado sin login: %d %s", rec.Code, rec.Body)
	}

	// clave equivocada: el mismo mensaje para usuario o clave, sin cookie
	for _, body := range []string{`{"user":"qa","password":"nope"}`, `{"user":"nadie","password":"pw"}`} {
		if rec := c.do("POST", "/api/v1/auth/login", body); rec.Code != 401 || !strings.Contains(rec.Body.String(), "wrong user or password") || c.cookie != nil {
			t.Fatalf("login malo: %d %s", rec.Code, rec.Body)
		}
	}
	// bien: cookie HttpOnly + SameSite=Strict, de sesión del navegador (sin recordarme)
	rec = c.do("POST", "/api/v1/auth/login", `{"user":"qa","password":"pw"}`)
	if rec.Code != 200 || c.cookie == nil || !c.cookie.HttpOnly || c.cookie.SameSite != http.SameSiteStrictMode || c.cookie.MaxAge != 0 || c.cookie.Path != "/" {
		t.Fatalf("login: %d %s %+v", rec.Code, rec.Body, c.cookie)
	}
	// el token no queda en la base: solo su hash
	if _, ok, _ := srv.Store.Session(c.cookie.Value); ok {
		t.Fatal("la base guarda el token en claro")
	}
	if _, ok, _ := srv.Store.Session(hashToken(c.cookie.Value)); !ok {
		t.Fatal("la sesión no quedó guardada por su hash")
	}
	// lee la UI y administra como el admin de HTTP Basic (Ajustes); los resultados siguen pidiendo token
	if rec := c.do("GET", "/api/v1/runs", ""); rec.Code != 200 {
		t.Fatalf("leer con la sesión: %d", rec.Code)
	}
	if rec := c.do("GET", "/", "", "Accept", "text/html"); rec.Code != 200 {
		t.Fatalf("la página con sesión: %d", rec.Code)
	}
	if rec := c.do("GET", "/api/v1/auth/session", ""); !strings.Contains(rec.Body.String(), `"logged_in":true`) || !strings.Contains(rec.Body.String(), `"role":"admin"`) {
		t.Fatalf("estado: %s", rec.Body)
	}
	if rec := c.do("POST", "/api/v1/runs", `{"name":"x"}`); rec.Code != 401 {
		t.Fatalf("la sesión no reemplaza el token de los clientes: %d", rec.Code)
	}
	// salir: la cookie se borra y la sesión ya no vale aunque alguien guardara la cookie
	old := c.cookie
	if rec := c.do("POST", "/api/v1/auth/logout", ""); rec.Code != 200 || c.cookie != nil {
		t.Fatalf("logout: %d %+v", rec.Code, c.cookie)
	}
	c.cookie = old
	if rec := c.do("GET", "/api/v1/runs", "", "X-TraceReports-UI", "1"); rec.Code != 401 || !strings.Contains(rec.Body.String(), "session expired") {
		t.Fatalf("la cookie de una sesión cerrada: %d %s", rec.Code, rec.Body)
	}
}

func TestSessionRememberViewerAndExpiry(t *testing.T) {
	t.Setenv("TRACEREPORTS_SESSION_REMEMBER_DAYS", "7")
	_, c := sessionServer(t)
	// recordarme: la cookie dura los días configurados
	if rec := c.do("POST", "/api/v1/auth/login", `{"user":"team","password":"ro","remember":true}`); rec.Code != 200 || c.cookie == nil || c.cookie.MaxAge != 7*24*3600 {
		t.Fatalf("recordarme: %d %+v", rec.Code, c.cookie)
	}
	// solo lectura: lee, no cambia nada
	if rec := c.do("GET", "/api/v1/runs", ""); rec.Code != 200 {
		t.Fatalf("viewer lee: %d", rec.Code)
	}
	if rec := c.do("PUT", "/api/v1/settings", `{}`); rec.Code != http.StatusForbidden {
		t.Fatalf("viewer no administra: %d %s", rec.Code, rec.Body)
	}

	// sin uso más que lo configurado, vence (y la página vuelve al ingreso)
	t.Setenv("TRACEREPORTS_SESSION_IDLE_MIN", "0.0005") // 30 ms
	_, c2 := sessionServer(t)
	if rec := c2.do("POST", "/api/v1/auth/login", `{"user":"qa","password":"pw"}`); rec.Code != 200 {
		t.Fatalf("login: %d", rec.Code)
	}
	time.Sleep(80 * time.Millisecond)
	if rec := c2.do("GET", "/api/v1/runs", "", "X-TraceReports-UI", "1"); rec.Code != 401 || !strings.Contains(rec.Body.String(), "session expired") {
		t.Fatalf("inactiva: %d %s", rec.Code, rec.Body)
	}
}

func TestSessionDiesWhenThePasswordChanges(t *testing.T) {
	srv, c := sessionServer(t)
	if rec := c.do("POST", "/api/v1/auth/login", `{"user":"qa","password":"pw"}`); rec.Code != 200 {
		t.Fatalf("login: %d", rec.Code)
	}
	srv.Auth.UIPass = "otra" // se cambió en la configuración y se reinició
	c.h = srv.Router()
	if rec := c.do("GET", "/api/v1/runs", "", "X-TraceReports-UI", "1"); rec.Code != 401 {
		t.Fatalf("la sesión de la clave anterior sigue: %d", rec.Code)
	}
}

func TestSessionLoginIsThrottled(t *testing.T) {
	_, c := sessionServer(t)
	for i := 0; i < authMaxFailures; i++ {
		c.do("POST", "/api/v1/auth/login", `{"user":"qa","password":"x"}`)
	}
	// bloqueada: ni la clave correcta entra hasta que pase la ventana
	if rec := c.do("POST", "/api/v1/auth/login", `{"user":"qa","password":"pw"}`); rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || c.cookie != nil {
		t.Fatalf("tras %d fallos: %d", authMaxFailures, rec.Code)
	}
}

func TestLoginPageIsPublicAndSafe(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Web = fstest.MapFS{"index.html": {Data: []byte("app")}, "app.js": {Data: []byte("secret ui")},
		"login/index.html": {Data: []byte("login page")}, "login/login.js": {Data: []byte("login js")}}
	srv.Auth = Auth{UIUser: "qa", UIPass: "pw"}
	c := &sessionClient{t: t, h: srv.Router()}
	// la página de ingreso y lo suyo, sin login; el resto de la UI no
	if rec := c.do("GET", "/login/", "", "Accept", "text/html"); rec.Code != 200 || !strings.Contains(rec.Body.String(), "login page") {
		t.Fatalf("/login/: %d %s", rec.Code, rec.Body)
	}
	if rec := c.do("GET", "/login/login.js", ""); rec.Code != 200 {
		t.Fatalf("/login/login.js: %d", rec.Code)
	}
	if rec := c.do("GET", "/app.js", "", "X-TraceReports-UI", "1"); rec.Code != 401 {
		t.Fatalf("la app sin login: %d", rec.Code)
	}
	if rec := c.do("POST", "/login/", "x"); rec.Code == 200 {
		t.Fatal("POST a la página de ingreso no es público")
	}
	// otro sitio no puede iniciar sesión por el navegador (CSRF de login)
	if rec := c.do("POST", "/api/v1/auth/login", `{"user":"qa","password":"pw"}`, "Origin", "https://evil.example", "Sec-Fetch-Site", "cross-site"); rec.Code != http.StatusForbidden || c.cookie != nil {
		t.Fatalf("login desde otro sitio: %d", rec.Code)
	}
	// sin login configurado no hay ingreso (la UI está abierta)
	srv.Auth = Auth{}
	c.h = srv.Router()
	if rec := c.do("POST", "/api/v1/auth/login", `{"user":"qa","password":"pw"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("sin login configurado: %d", rec.Code)
	}
	if rec := c.do("GET", "/api/v1/auth/session", ""); !strings.Contains(rec.Body.String(), `"login_required":false`) {
		t.Fatalf("estado sin login configurado: %s", rec.Body)
	}
}
