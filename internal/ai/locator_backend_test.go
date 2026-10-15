package ai

import (
	"path/filepath"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// An element that never showed up because the backend failed right before is not a broken
// selector: no suggestions (neither on the screen nor in the AI prompt). Without that backend
// error, the same failure is a locator one.
func TestNoLocatorSuggestionsWhenTheBackendFailed(t *testing.T) {
	store, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	dom := `{"url":"https://shop/cart","viewport":{"w":1280,"h":720},"elements":[
		{"tag":"button","testid":"pay","testid_attr":"data-testid","text":"Pay now","visible":true,"w":100,"h":40},
		{"tag":"div","role":"alert","text":"Something went wrong","visible":true,"w":300,"h":40}]}`
	failing := func(status int) *db.Test {
		runID, _ := store.CreateRun("r", "")
		id, _ := store.CreateTest(runID, "pay", "", "")
		_ = store.AddNetwork(id, []db.NetConn{{Method: "POST", URL: "https://shop/api/orders", Status: status, StartedAt: 1000}})
		_, _ = store.AddLog(id, "FAIL", "the order was not confirmed", 2000, "")
		_ = store.SaveDOM(id, dom)
		_, _ = store.FinishTest(id, "FAIL", "TimeoutError: Locator.click: Timeout 5000ms exceeded.\nCall log:\n  - waiting for locator(\"#pay-btn\")", "")
		full, err := store.GetTest(id)
		if err != nil {
			t.Fatal(err)
		}
		return full
	}

	cands, failed, err := LocatorCandidates(store, failing(500))
	if err != nil || len(cands) != 0 || failed != "" {
		t.Fatalf("after a 500: %d suggestions for %q (%v)", len(cands), failed, err)
	}
	cands, failed, err = LocatorCandidates(store, failing(201))
	if err != nil || failed != "#pay-btn" || len(cands) == 0 {
		t.Fatalf("without a backend error: %d suggestions for %q (%v)", len(cands), failed, err)
	}
}
