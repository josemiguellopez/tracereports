package demo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestPlayground(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: t.TempDir(), Web: fstest.MapFS{"index.html": {Data: []byte("x")}}}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.AI.Wait(ctx)
	}()
	tr := httptest.NewServer(srv.Router())
	defer tr.Close()
	pg := &Playground{Shop: NewShop(), Base: tr.URL, Public: "http://localhost:8080"}
	ps := httptest.NewServer(pg)
	defer ps.Close()
	pg.ShopURL = ps.URL

	post := func(path, body string) (int, map[string]any) {
		res, err := http.Post(ps.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		out := map[string]any{}
		_ = json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}

	// la persona arregló el backend de pedidos: el escenario NOK lo rompe para correr y lo deja arreglado
	if st, _ := post("/__state", `{"bug":"orders","on":false}`); st != 200 || pg.Shop.Bug("orders") {
		t.Fatalf("fix the orders bug: %d", st)
	}
	st, r := post("/__play", `{"id":"go-nok-backend"}`)
	if st != 200 || r["ok"] != true {
		t.Fatalf("play: %d %v", st, r)
	}
	if sent, _ := r["sent"].([]any); len(sent) < 5 {
		t.Fatalf("requests sent: %v", r["sent"])
	}
	if !strings.HasPrefix(r["report"].(string), "http://localhost:8080/#run=") {
		t.Fatalf("report link: %v", r["report"])
	}
	if pg.Shop.Bug("orders") {
		t.Fatal("the play must leave the shop as the person had it")
	}

	if st, _ := post("/__play", `{"id":"nope"}`); st != 404 {
		t.Fatalf("unknown scenario: %d", st)
	}
	// otra página no puede disparar escenarios ni cambiar la tienda sin JSON (CSRF)
	res, _ := http.Post(ps.URL+"/__play", "application/x-www-form-urlencoded", strings.NewReader("id=go-ok"))
	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: %d", res.StatusCode)
	}
	res.Body.Close()

	res, _ = http.Get(ps.URL + "/")
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("page: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	res.Body.Close()
	// lo demás sigue siendo la tienda
	if res, _ := http.Get(ps.URL + "/api/health"); res.StatusCode != 200 {
		t.Fatalf("shop api through the playground: %d", res.StatusCode)
	}
	var cat struct {
		Scenarios []scenarioView `json:"scenarios"`
	}
	res, _ = http.Get(ps.URL + "/__scenarios")
	_ = json.NewDecoder(res.Body).Decode(&cat)
	res.Body.Close()
	for _, s := range cat.Scenarios {
		if s.TitleES == "" || s.WhatES == "" {
			t.Errorf("scenario %s has no Spanish text", s.ID)
		}
	}
}
