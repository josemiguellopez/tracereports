package offline

import "testing"

func TestWorkersAtTheSameMillisecondFollowRunBoundaries(t *testing.T) {
	// El archivo del worker ordena antes que el del dueño, pero depende de su ejecución.
	r := Recording{streams: [][]Event{
		{{TS: 100, Method: "POST", Path: "/api/v1/runs/-1/tests", LocalID: -2},
			{TS: 100, Method: "POST", Path: "/api/v1/tests/-2/logs"},
			{TS: 100, Method: "PATCH", Path: "/api/v1/tests/-2/finish"}},
		{{TS: 100, Method: "POST", Path: "/api/v1/runs", LocalID: -1},
			{TS: 100, Method: "PATCH", Path: "/api/v1/runs/-1/finish"}},
	}}
	events := r.Events()
	if events[0].Path != "/api/v1/runs" || events[4].Path != "/api/v1/runs/-1/finish" {
		t.Fatalf("run boundaries: %+v", events)
	}
	if events[1].LocalID != -2 || events[2].Path != "/api/v1/tests/-2/logs" || events[3].Path != "/api/v1/tests/-2/finish" {
		t.Fatalf("worker order: %+v", events)
	}
}
