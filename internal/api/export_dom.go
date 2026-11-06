package api

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/josemiguellopez/tracereports/internal/db"
)

// maxExportDOM caps the Inspector pages copied into an exported ZIP (each one weighs hundreds of KB
// uncompressed). The pages of a failure go first, then those of a WARNING, then the other steps;
// what does not fit stays on the server and the report says so.
var maxExportDOM = 64 << 20

// exportDOMPage is one page of the Inspector as the exported report lists it (dom/index.js).
type exportDOMPage struct {
	Seq      int    `json:"seq,omitempty"` // 0: la página del fallo
	Label    string `json:"label,omitempty"`
	At       int64  `json:"at,omitempty"`
	Kind     string `json:"kind"`
	Included bool   `json:"included"`
}

// exportDOMTest is what dom/index.js says of a test: its pages and how many did not fit.
type exportDOMTest struct {
	Fail    *exportDOMPage  `json:"fail,omitempty"`
	Steps   []exportDOMPage `json:"steps"`
	Omitted int             `json:"omitted,omitempty"`
}

// exportDOM writes dom/test_<id>_<seq|fail>.js for each page (the same pattern as the network:
// it works from file://) and dom/index.js with what each test has. Snapshots are redacted again,
// like the rest of the evidence that leaves in the ZIP.
func (s *Server) exportDOM(runID int64, tests []db.Test, add func(string, []byte, uint16)) {
	index := map[int64]*exportDOMTest{}
	type page struct {
		test int64
		ref  *exportDOMPage
	}
	var byPriority [3][]page // fail, warning, step
	for i := range tests {
		t := &tests[i]
		full, err := s.Store.GetTest(t.ID)
		if err != nil || (!full.HasDOM && full.DOMSteps == 0) {
			continue
		}
		e := &exportDOMTest{Steps: []exportDOMPage{}}
		index[t.ID] = e
		if full.HasDOM {
			e.Fail = &exportDOMPage{Kind: "fail"}
			byPriority[0] = append(byPriority[0], page{t.ID, e.Fail})
		}
		steps, err := s.Store.ListDOMSteps(t.ID)
		if err != nil {
			slog.Error("export: dom steps", "run_id", runID, "test_id", t.ID, "err", err)
			continue
		}
		for _, st := range steps {
			e.Steps = append(e.Steps, exportDOMPage{Seq: st.Seq, Label: s.redactor().Text(st.Label), At: st.At, Kind: st.Kind})
		}
		for k := range e.Steps {
			prio := 2
			if e.Steps[k].Kind == "warning" {
				prio = 1
			}
			byPriority[prio] = append(byPriority[prio], page{t.ID, &e.Steps[k]})
		}
	}
	if len(index) == 0 {
		return
	}
	used := 0
	for _, list := range byPriority {
		for _, p := range list {
			var raw string
			var err error
			name := fmt.Sprintf("dom/test_%d_%d.js", p.test, p.ref.Seq)
			key := fmt.Sprintf("%d/%d", p.test, p.ref.Seq)
			if p.ref.Kind == "fail" {
				raw, err = s.Store.GetDOM(p.test)
				name, key = fmt.Sprintf("dom/test_%d_fail.js", p.test), fmt.Sprintf("%d/fail", p.test)
			} else {
				raw, err = s.Store.GetDOMStep(p.test, p.ref.Seq)
			}
			if err != nil || raw == "" {
				continue
			}
			if used+len(raw) > maxExportDOM {
				index[p.test].Omitted++
				continue
			}
			clean, err := redactJSON(s.redactor(), []byte(raw))
			if err != nil {
				continue
			}
			used += len(raw)
			p.ref.Included = true
			add(name, []byte(fmt.Sprintf("(window.TRACEREPORTS_DOM = window.TRACEREPORTS_DOM || {})[%q] = %s;\n", key, clean)), zip.Deflate)
		}
	}
	js, err := json.Marshal(index)
	if err != nil {
		slog.Error("export: dom index", "run_id", runID, "err", err)
		return
	}
	add("dom/index.js", []byte("window.TRACEREPORTS_DOM_INDEX = "+string(js)+";\n"), zip.Deflate)
}
