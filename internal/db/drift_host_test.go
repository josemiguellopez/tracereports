package db

import (
	"encoding/json"
	"testing"
)

// La deriva por endpoint separa servicios: el mismo método y path en dos hosts son dos endpoints
// (la regresión de uno no queda escondida en el promedio con el otro). Los ids variables del path
// se siguen agrupando dentro de cada host.
func TestEndpointDriftKeepsHostsApart(t *testing.T) {
	s := openTestStore(t)
	sample := func(name string, a, b int64) int64 {
		run, _ := s.CreateRun(name, "qa")
		id, _ := s.CreateTestWithMeta(run, "same test", "", "", TestMeta{Key: "stable-key"})
		if err := s.AddNetwork(id, []NetConn{
			{Method: "GET", URL: "https://service-a.test/api/users/17", Status: 200, ResourceType: "fetch", DurationMs: &a},
			{Method: "GET", URL: "https://service-a.test/api/users/42", Status: 200, ResourceType: "fetch", DurationMs: &a},
			{Method: "GET", URL: "https://service-b.test/api/users/17", Status: 200, ResourceType: "fetch", DurationMs: &b},
		}); err != nil {
			t.Fatal(err)
		}
		s.FinishTest(id, "PASS", "", "")
		s.FinishRun(run)
		return id
	}
	sample("baseline", 100, 5000)
	current := sample("current", 2000, 5000)
	rows, err := s.TestDrift(current)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]EndpointDrift{}
	for _, r := range rows {
		got[r.Method+" "+hostOf(r)+r.Path] = r
	}
	a, b := got["GET service-a.test/api/users/:id"], got["GET service-b.test/api/users/:id"]
	if len(rows) != 2 || a.Count != 2 || a.BaselineP95 != 100 || a.DeltaMs != 1900 || b.Count != 1 || b.DeltaMs != 0 {
		t.Fatalf("one row per method+host+path: %+v", rows)
	}
	if hostOf(rows[0]) != "service-a.test" {
		t.Fatalf("the regression comes first: %+v", rows)
	}
}

// hostOf reads the host as the API returns it (JSON "host").
func hostOf(r EndpointDrift) string {
	raw, _ := json.Marshal(r)
	var v struct{ Host string }
	json.Unmarshal(raw, &v)
	return v.Host
}
