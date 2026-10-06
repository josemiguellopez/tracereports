package api

import (
	"net/http"
	"slices"
	"strings"

	"github.com/josemiguellopez/tracereports/internal/ai"
	"github.com/josemiguellopez/tracereports/internal/db"
	"github.com/josemiguellopez/tracereports/internal/owners"
)

// decorate fills what the store does not know about the tests of a run: their owner (rules) and
// their verdicts (this run's and the previous one of the same test).
func (s *Server) decorate(runID int64, tests []db.Test) error {
	if len(tests) == 0 {
		return nil
	}
	current, previous, err := s.Store.VerdictsOfRun(runID)
	if err != nil {
		return err
	}
	for i := range tests {
		t := &tests[i]
		t.Owner = s.ownerOf(t)
		t.Verdict, t.PreviousVerdict = current[t.ID], previous[t.ID]
	}
	return nil
}

func (s *Server) ownerOf(t *db.Test) string {
	return s.Owners.Owner(owners.Test{Key: t.Key, Suite: t.Suite, Tags: owners.Tags(t.Category)})
}

// decorateOne is decorate for a single test.
func (s *Server) decorateOne(t *db.Test) error {
	one := []db.Test{*t}
	if err := s.decorate(t.RunID, one); err != nil {
		return err
	}
	t.Owner, t.Verdict, t.PreviousVerdict = one[0].Owner, one[0].Verdict, one[0].PreviousVerdict
	return nil
}

// setVerdict classifies a failure: product_bug, test_bug, environment, data, flaky or other,
// with an optional comment and author. The latest one is shown; earlier runs keep theirs.
func (s *Server) setVerdict(w http.ResponseWriter, r *http.Request) {
	if !s.guardUIWrite(w, r) {
		return
	}
	id, ok := pathID(w, r, "test_id")
	if !ok {
		return
	}
	var in struct {
		Verdict string `json:"verdict"`
		Comment string `json:"comment"`
		Author  string `json:"author"`
	}
	if !decode(w, r, &in) {
		return
	}
	if !slices.Contains(db.Verdicts, in.Verdict) {
		writeError(w, http.StatusBadRequest, "verdict must be one of "+strings.Join(db.Verdicts, ", "))
		return
	}
	t, run, ok := s.testAndRun(w, id)
	if !ok {
		return
	}
	v := &db.Verdict{RunID: run.ID, TestID: t.ID, Verdict: in.Verdict, Comment: clean(s.redactor().Text(in.Comment), 2000),
		Author: clean(s.label(in.Author), 120)}
	if err := s.Store.SaveVerdict(v, run.Project, t.Key); err != nil {
		serverError(w, err)
		return
	}
	s.publish("test", run.ID, t.ID, map[string]string{"action": "verdict"})
	writeJSON(w, http.StatusCreated, v)
}

// withOwner puts the owner from the rules in an escalation (instead of the AI or template guess):
// the test's owner, or the owners of the run's failed tests.
func (s *Server) withOwner(e *ai.Escalation) {
	if s.Owners.Len() == 0 || e == nil {
		return
	}
	if e.TestID != 0 {
		if t, err := s.Store.GetTest(e.TestID); err == nil {
			if o := s.ownerOf(t); o != "" {
				e.Owner = o
			}
		}
		return
	}
	d, err := s.Store.GetRunDetail(e.RunID)
	if err != nil {
		return
	}
	var list []string
	for i := range d.Tests {
		if d.Tests[i].Status == "FAIL" {
			if o := s.ownerOf(&d.Tests[i]); o != "" && !slices.Contains(list, o) {
				list = append(list, o)
			}
		}
	}
	if len(list) > 0 {
		e.Owner = strings.Join(list, ", ")
	}
}
