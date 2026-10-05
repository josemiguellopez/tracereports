package redact

import (
	"strings"
	"testing"
)

func TestTextMasksSecretsAndKeepsTheRest(t *testing.T) {
	p := Default()
	cases := []struct{ in, want string }{
		{`{"user":"ana","password":"hunter2","age":31,"pin":1234}`, `{"user":"ana","password":"<masked>","age":31,"pin":"<masked>"}`},
		{`{"access_token": "abc.def", "token_type": "bearer", "expires_in": 3600}`, `{"access_token": "<masked>", "token_type": "bearer", "expires_in": 3600}`},
		{`{"userPassword":"x","csrf_token":"y","next":"/home"}`, `{"userPassword":"<masked>","csrf_token":"<masked>","next":"/home"}`},
		{`https://api.test/v1/orders?id=42&api_key=SECRET1&page=2`, `https://api.test/v1/orders?id=42&api_key=<masked>&page=2`},
		{`username=ana&password=hunter2&remember=1`, `username=ana&password=<masked>&remember=1`},
		{`https://bob:s3cret@db.internal/x`, `https://<masked>@db.internal/x`},
		{`Authorization: Bearer abcdefghijklmnop`, `Authorization: <masked>`},
		{`call failed with Bearer abcdefghijklmnop`, `call failed with Bearer <masked>`},
		{`jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.c2lnbmF0dXJlMTIz end`, `jwt <masked> end`},
		{`AssertionError: expected 3 items, got 2`, `AssertionError: expected 3 items, got 2`},
		{`{"token_type":"bearer","password":""}`, `{"token_type":"bearer","password":""}`},
	}
	for _, c := range cases {
		if got := p.Text(c.in); got != c.want {
			t.Errorf("Text(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
	if got := p.Text(p.Text(cases[0].in)); got != cases[0].want {
		t.Errorf("masking must be idempotent: %q", got)
	}
}

func TestHeadersAndConfiguration(t *testing.T) {
	t.Setenv("TRACEREPORTS_REDACT_HEADERS", "X-Tenant-Key")
	t.Setenv("TRACEREPORTS_REDACT_KEYS", "rut")
	t.Setenv("TRACEREPORTS_REDACT_PATTERNS", `\b\d{7,8}-[\dkK]\b`)
	p := FromEnv()
	h := p.Headers(map[string]string{"Cookie": "sid=1", "X-Tenant-Key": "k", "Content-Type": "application/json", "Referer": "https://x/?token=abc"})
	if h["Cookie"] != Mask || h["X-Tenant-Key"] != Mask || h["Content-Type"] != "application/json" || h["Referer"] != "https://x/?token=<masked>" {
		t.Fatalf("headers: %v", h)
	}
	if got := p.Text(`{"rut":"12345678-9","nota":"cliente 7654321-K"}`); strings.Contains(got, "12345678") || strings.Contains(got, "7654321") {
		t.Fatalf("configured keys and patterns: %s", got)
	}
	t.Setenv("TRACEREPORTS_REDACT", "off")
	if got := FromEnv().Text(`password=x`); got != `password=x` {
		t.Fatalf("disabled policy must not mask: %s", got)
	}
}

func TestStructuredAndEncodedSecrets(t *testing.T) {
	p := Default()
	cases := []struct{ in, want string }{
		{`{"token":["SECRET_AUDIT"],"page":2}`, `{"token":"<masked>","page":2}`},
		{`{"password":{"value":"SECRET_AUDIT","hint":"x"},"user":"ana"}`, `{"password":"<masked>","user":"ana"}`},
		{"{\n  \"auth\": {\n    \"jwt\": \"a]b}c\"\n  },\n  \"ok\": true\n}", "{\n  \"auth\": \"<masked>\",\n  \"ok\": true\n}"},
		{`https://example.test/?%74oken=SECRET_AUDIT&q=1`, `https://example.test/?%74oken=<masked>&q=1`},
		{`api%5Fkey=SECRET_AUDIT&x=1`, `api%5Fkey=<masked>&x=1`},
		{"{\"\\u0074oken\":\"SECRET_AUDIT\"}", "{\"\\u0074oken\":\"<masked>\"}"}, // clave con escape JSON
		{`{"remember_password":true,"name":"ana"}`, `{"remember_password":"<masked>","name":"ana"}`},
		{`{"session":{"id":"SECRET_AUDIT"`, `{"session":"<masked>"`}, // cuerpo truncado
		{`{"items":[{"id":1,"name":"a"}],"total":1}`, `{"items":[{"id":1,"name":"a"}],"total":1}`},
	}
	for _, c := range cases {
		if got := p.Text(c.in); got != c.want {
			t.Errorf("Text(%q)\n got  %q\n want %q", c.in, got, c.want)
		}
	}
}
