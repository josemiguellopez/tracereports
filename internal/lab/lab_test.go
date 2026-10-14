package lab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/redact"
)

const password = "s3cret!"

// masked applies the server's masking policy to a captured call, as the API does before storing it.
func masked(c db.NetConn) db.NetConn {
	p := redact.Default()
	c.URL = p.Text(c.URL)
	c.RequestHeaders = p.Headers(c.RequestHeaders)
	c.ResponseHeaders = p.Headers(c.ResponseHeaders)
	c.PostData = p.Text(c.PostData)
	c.ResponseBody = p.Text(c.ResponseBody)
	return c
}

// ---------- tienda con login JWT ----------

type jwtShop struct {
	*httptest.Server
	fail   atomic.Bool
	tokens sync.Map
	hits   atomic.Int32
}

func newJWTShop(t *testing.T) *jwtShop {
	s := &jwtShop{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/login":
			var in struct{ Email, Password string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			if in.Password != password {
				w.WriteHeader(401)
				_, _ = io.WriteString(w, `{"error":"bad credentials"}`)
				return
			}
			tok := fmt.Sprintf("eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.sig%d", s.hits.Load())
			s.tokens.Store(tok, true)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]string{"accessToken": tok}})
		case "/api/orders":
			if _, ok := s.tokens.Load(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")); !ok {
				w.WriteHeader(401)
				_, _ = io.WriteString(w, `{"error":"token expired"}`)
				return
			}
			if s.fail.Load() {
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `{"error":"orders db: connection pool exhausted"}`)
				return
			}
			_, _ = io.WriteString(w, `{"orders":[{"id":"A-1"}]}`)
		default:
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

// recording of the test against the shop: the login, then the call that failed with a 500.
func (s *jwtShop) recording() []db.NetConn {
	hdr := map[string]string{"date": "Wed, 14 Oct 2026 21:00:00 GMT", "server": "shop", "content-type": "application/json"}
	return []db.NetConn{
		masked(db.NetConn{ID: 1, Seq: 1, Method: "GET", URL: s.URL + "/", Status: 200, ResourceType: "document", ResponseHeaders: hdr}),
		masked(db.NetConn{ID: 2, Seq: 2, Method: "POST", URL: s.URL + "/api/login", Status: 200, ResourceType: "fetch",
			RequestHeaders: map[string]string{"content-type": "application/json"},
			PostData:       `{"email":"qa@shop.test","password":"` + password + `"}`,
			ResponseBody:   `{"data":{"accessToken":"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.old"}}`, ResponseHeaders: hdr}),
		masked(db.NetConn{ID: 3, Seq: 3, Method: "GET", URL: s.URL + "/api/orders?status=open", Status: 500, ResourceType: "fetch",
			RequestHeaders: map[string]string{"authorization": "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJxYSJ9.old"},
			ResponseBody:   `{"error":"orders db: connection pool exhausted"}`, ResponseHeaders: hdr}),
	}
}

func varsByName(p *Plan) map[string]Var {
	out := map[string]Var{}
	for _, v := range p.Vars {
		out[v.Name] = v
	}
	return out
}

func TestPrepareJWT(t *testing.T) {
	shop := newJWTShop(t)
	p, err := Prepare(shop.recording(), 3)
	if err != nil {
		t.Fatal(err)
	}
	if p.Login == nil || p.Login.ConnID != 2 || strings.Join(p.Login.TokenPath, ".") != "data.accessToken" {
		t.Fatalf("login step = %+v", p.Login)
	}
	v := varsByName(p)
	if v["authorization"].Kind != KindSession || v["password"].Kind != KindSecret {
		t.Fatalf("vars = %+v", p.Vars)
	}
	if p.Target.Headers["authorization"] != "{{authorization}}" || !strings.Contains(p.Login.Request.Body, `"password":"{{password}}"`) {
		t.Fatalf("placeholders: target %v login %q", p.Target.Headers, p.Login.Request.Body)
	}
	if p.Original.Status != 500 || p.Original.Simulated {
		t.Fatalf("original = %+v", p.Original)
	}
}

func TestRunJWTReproducedThenFixed(t *testing.T) {
	shop := newJWTShop(t)
	shop.fail.Store(true)
	p, _ := Prepare(shop.recording(), 3)

	res, err := Run(context.Background(), p, Options{Values: map[string]string{"password": password}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictReproduced || res.Response.Status != 500 {
		t.Fatalf("verdict %s status %d, steps %+v", res.Verdict, res.Response.Status, res.Steps)
	}
	if len(res.Steps) != 2 || res.Steps[0].Role != "login" || res.Steps[0].Note != "token: data.accessToken" {
		t.Fatalf("steps = %+v", res.Steps)
	}
	// lo que se devuelve nunca lleva los valores: solo las variables
	if b, _ := json.Marshal(res); strings.Contains(string(b), password) {
		t.Fatalf("the result leaks the password: %s", b)
	}

	shop.fail.Store(false) // el dev arregla el backend
	res, err = Run(context.Background(), p, Options{Values: map[string]string{"password": password}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictFixed || res.Response.Status != 200 {
		t.Fatalf("verdict %s status %d", res.Verdict, res.Response.Status)
	}
}

func TestRunMissingAndWrongPassword(t *testing.T) {
	shop := newJWTShop(t)
	p, _ := Prepare(shop.recording(), 3)
	_, err := Run(context.Background(), p, Options{})
	var miss *ErrMissing
	if !errors.As(err, &miss) || strings.Join(miss.Names, ",") != "password" {
		t.Fatalf("err = %v", err)
	}
	if shop.hits.Load() != 0 {
		t.Fatal("nothing must be sent when a value is missing")
	}
	res, err := Run(context.Background(), p, Options{Values: map[string]string{"password": "nope"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictSession || res.Steps[0].Status != 401 || res.Steps[0].Note != "token not found in the response" {
		t.Fatalf("verdict %s steps %+v", res.Verdict, res.Steps)
	}
}

func TestRunManualAuthorizationWins(t *testing.T) {
	shop := newJWTShop(t)
	shop.fail.Store(true)
	p, _ := Prepare(shop.recording(), 3)
	// sin login automático: la persona pega su propio header
	shop.tokens.Store("manual-token", true)
	p.Login = nil
	res, err := Run(context.Background(), p, Options{Values: map[string]string{"authorization": "Bearer manual-token"}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictReproduced {
		t.Fatalf("verdict %s %+v", res.Verdict, res.Response)
	}
}

// ---------- app con sesión por cookie y CSRF (como OrangeHRM) ----------

type formApp struct {
	*httptest.Server
	fail     atomic.Bool
	sessions sync.Map // sid -> "anon:<csrf>" | "auth"
	n        atomic.Int32
}

func newFormApp(t *testing.T) *formApp {
	a := &formApp{}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Server", "formapp")
		sid := ""
		if c, err := r.Cookie("sid"); err == nil {
			sid = c.Value
		}
		state, _ := a.sessions.Load(sid)
		switch r.URL.Path {
		case "/auth/login":
			sid = fmt.Sprintf("s%d", a.n.Add(1))
			csrf := fmt.Sprintf("csrf-%d-abcdefghijklmnop", a.n.Load())
			a.sessions.Store(sid, "anon:"+csrf)
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: sid, Path: "/"})
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><auth-login :token="&quot;%s&quot;"></auth-login></html>`, csrf)
		case "/auth/validate":
			_ = r.ParseForm()
			if a.fail.Load() {
				w.WriteHeader(500)
				return
			}
			if state != "anon:"+r.PostForm.Get("_token") || r.PostForm.Get("password") != password {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}
			a.sessions.Store(sid, "auth")
			http.Redirect(w, r, "/dashboard/index", http.StatusFound)
		case "/api/employees":
			w.Header().Set("Content-Type", "application/json")
			if state != "auth" {
				w.WriteHeader(401)
				_, _ = io.WriteString(w, `{"error":{"status":401,"message":"Session expired"}}`)
				return
			}
			if a.fail.Load() {
				w.WriteHeader(500)
				return
			}
			_, _ = io.WriteString(w, `{"data":[{"empNumber":7}]}`)
		}
	}))
	t.Cleanup(a.Close)
	return a
}

func (a *formApp) recording() []db.NetConn {
	page := a.URL + "/auth/login"
	return []db.NetConn{
		masked(db.NetConn{ID: 10, Seq: 1, Method: "GET", URL: page, Status: 200, ResourceType: "document",
			ResponseHeaders: map[string]string{"date": "x", "server": "formapp"}}),
		masked(db.NetConn{ID: 11, Seq: 2, Method: "POST", URL: a.URL + "/auth/validate", Status: 302, ResourceType: "document",
			RequestHeaders:  map[string]string{"cookie": "sid=s0", "referer": page, "content-type": "application/x-www-form-urlencoded"},
			PostData:        "_token=csrf-old-abcdefghijklmnop&username=Admin&password=" + password,
			ResponseHeaders: map[string]string{"date": "x", "server": "formapp", "location": "/dashboard/index"}}),
		masked(db.NetConn{ID: 12, Seq: 3, Method: "GET", URL: a.URL + "/api/employees?limit=50", ResourceType: "xhr",
			Failed: true, ErrorText: "net::ERR_TIMED_OUT", RequestHeaders: map[string]string{"cookie": "sid=s0"}}),
		// el 500 que fabricó el test con route.fulfill: sin date ni server
		masked(db.NetConn{ID: 13, Seq: 4, Method: "POST", URL: a.URL + "/auth/validate", Status: 500, ResourceType: "document",
			RequestHeaders:  map[string]string{"cookie": "sid=s0", "referer": page, "content-type": "application/x-www-form-urlencoded"},
			PostData:        "_token=csrf-old-abcdefghijklmnop&username=Admin&password=" + password,
			ResponseHeaders: map[string]string{"content-type": "application/json"}, ResponseBody: `{"error":"simulated"}`}),
	}
}

func TestPrepareFormSession(t *testing.T) {
	app := newFormApp(t)
	p, err := Prepare(app.recording(), 12)
	if err != nil {
		t.Fatal(err)
	}
	if p.Login == nil || p.Login.ConnID != 11 || p.Login.TokenPath != nil {
		t.Fatalf("login = %+v", p.Login)
	}
	if p.CSRF == nil || p.CSRF.PageURL != app.URL+"/auth/login" || strings.Join(p.CSRF.Fields, ",") != "_token" {
		t.Fatalf("csrf = %+v", p.CSRF)
	}
	v := varsByName(p)
	if v["cookie"].Kind != KindSession || v["_token"].Kind != KindCSRF || v["password"].Kind != KindSecret {
		t.Fatalf("vars = %+v", p.Vars)
	}
	if !p.Original.Failed {
		t.Fatalf("original = %+v", p.Original)
	}
}

func TestRunFormSession(t *testing.T) {
	app := newFormApp(t)
	p, _ := Prepare(app.recording(), 12)
	res, err := Run(context.Background(), p, Options{Values: map[string]string{"password": password}})
	if err != nil {
		t.Fatal(err)
	}
	// el timeout original ya no ocurre: el backend responde
	if res.Verdict != VerdictFixed || res.Response.Status != 200 {
		t.Fatalf("verdict %s %+v steps %+v", res.Verdict, res.Response, res.Steps)
	}
	roles := []string{}
	for _, s := range res.Steps {
		roles = append(roles, s.Role)
	}
	if strings.Join(roles, ",") != "csrf,login,target" || res.Steps[0].Note != "csrf: _token" || res.Steps[1].Location != "/dashboard/index" {
		t.Fatalf("steps = %+v", res.Steps)
	}

	app.fail.Store(true)
	res, _ = Run(context.Background(), p, Options{Values: map[string]string{"password": password}})
	if res.Verdict != VerdictDifferent && res.Verdict != VerdictSession {
		// el login también falla con 500: la sesión no se abre y la llamada responde 401
		t.Fatalf("verdict with a broken login: %s", res.Verdict)
	}
}

func TestRunSimulatedFailureDoesNotReproduce(t *testing.T) {
	app := newFormApp(t)
	p, _ := Prepare(app.recording(), 13)
	if !p.Original.Simulated || p.Login != nil || p.CSRF == nil {
		t.Fatalf("plan = %+v", p)
	}
	res, err := Run(context.Background(), p, Options{Values: map[string]string{"password": password}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Verdict != VerdictFixed || res.Response.Status != 302 {
		t.Fatalf("verdict %s %+v", res.Verdict, res.Steps)
	}
	res, _ = Run(context.Background(), p, Options{Values: map[string]string{"password": "wrong"}})
	if res.Verdict != VerdictSession {
		t.Fatalf("a redirect back to the login must be a session problem, got %s", res.Verdict)
	}
}

func TestRunBaseURLAndEditedTarget(t *testing.T) {
	recorded := newJWTShop(t) // dónde se grabó (staging)
	local := newJWTShop(t)    // donde el dev prueba su arreglo
	local.fail.Store(false)
	p, _ := Prepare(recorded.recording(), 3)
	edited := p.Target
	edited.URL = strings.Replace(edited.URL, "status=open", "status=closed", 1)
	res, err := Run(context.Background(), p, Options{Values: map[string]string{"password": password}, BaseURL: local.URL, Target: &edited})
	if err != nil {
		t.Fatal(err)
	}
	if recorded.hits.Load() != 0 || local.hits.Load() != 2 {
		t.Fatalf("hits recorded=%d local=%d", recorded.hits.Load(), local.hits.Load())
	}
	if res.Verdict != VerdictFixed || !strings.HasPrefix(res.Sent.URL, local.URL) || !strings.Contains(res.Sent.URL, "status=closed") {
		t.Fatalf("verdict %s sent %s", res.Verdict, res.Sent.URL)
	}
}

func TestRunAllowBlocksBeforeSending(t *testing.T) {
	shop := newJWTShop(t)
	p, _ := Prepare(shop.recording(), 3)
	_, err := Run(context.Background(), p, Options{Values: map[string]string{"password": password},
		Allow: func(u *url.URL) error { return fmt.Errorf("host %s not allowed", u.Host) }})
	if !errors.Is(err, ErrBlocked) || shop.hits.Load() != 0 {
		t.Fatalf("err %v hits %d", err, shop.hits.Load())
	}
	if _, err := Run(context.Background(), p, Options{Values: map[string]string{"password": password}, BaseURL: "file:///etc"}); err == nil {
		t.Fatal("a non-http base URL must be rejected")
	}
}

func TestSafeTransportBlocksLinkLocal(t *testing.T) {
	p := &Plan{Target: Request{Method: "GET", URL: "http://169.254.169.254/latest/meta-data/", Headers: map[string]string{}}}
	res, err := Run(context.Background(), p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Response.Status != 0 || !strings.Contains(res.Response.Error, "link-local") {
		t.Fatalf("response = %+v", res.Response)
	}
}

func TestRedirectsAreNotFollowed(t *testing.T) {
	var followed atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/next" {
			followed.Store(true)
		}
		http.Redirect(w, r, "/next", http.StatusFound)
	}))
	defer srv.Close()
	p := &Plan{Target: Request{Method: "GET", URL: srv.URL + "/start", Headers: map[string]string{}}, Original: Original{Status: 500}}
	res, _ := Run(context.Background(), p, Options{})
	if followed.Load() || res.Response.Status != 302 || res.Steps[0].Location != "/next" {
		t.Fatalf("followed=%v res=%+v", followed.Load(), res)
	}
}

func TestCSRFValue(t *testing.T) {
	cases := map[string]string{
		`<input type="hidden" name="_token" value="abc123">`:         "abc123",
		`<input value="v-2" type="hidden" name="_token">`:            "v-2",
		`<meta name="csrf-token" content="meta-tok">`:                "meta-tok",
		`<auth-login :token="&quot;e1.f2-abcdefghijklmn&quot;"></x>`: "e1.f2-abcdefghijklmn",
		`<form><input name="other" value="x"></form>`:                "",
	}
	for page, want := range cases {
		if got := csrfValue(page, "_token"); got != want {
			t.Errorf("csrfValue(%q) = %q, want %q", page, got, want)
		}
	}
}

func TestVerdict(t *testing.T) {
	loc := func(l string) map[string]string { return map[string]string{"location": l} }
	cases := []struct {
		o    Original
		now  Response
		want string
	}{
		{Original{Status: 500}, Response{Status: 500}, VerdictReproduced},
		{Original{Status: 500}, Response{Status: 200}, VerdictFixed},
		{Original{Failed: true}, Response{Status: 0, Error: "timeout"}, VerdictReproduced},
		{Original{Failed: true}, Response{Status: 200}, VerdictFixed},
		{Original{Status: 500}, Response{Status: 401}, VerdictSession},
		{Original{Status: 500}, Response{Status: 302, Headers: loc("/web/index.php/auth/login")}, VerdictSession},
		{Original{Status: 500}, Response{Status: 302, Headers: loc("/dashboard")}, VerdictFixed},
		{Original{Status: 500}, Response{Status: 404}, VerdictDifferent},
		{Original{Status: 200}, Response{Status: 0, Error: "refused"}, VerdictNetwork},
		{Original{Status: 200}, Response{Status: 200}, VerdictOK},
		{Original{Status: 401}, Response{Status: 401}, VerdictReproduced},
	}
	for _, c := range cases {
		if got := verdict(c.o, c.now); got != c.want {
			t.Errorf("verdict(%+v, %d) = %s, want %s", c.o, c.now.Status, got, c.want)
		}
	}
}

func TestPrepareUnknownCall(t *testing.T) {
	if _, err := Prepare(nil, 99); err == nil {
		t.Fatal("expected an error")
	}
}

// A CSRF token recorded in plain sight (an older client, or TRACEREPORTS_REDACT=off) belonged to
// the recorded session: it is read live too, or kept as is when there is no page to read it from.
func TestStaleCSRFIsReadLive(t *testing.T) {
	app := newFormApp(t)
	conns := app.recording()
	for i := range conns {
		conns[i].PostData = strings.Replace(conns[i].PostData, "_token=<masked>", "_token=csrf-old-abcdefghijklmnop", 1)
	}
	p, err := Prepare(conns, 12)
	if err != nil {
		t.Fatal(err)
	}
	if p.CSRF == nil || strings.Join(p.CSRF.Fields, ",") != "_token" || varsByName(p)["_token"].Kind != KindCSRF {
		t.Fatalf("plan = %+v", p)
	}
	res, err := Run(context.Background(), p, Options{Values: map[string]string{"password": password}})
	if err != nil || res.Verdict != VerdictFixed {
		t.Fatalf("verdict %v err %v steps %+v", res, err, res.Steps)
	}

	// sin la página de login en la grabación ni Referer: el valor grabado vuelve a su lugar
	conns = conns[1:]
	delete(conns[0].RequestHeaders, "referer")
	p, _ = Prepare(conns, 12)
	if p.CSRF != nil || !strings.Contains(p.Login.Request.Body, "_token=csrf-old-abcdefghijklmnop") {
		t.Fatalf("login body = %q csrf = %+v", p.Login.Request.Body, p.CSRF)
	}
	if _, ok := varsByName(p)["_token"]; ok {
		t.Fatal("_token must not be a variable without a page")
	}
}

// A value is encoded for where it goes: inside a JSON string, in a form body (even without a
// Content-Type) and in the query string.
func TestValuesAreEncodedForTheirPlace(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.URL.RawQuery+" | "+string(b))
	}))
	defer srv.Close()
	tricky := `a&b=c "d"\`
	for _, body := range []string{`{"password":"{{password}}"}`, `user=qa&password={{password}}`} {
		p := &Plan{Target: Request{Method: "POST", URL: srv.URL + "/x?k={{password}}", Headers: map[string]string{}, Body: body},
			Vars: []Var{{Name: "password", Kind: KindSecret}}}
		if _, err := Run(context.Background(), p, Options{Values: map[string]string{"password": tricky}}); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{
		`k=a%26b%3Dc+%22d%22%5C | {"password":"a&b=c \"d\"\\"}`,
		`k=a%26b%3Dc+%22d%22%5C | user=qa&password=a%26b%3Dc+%22d%22%5C`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("got\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}
