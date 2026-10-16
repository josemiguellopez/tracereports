package offline

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkersAtTheSameMillisecondFollowRunBoundaries(t *testing.T) {
	// El archivo del worker ordena antes que el del dueño, pero depende de su ejecución.
	dir := t.TempDir()
	write := func(name, lines string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(lines), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("events-a.jsonl", `{"seq":1,"ts":100,"method":"POST","path":"/api/v1/runs/-1/tests","local_id":-2}
{"seq":2,"ts":100,"method":"POST","path":"/api/v1/tests/-2/logs"}
{"seq":3,"ts":100,"method":"PATCH","path":"/api/v1/tests/-2/finish"}
`)
	write("events-b.jsonl", `{"seq":1,"ts":100,"method":"POST","path":"/api/v1/runs","local_id":-1}
{"seq":2,"ts":100,"method":"PATCH","path":"/api/v1/runs/-1/finish"}
`)
	r, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	events := r.Events()
	if len(events) != 5 || events[0].Path != "/api/v1/runs" || events[4].Path != "/api/v1/runs/-1/finish" {
		t.Fatalf("run boundaries: %+v", events)
	}
	if events[1].LocalID != -2 || events[2].Path != "/api/v1/tests/-2/logs" || events[3].Path != "/api/v1/tests/-2/finish" {
		t.Fatalf("worker order: %+v", events)
	}
}
