package api

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
)

func TestCreateRunValidatesTheCommit(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, tc := range []struct{ commit, stored string }{
		{";echo MARK;#", ""},
		{"abc$(echo MARK)", ""},
		{"abc\necho MARK", ""},
		{"0123456789ABCDEF0123456789ABCDEF01234567", "0123456789abcdef0123456789abcdef01234567"},
		{"abc1234", "abc1234"},
		{"", ""},
	} {
		body, _ := json.Marshal(map[string]string{"name": "x", "commit": tc.commit, "framework": "pytest"})
		rec := call(t, srv, "POST", "/api/v1/runs", string(body))
		var out struct {
			RunID    int64    `json:"run_id"`
			Warnings []string `json:"warnings"`
		}
		json.Unmarshal(rec.Body.Bytes(), &out)
		if rec.Code != 201 {
			t.Fatalf("%q: the run is created anyway: %d", tc.commit, rec.Code)
		}
		run, _ := srv.Store.GetRun(out.RunID)
		if run.Commit != tc.stored {
			t.Fatalf("%q stored as %q, want %q", tc.commit, run.Commit, tc.stored)
		}
		if (tc.stored == "" && tc.commit != "") != (len(out.Warnings) == 1) {
			t.Fatalf("%q: warning %v", tc.commit, out.Warnings)
		}
	}
	// importaciones: el mismo criterio
	res, rec := importAllureZip(t, srv, "?commit="+url.QueryEscape(";echo MARK;#"), allureZip(t, "", nil), "application/zip")
	if rec.Code != 201 {
		t.Fatalf("import: %d", rec.Code)
	}
	if run, _ := srv.Store.GetRun(res.RunID); run.Commit != "" {
		t.Fatalf("import stored %q", run.Commit)
	}
}

// Datos guardados por una versión anterior, sin validar: los comandos tampoco los usan.
func TestStoredHostileCommitNeverReachesTheCommands(t *testing.T) {
	clearAIEnv(t)
	srv, _ := newTestServer(t)
	runID, _ := srv.Store.CreateRunWithMeta("old", "qa", db.RunMeta{Commit: ";echo MARK;#", Framework: "pytest"})
	testID, _ := srv.Store.CreateTestWithMeta(runID, "t", "", "", db.TestMeta{Key: "tests/test_login.py::test_admin"})
	srv.Store.FinishTest(testID, "FAIL", "boom", "")
	b, _ := json.Marshal(map[string]any{"run_id": runID, "test_id": testID, "audience": "dev", "lang": "es"})
	var e ai.Escalation
	json.Unmarshal(call(t, srv, "POST", "/api/v1/ui/escalate", string(b)).Body.Bytes(), &e)
	if len(e.Facts.Dev) != 1 || len(e.Facts.Dev[0].Repro) != 1 {
		t.Fatalf("dev detail: %+v", e.Facts.Dev)
	}
	if cmd := e.Facts.Dev[0].Repro[0].Cmd; strings.Contains(cmd, "MARK") || cmd != "pytest 'tests/test_login.py::test_admin'" {
		t.Fatalf("stored commit used in the command: %s", cmd)
	}
}
