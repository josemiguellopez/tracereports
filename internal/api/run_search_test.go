package api

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

func seedSearchRun(t *testing.T, srv *Server, name, project, branch string, started int64, failed bool) int64 {
	t.Helper()
	id, err := srv.Store.CreateRunWithMeta(name, "qa", db.RunMeta{Project: project, Branch: branch, Commit: "abcd"})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Store.SetRunStarted(id, started); err != nil {
		t.Fatal(err)
	}
	if failed {
		tid, err := srv.Store.CreateTestWithMeta(id, "checkout", "smoke, checkout", "", db.TestMeta{Key: "checkout"})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := srv.Store.FinishTestChange(tid, "FAIL", "", ""); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := srv.Store.CloseRun(id, false); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestRunSearchFiltersEscapesAndPaginates(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	a := seedSearchRun(t, srv, "literal 100%_match", "shop", "release/x", 1000, true)
	seedSearchRun(t, srv, "other", "blog", "main", 2000, false)
	c := seedSearchRun(t, srv, "later", "shop", "main", 3000, false)

	q := url.QueryEscape("100%_match")
	rec := call(t, srv, http.MethodGet, "/api/v1/runs/search?q="+q+"&project=shop&status=FAIL&limit=1", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}
	var first struct {
		Items []db.RunSearchItem `json:"items"`
		Next  string             `json:"next_cursor"`
		Total int                `json:"total_estimate"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != a || first.Total != 1 {
		t.Fatalf("unexpected filtered response: %+v", first)
	}

	// A new run after the first page cannot displace older rows from that cursor.
	rec = call(t, srv, http.MethodGet, "/api/v1/runs/search?project=shop&limit=1", "")
	json.Unmarshal(rec.Body.Bytes(), &first)
	if first.Items[0].ID != c || first.Next == "" {
		t.Fatalf("first page: %+v", first)
	}
	seedSearchRun(t, srv, "newest", "shop", "main", 4000, false)
	rec = call(t, srv, http.MethodGet, "/api/v1/runs/search?project=shop&limit=1&cursor="+url.QueryEscape(first.Next), "")
	json.Unmarshal(rec.Body.Bytes(), &first)
	if len(first.Items) != 1 || first.Items[0].ID != a {
		t.Fatalf("cursor duplicated or skipped rows: %+v", first.Items)
	}
}

func TestRunSearchFacetsAndValidation(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	seedSearchRun(t, srv, "one", "shop", "main", 1000, true)
	seedSearchRun(t, srv, "two", "shop", "release", 2000, false)
	rec := call(t, srv, http.MethodGet, "/api/v1/runs/facets?project=shop", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("facets: %d %s", rec.Code, rec.Body)
	}
	var facets map[string][]db.RunFacet
	if err := json.Unmarshal(rec.Body.Bytes(), &facets); err != nil {
		t.Fatal(err)
	}
	if len(facets["branch"]) != 2 || len(facets["tag"]) != 2 || facets["tag"][0].Value != "checkout" {
		t.Fatalf("facets: %#v", facets)
	}
	if rec := call(t, srv, http.MethodGet, "/api/v1/runs/search?tag=checkout", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"items"`) {
		t.Fatalf("exact tag search: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, srv, http.MethodGet, "/api/v1/runs/search?q=checkout", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"one"`) {
		t.Fatalf("indexed text search: %d %s", rec.Code, rec.Body)
	}
	if rec := call(t, srv, http.MethodGet, "/api/v1/runs/search?q="+url.QueryEscape(string(make([]byte, 201))), ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("q limit: %d", rec.Code)
	}
	if rec := call(t, srv, http.MethodGet, "/api/v1/runs/search?limit=101", ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("limit: %d", rec.Code)
	}
}
