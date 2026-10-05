package db

import (
	"path/filepath"
	"testing"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// addRun creates a finished run with tests name -> status.
func addRun(t *testing.T, s *Store, tests map[string]string) int64 {
	t.Helper()
	runID, _ := s.CreateRun("suite", "")
	for name, st := range tests {
		id, _ := s.CreateTest(runID, name, "", "")
		if _, err := s.FinishTest(id, st, "", ""); err != nil {
			t.Fatal(err)
		}
	}
	s.FinishRun(runID)
	return runID
}

func TestIsFlaky(t *testing.T) {
	cases := map[string]struct {
		in   []string
		want bool
	}{
		"always passes":        {[]string{"PASS", "PASS", "PASS"}, false},
		"broke once and stays": {[]string{"FAIL", "FAIL", "PASS", "PASS"}, false},
		"alternates":           {[]string{"FAIL", "PASS", "FAIL", "PASS"}, true},
		"fail-pass-fail":       {[]string{"FAIL", "PASS", "FAIL"}, true},
		"skips ignored":        {[]string{"FAIL", "SKIP", "FAIL", "PASS"}, false},
	}
	for name, c := range cases {
		if got := IsFlaky(c.in); got != c.want {
			t.Errorf("%s: IsFlaky(%v) = %v", name, c.in, got)
		}
	}
}

func TestFlakyFlagAndCompare(t *testing.T) {
	s := openTestStore(t)
	addRun(t, s, map[string]string{"login": "PASS", "search": "PASS", "logout": "FAIL"})
	addRun(t, s, map[string]string{"login": "FAIL", "search": "PASS", "logout": "FAIL"})
	last := addRun(t, s, map[string]string{"login": "PASS", "search": "FAIL", "logout": "PASS", "export": "PASS"})

	d, err := s.GetRunDetail(last)
	if err != nil {
		t.Fatal(err)
	}
	flaky := map[string]bool{}
	for _, tt := range d.Tests {
		flaky[tt.Name] = tt.Flaky
	}
	if !flaky["login"] || flaky["search"] || flaky["logout"] {
		t.Errorf("flaky flags: %v (want only login)", flaky)
	}

	hist, _ := s.TestHistory(NameKey("login"), last, 10)
	if len(hist) != 3 || hist[0].Status != "PASS" || hist[1].Status != "FAIL" {
		t.Errorf("history: %+v", hist)
	}

	cmp, err := s.CompareRuns(last, 0)
	if err != nil {
		t.Fatal(err)
	}
	names := func(items []CompareItem) []string {
		var out []string
		for _, i := range items {
			out = append(out, i.Name)
		}
		return out
	}
	if cmp.BaseRun == nil || cmp.BaseRun.ID != last-1 {
		t.Fatalf("base run: %+v", cmp.BaseRun)
	}
	if got := names(cmp.NewFailures); len(got) != 1 || got[0] != "search" {
		t.Errorf("new failures: %v", got)
	}
	if got := names(cmp.Fixed); len(got) != 2 {
		t.Errorf("fixed (login, logout): %v", got)
	}
	if got := names(cmp.NewTests); len(got) != 1 || got[0] != "export" {
		t.Errorf("new tests: %v", got)
	}
}

func TestNormalizeEndpointAndRanking(t *testing.T) {
	host, path := NormalizeEndpoint("https://app.test/api/v2/users/42/orders/9f8e7d6c-1a2b-4c3d-8e9f-001122334455?x=1")
	if host != "app.test" || path != "/api/v2/users/:id/orders/:id" {
		t.Errorf("normalize: %s %s", host, path)
	}

	s := openTestStore(t)
	runID, _ := s.CreateRun("r", "")
	testID, _ := s.CreateTest(runID, "t", "", "")
	d := func(ms int64) *int64 { return &ms }
	s.AddNetwork(testID, []NetConn{
		{Method: "GET", URL: "https://app.test/api/users/1", Status: 200, ResourceType: "xhr", DurationMs: d(100)},
		{Method: "GET", URL: "https://app.test/api/users/2", Status: 200, ResourceType: "xhr", DurationMs: d(300)},
		{Method: "POST", URL: "https://app.test/api/login", Status: 500, ResourceType: "fetch", DurationMs: d(50)},
		{Method: "GET", URL: "https://app.test/static/app.js", Status: 200, ResourceType: "script", DurationMs: d(900)},
	})
	eps, err := s.RunEndpoints(runID, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 {
		t.Fatalf("want 2 endpoints (script ignored), got %+v", eps)
	}
	if eps[0].Path != "/api/login" || eps[0].Errors != 1 {
		t.Errorf("endpoints with errors must come first: %+v", eps[0])
	}
	if eps[1].Path != "/api/users/:id" || eps[1].Count != 2 || eps[1].AvgMs != 200 || eps[1].MaxMs != 300 {
		t.Errorf("aggregation: %+v", eps[1])
	}
}

func TestFlakyScoreAndNetworkDrift(t *testing.T) {
	s := openTestStore(t)
	d := func(ms int64) *int64 { return &ms }
	// 4 ejecuciones del mismo test: alterna PASS/FAIL (flaky) y la última es mucho más lenta.
	var lastRun, lastTest int64
	for i, st := range []string{"PASS", "FAIL", "PASS", "FAIL"} {
		runID, _ := s.CreateRun("suite", "")
		id, _ := s.CreateTest(runID, "login", "", "")
		dur := int64(400)
		if i == 3 {
			dur = 1200
		}
		s.AddNetwork(id, []NetConn{
			{Method: "POST", URL: "https://app/api/login", Status: 200, ResourceType: "fetch", DurationMs: d(dur)},
			{Method: "GET", URL: "https://app/api/users/7", Status: 200, ResourceType: "xhr", DurationMs: d(100)},
		})
		s.FinishTest(id, st, "", "")
		s.FinishRun(runID)
		lastRun, lastTest = runID, id
	}
	det, err := s.GetRunDetail(lastRun)
	if err != nil {
		t.Fatal(err)
	}
	got := det.Tests[0]
	if got.FlakyInfo == nil || got.FlakyInfo.Fails != 2 || got.FlakyInfo.Runs != 4 || got.FlakyInfo.FailRate != 50 || got.FlakyInfo.Kind != StabilityFlaky || !got.FlakyInfo.LowData ||
		len(got.FlakyInfo.Recent) != 4 || got.FlakyInfo.Recent[3] != "FAIL" {
		t.Errorf("flaky info: %+v", got.FlakyInfo)
	}
	if got.NetDrift == nil || got.NetDrift.CurrentP95 != 1200 || got.NetDrift.BaselineP95 != 400 || got.NetDrift.DeltaMs != 800 {
		t.Fatalf("net drift: %+v", got.NetDrift)
	}
	eps, err := s.TestDrift(lastTest)
	if err != nil {
		t.Fatal(err)
	}
	if len(eps) != 2 || eps[0].Path != "/api/login" || eps[0].DeltaMs != 800 || eps[1].Path != "/api/users/:id" || eps[1].DeltaMs != 0 {
		t.Errorf("endpoint drift: %+v", eps)
	}
}
