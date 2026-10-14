package demo

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/api"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/lab"
)

func TestShop(t *testing.T) {
	shop := NewShop()
	srv := httptest.NewServer(shop)
	defer srv.Close()
	call := func(method, path, auth, body string) (int, string) {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if auth != "" {
			req.Header.Set("Authorization", "Bearer "+auth)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}
	if st, _ := call("POST", "/api/login", "", `{"password":"nope"}`); st != 401 {
		t.Fatalf("wrong password: %d", st)
	}
	st, body := call("POST", "/api/login", "", `{"email":"qa@demo.shop","password":"demo"}`)
	var login struct{ AccessToken string }
	_ = json.Unmarshal([]byte(body), &login)
	if st != 200 || login.AccessToken == "" {
		t.Fatalf("login: %d %s", st, body)
	}
	if st, _ := call("POST", "/api/orders", "", `{}`); st != 401 {
		t.Fatalf("orders without token: %d", st)
	}
	if st, body := call("POST", "/api/orders", login.AccessToken, `{}`); st != 500 || !strings.Contains(body, "pool exhausted") {
		t.Fatalf("orders while broken: %d %s", st, body)
	}
	// la página arregla el backend con su botón
	res, err := http.PostForm(srv.URL+"/__bug", url.Values{"key": {"orders"}, "on": {"0"}})
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(page), `data-bug="orders"`) || !strings.Contains(string(page), "Break it") {
		t.Fatalf("page after the fix: %s", page)
	}
	if st, body := call("POST", "/api/orders", login.AccessToken, `{}`); st != 201 || !strings.Contains(body, "confirmed") {
		t.Fatalf("orders after the fix: %d %s", st, body)
	}
	if len(screen("error")) < 100 || string(screen("login")[1:4]) != "PNG" {
		t.Fatal("screenshots must be PNG images")
	}
}

// The sample data goes through the real API and leaves what the guide promises: runs of every
// language, history, failures grouped into incidents and a call the Lab can resend.
func TestSeed(t *testing.T) {
	t.Setenv("GEMINI_API_KEY", "")
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	srv := &api.Server{Store: store, AI: ai.New(store), ScreenshotsDir: t.TempDir(), Web: fstest.MapFS{"index.html": {Data: []byte("x")}}}
	tr := httptest.NewServer(srv.Router())
	defer tr.Close()
	// los resúmenes de las ejecuciones terminan en segundo plano: antes de cerrar la base
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		srv.AI.Wait(ctx)
	}()
	shop := httptest.NewServer(NewShop())
	defer shop.Close()

	now := time.Now()
	sum, err := Seed(context.Background(), tr.URL, shop.URL, now)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Runs != 2*historyNights+6 || sum.Showcase == 0 || sum.APIRun == 0 || sum.Failed < 7 {
		t.Fatalf("summary = %+v", sum)
	}
	runs, err := store.ListRuns(100)
	if err != nil || len(runs) != sum.Runs {
		t.Fatalf("runs: %v %d", err, len(runs))
	}
	frameworks := map[string]bool{}
	for _, r := range runs {
		frameworks[r.Framework] = true
		if r.StartedAt > now.UnixMilli() {
			t.Fatalf("run %q starts in the future", r.Name)
		}
		if len(r.Commit) != 40 {
			t.Fatalf("run %q commit %q", r.Name, r.Commit)
		}
	}
	for _, fw := range []string{"pytest", "playwright", "junit5", "go"} {
		if !frameworks[fw] {
			t.Errorf("no %s run", fw)
		}
	}
	if runs[0].ID != sum.Showcase {
		t.Fatalf("the showcase run must be the latest one (it opens by itself): %d vs %d", runs[0].ID, sum.Showcase)
	}

	// la llamada que falló se puede reenviar desde el Lab: login grabado y contraseña enmascarada
	detail, err := store.GetRunDetail(sum.Showcase)
	if err != nil {
		t.Fatal(err)
	}
	var checkout *db.Test
	for i := range detail.Tests {
		if detail.Tests[i].Name == "test_checkout_pays_order" {
			checkout = &detail.Tests[i]
		}
	}
	if checkout == nil || checkout.Status != "FAIL" {
		t.Fatalf("checkout test: %+v", checkout)
	}
	conns, _ := store.ListNetwork(checkout.ID)
	var failed *db.NetConn
	for i := range conns {
		if conns[i].Status == 500 {
			failed = &conns[i]
		}
		if strings.Contains(conns[i].PostData, ShopPassword+`"`) {
			t.Fatalf("the password must be masked: %s", conns[i].PostData)
		}
	}
	if failed == nil || !strings.HasPrefix(failed.URL, shop.URL) {
		t.Fatalf("failed call: %+v", failed)
	}
	plan, err := lab.Prepare(conns, failed.ID)
	if err != nil || plan.Login == nil || strings.Join(plan.Needs(nil), ",") != "password" {
		t.Fatalf("plan: %v %+v", err, plan)
	}
	res, err := lab.Run(context.Background(), plan, lab.Options{Values: map[string]string{"password": ShopPassword}})
	if err != nil || res.Verdict != lab.VerdictReproduced {
		t.Fatalf("the Lab must reproduce it: %v %+v", err, res)
	}
	// capturas, consola y el DOM del selector que cambió
	full, _ := store.GetTest(checkout.ID)
	shots := 0
	for _, l := range full.Logs {
		if l.Screenshot != "" {
			shots++
		}
	}
	if shots == 0 || len(full.Console) == 0 {
		t.Fatalf("checkout evidence: %d screenshots, %d console entries", shots, len(full.Console))
	}
}
