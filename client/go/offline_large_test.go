package tracereports

import (
	"bufio"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Una grabación con un evento de más de 16 MiB la lee el CLI (report).
func TestAnEventOver16MiBCanBeReported(t *testing.T) {
	bin := os.Getenv("TRACEREPORTS_BIN")
	if bin == "" { bin = os.Getenv("TRACEREPORTS_TEST_BIN") }
	if bin == "" {
		t.Skip("needs the tracereports binary ($TRACEREPORTS_BIN)")
	}
	t.Setenv("TRACEREPORTS_OFFLINE_REPORT", "0")
	rec := filepath.Join(t.TempDir(), "rec")
	c := New("http://127.0.0.1:9")
	c.Offline, c.OfflineDir = "always", rec
	c.StartRun("offline-large", "qa")
	tt, _ := c.StartTest("large POST", "", "")
	tt.Info("antes del upload")
	tt.Network([]Conn{{Method: "POST", URL: "https://app/api/upload", Status: 500, PostData: strings.Repeat("ñ\"x", 4<<20), ResponseBody: "{}"}})
	tt.Info("después del upload")
	tt.Finish(Fail, "boom", "")
	c.FinishRun()

	files, _ := filepath.Glob(filepath.Join(rec, "events-*.jsonl"))
	longest := 0
	for _, f := range files {
		fh, _ := os.Open(f)
		r := bufio.NewReader(fh)
		for {
			line, err := r.ReadString('\n')
			if len(line) > longest {
				longest = len(line)
			}
			if err != nil {
				break
			}
		}
		fh.Close()
	}
	if longest <= 16<<20 {
		t.Fatalf("the fixture must write a line over 16 MiB: %d", longest)
	}
	out := filepath.Join(t.TempDir(), "report")
	if b, err := exec.Command(bin, "report", rec, "-o", out).CombinedOutput(); err != nil {
		t.Fatalf("report: %v %s", err, b)
	}
	data, _ := os.ReadFile(filepath.Join(out, "data.js"))
	s := string(data)
	if !strings.Contains(s, "https://app/api/upload") || strings.Index(s, "antes del upload") > strings.Index(s, "después del upload") {
		t.Fatal("report content")
	}
}
