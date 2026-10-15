package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/lab"
	"github.com/josemiguellopez/tracereports/internal/repro"
)

// Check is one expectation of a scenario, verified against the server.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type verifier struct {
	ctx    context.Context
	base   string
	client *http.Client
}

func (v verifier) get(path string, out any) (int, error) {
	req, err := http.NewRequestWithContext(v.ctx, http.MethodGet, v.base+path, nil)
	if err != nil {
		return 0, err
	}
	res, err := v.client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode == http.StatusOK && out != nil {
		return res.StatusCode, json.Unmarshal(body, out)
	}
	return res.StatusCode, nil
}

// Verify checks what the server shows for a played scenario. The Lab expectation resends the
// failed call to the shop, so the shop must still be broken.
func Verify(ctx context.Context, base string, sc Scenario, runID int64) []Check {
	v := verifier{ctx: ctx, base: strings.TrimRight(base, "/"), client: &http.Client{Timeout: 30 * time.Second}}
	e := sc.Expect
	var out []Check
	add := func(name string, ok bool, detail string, args ...any) {
		out = append(out, Check{Name: name, OK: ok, Detail: fmt.Sprintf(detail, args...)})
	}
	var run db.RunDetail
	if _, err := v.get(fmt.Sprintf("/api/v1/runs/%d", runID), &run); err != nil || run.ID == 0 {
		add("run created", false, "GET run %d: %v", runID, err)
		return out
	}
	add("run status "+e.RunStatus, run.Status == e.RunStatus, "got %s", run.Status)

	tests := map[string]*db.Test{}
	for i := range run.Tests {
		tests[run.Tests[i].Name] = &run.Tests[i]
	}
	var watched []*db.Test // los tests de las expectativas: ahí se mira la evidencia
	names := make([]string, 0, len(e.Tests))
	for name := range e.Tests {
		names = append(names, name)
	}
	sort.Strings(names) // siempre el mismo orden: el mismo test de foco en cada corrida
	for _, name := range names {
		want := e.Tests[name]
		t := tests[name]
		if t == nil {
			add(fmt.Sprintf("test %q %s", name, want), false, "not in the run (tests: %s)", testNames(run.Tests))
			continue
		}
		add(fmt.Sprintf("test %q %s", name, want), t.Status == want, "got %s", t.Status)
		watched = append(watched, t)
	}

	failed := func() *db.Test {
		if e.Focus != "" {
			return tests[e.Focus]
		}
		for _, t := range watched {
			if t.Status == "FAIL" {
				return t
			}
		}
		if len(watched) > 0 {
			return watched[0]
		}
		return nil
	}()

	if e.Incident != "" {
		inc := v.incident(runID, failed)
		add("incident "+e.Incident, inc == e.Incident, "got %q", inc)
	}
	var conns []db.NetConn
	if failed != nil && (e.Network || e.Masked || e.Lab) {
		_, _ = v.get(fmt.Sprintf("/api/v1/tests/%d/network", failed.ID), &conns)
	}
	if e.Network {
		add("network captured", len(conns) > 0, "%d calls", len(conns))
	}
	if e.Masked {
		leaked := ""
		for _, t := range run.Tests {
			var cs []db.NetConn
			_, _ = v.get(fmt.Sprintf("/api/v1/tests/%d/network", t.ID), &cs)
			for _, c := range cs {
				text := c.PostData + c.URL + fmt.Sprint(c.RequestHeaders)
				if strings.Contains(text, `"password":"`+ShopPassword+`"`) || strings.Contains(text, `"password": "`+ShopPassword+`"`) ||
					strings.Contains(text, "password="+ShopPassword) {
					leaked = c.Method + " " + c.URL
				}
			}
		}
		add("password masked", leaked == "", "stored in clear in %s", leaked)
	}
	if e.Screenshot {
		shots := 0
		for _, t := range watched {
			var full db.Test
			_, _ = v.get(fmt.Sprintf("/api/v1/tests/%d", t.ID), &full)
			for _, l := range full.Logs {
				if l.Screenshot != "" {
					shots++
				}
			}
		}
		add("screenshot", shots > 0, "%d screenshots", shots)
	}
	if e.Locator != "" && failed != nil {
		var rep struct {
			FailedSelector string `json:"failed_selector"`
			Suggestions    []struct {
				Selector, Python, JS string
			} `json:"suggestions"`
		}
		_, _ = v.get(fmt.Sprintf("/api/v1/tests/%d/locator", failed.ID), &rep)
		found := ""
		for _, s := range rep.Suggestions {
			if strings.Contains(s.Selector+s.Python+s.JS, e.Locator) {
				found = s.Selector
				break
			}
		}
		add("new selector suggested", found != "", "broken %q, %d suggestions", rep.FailedSelector, len(rep.Suggestions))
	}
	if e.NoLocator && failed != nil {
		var rep struct {
			FailedSelector string `json:"failed_selector"`
			Suggestions    []struct{ Selector string }
		}
		st, _ := v.get(fmt.Sprintf("/api/v1/tests/%d/locator", failed.ID), &rep)
		add("no selector suggested (the backend failed)", st == http.StatusNoContent || len(rep.Suggestions) == 0,
			"answered %d: broken %q, %d suggestions", st, rep.FailedSelector, len(rep.Suggestions))
	}
	if e.Flaky {
		flaky := false
		for _, t := range watched {
			flaky = flaky || (t.Status == "PASS" && t.Attempts > 1)
		}
		add("passed after retry (flaky)", flaky, "attempts of the tests: %s", attempts(watched))
	}
	if e.Command != "" && len(watched) > 0 {
		cmds := repro.Commands(run.Framework, watched[0].Key, run.Commit)
		got := ""
		for _, c := range cmds {
			got += c.Cmd + " | "
		}
		add("command to run it locally", strings.Contains(got, e.Command), "framework %q key %q: %s", run.Framework, watched[0].Key, got)
	}
	if e.Lab {
		add("Lab reproduces the failure", true, "")
		verdict, err := v.lab(conns)
		out[len(out)-1].OK, out[len(out)-1].Detail = err == nil && verdict == lab.VerdictReproduced, fmt.Sprintf("verdict %q %v", verdict, errText(err))
	}
	return out
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func testNames(ts []db.Test) string {
	var n []string
	for _, t := range ts {
		n = append(n, fmt.Sprintf("%q", t.Name))
	}
	return strings.Join(n, ", ")
}

func attempts(ts []*db.Test) string {
	var n []string
	for _, t := range ts {
		n = append(n, fmt.Sprintf("%s=%d", t.Name, t.Attempts))
	}
	return strings.Join(n, ", ")
}

// incident waits for the run's diagnosis (it is written when the run finishes) and returns the
// kind of the incident that groups the test.
func (v verifier) incident(runID int64, t *db.Test) string {
	if t == nil {
		return ""
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		var run db.RunDetail
		_, _ = v.get(fmt.Sprintf("/api/v1/runs/%d", runID), &run)
		if run.Summary != nil && run.Summary.State != "PENDING" {
			for _, inc := range run.Summary.Incidents {
				for _, id := range inc.TestIDs {
					if id == t.ID {
						return inc.Kind
					}
				}
			}
			return "none"
		}
		select {
		case <-v.ctx.Done():
			return "cancelled"
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "no diagnosis after 20 s"
}

// lab resends the failed backend call from its capsule, like `tracereports repro`.
func (v verifier) lab(conns []db.NetConn) (string, error) {
	var failed *db.NetConn
	for i := range conns {
		if conns[i].Status >= 500 {
			failed = &conns[i]
		}
	}
	if failed == nil {
		return "", fmt.Errorf("no failed backend call in the test")
	}
	req, err := http.NewRequestWithContext(v.ctx, http.MethodGet, fmt.Sprintf("%s/api/v1/network/%d/capsule", v.base, failed.ID), nil)
	if err != nil {
		return "", err
	}
	res, err := v.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	body, _ := io.ReadAll(res.Body)
	c, err := lab.ReadCapsule(body)
	if err != nil {
		return "", err
	}
	r, err := lab.Run(v.ctx, c.Calls[0].Plan, lab.Options{Values: map[string]string{"password": ShopPassword}})
	if err != nil {
		return "", err
	}
	return r.Verdict, nil
}
