// Package allure reads an allure-results folder (allure-pytest, allure-junit5, allure-testng,
// allure-playwright, allure-cucumber, allure-js...): one <uuid>-result.json per test execution,
// plus the attachment files they reference. It only parses: it knows nothing about the API.
//
// Results are read from an fs.FS, so the folder can come from disk (os.DirFS) or from a ZIP
// (zip.Reader). Retries of the same test (same historyId) are merged into one Result whose
// Attempts counts them and whose data is the last execution.
package allure

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// Status of a result or step, the same values the TraceReports API uses.
const (
	Pass = "PASS"
	Fail = "FAIL"
	Skip = "SKIP"
)

const (
	maxResultFile = 16 << 20 // un result.json más grande no es razonable
	maxAttachment = 15 << 20 // el mismo límite que una captura subida a la API
)

// Result is one test (the last of its retries).
type Result struct {
	Name, FullName  string
	Status          string // Pass, Fail or Skip
	Message, Trace  string
	Description     string
	Start, Stop     int64 // Unix ms (0 if unknown)
	Suite           string
	Tags            []string // tag, feature, story and epic labels
	Params          string   // "name=value, ..."
	Framework       string
	Attempts        int
	Steps           []Step
	Attachments     []Attachment // of the test itself (not of a step)
	historyID, uuid string
}

// Key is the stable identity of the test: its full name (or name) plus its parameters.
func (r Result) Key() string {
	k := r.FullName
	if k == "" {
		k = r.Name
	}
	if r.Params != "" {
		k += " [" + r.Params + "]"
	}
	return k
}

// Step is a step of a test, with its own nested steps and attachments.
type Step struct {
	Name        string
	Status      string
	Message     string
	Start, Stop int64
	Steps       []Step
	Attachments []Attachment
}

// Attachment is a file attached to a test or a step. Image reads its bytes when it is an image.
type Attachment struct {
	Name, Type, Source string
	fsys               fs.FS
}

// IsImage reports whether the attachment is a picture (by its declared type or extension).
func (a Attachment) IsImage() bool {
	if strings.HasPrefix(a.Type, "image/") {
		return true
	}
	switch strings.ToLower(path.Ext(a.Source)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp":
		return true
	}
	return false
}

// Read returns the attachment bytes (at most 15 MB); nil if the file is missing.
func (a Attachment) Read() ([]byte, error) {
	if a.fsys == nil || a.Source == "" || strings.Contains(a.Source, "/") || strings.Contains(a.Source, `\`) {
		return nil, errors.New("invalid attachment source")
	}
	f, err := a.fsys.Open(a.Source)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxAttachment+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxAttachment {
		return nil, fmt.Errorf("attachment %s is larger than %d MB", a.Source, maxAttachment>>20)
	}
	return data, nil
}

// ---- JSON shape ----

type jsonDetails struct {
	Message string `json:"message"`
	Trace   string `json:"trace"`
}

type jsonAttachment struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Type   string `json:"type"`
}

type jsonStep struct {
	Name          string           `json:"name"`
	Status        string           `json:"status"`
	StatusDetails jsonDetails      `json:"statusDetails"`
	Start         int64            `json:"start"`
	Stop          int64            `json:"stop"`
	Steps         []jsonStep       `json:"steps"`
	Attachments   []jsonAttachment `json:"attachments"`
}

type jsonResult struct {
	UUID          string                         `json:"uuid"`
	HistoryID     string                         `json:"historyId"`
	FullName      string                         `json:"fullName"`
	Name          string                         `json:"name"`
	Status        string                         `json:"status"`
	StatusDetails jsonDetails                    `json:"statusDetails"`
	Description   string                         `json:"description"`
	Start         int64                          `json:"start"`
	Stop          int64                          `json:"stop"`
	Labels        []struct{ Name, Value string } `json:"labels"`
	Parameters    []struct{ Name, Value string } `json:"parameters"`
	Steps         []jsonStep                     `json:"steps"`
	Attachments   []jsonAttachment               `json:"attachments"`
}

// Report is an allure-results folder: its tests and the build that produced them.
type Report struct {
	Results []Result
	Build   string // executor.json buildName (or name): "Nightly #42"
}

// Parse reads every *-result.json of the folder (the root of fsys, or a single subfolder when
// the ZIP was made from the folder itself).
func Parse(fsys fs.FS) (*Report, error) {
	root, err := resultsRoot(fsys)
	if err != nil {
		return nil, err
	}
	if root != "." {
		if fsys, err = fs.Sub(fsys, root); err != nil {
			return nil, err
		}
	}
	names, err := fs.Glob(fsys, "*-result.json")
	if err != nil {
		return nil, err
	}
	rep := &Report{Build: buildName(fsys)}
	sort.Strings(names)
	byHistory := map[string][]Result{}
	var order []string
	for _, name := range names {
		r, err := readResult(fsys, name)
		if err != nil {
			return nil, err
		}
		id := r.historyID
		if id == "" {
			id = "uuid:" + r.uuid + name
		}
		if _, seen := byHistory[id]; !seen {
			order = append(order, id)
		}
		byHistory[id] = append(byHistory[id], r)
	}
	out := make([]Result, 0, len(order))
	for _, id := range order {
		runs := byHistory[id]
		sort.SliceStable(runs, func(i, j int) bool { return runs[i].Stop < runs[j].Stop })
		last := runs[len(runs)-1]
		last.Attempts = len(runs)
		out = append(out, last)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	rep.Results = out
	return rep, nil
}

// buildName reads executor.json, which the CI plugins write ("" if missing or invalid).
func buildName(fsys fs.FS) string {
	raw, err := fs.ReadFile(fsys, "executor.json")
	if err != nil || len(raw) > 1<<20 {
		return ""
	}
	var e struct{ BuildName, Name string }
	if json.Unmarshal(raw, &e) != nil {
		return ""
	}
	if e.BuildName != "" {
		return strings.TrimSpace(e.BuildName)
	}
	return strings.TrimSpace(e.Name)
}

// resultsRoot finds where the *-result.json files are: "." or one subfolder.
func resultsRoot(fsys fs.FS) (string, error) {
	if m, _ := fs.Glob(fsys, "*-result.json"); len(m) > 0 {
		return ".", nil
	}
	if m, _ := fs.Glob(fsys, "*/*-result.json"); len(m) > 0 {
		root := path.Dir(m[0])
		for _, f := range m {
			if path.Dir(f) != root {
				return "", errors.New("results in several folders: pass one allure-results folder")
			}
		}
		return root, nil
	}
	return "", errors.New("not an allure-results folder: no *-result.json files")
}

func readResult(fsys fs.FS, name string) (Result, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxResultFile+1))
	if err != nil {
		return Result{}, err
	}
	if len(raw) > maxResultFile {
		return Result{}, fmt.Errorf("%s: larger than %d MB", name, maxResultFile>>20)
	}
	var j jsonResult
	if err := json.Unmarshal(raw, &j); err != nil {
		return Result{}, fmt.Errorf("%s: invalid JSON: %w", name, err)
	}
	r := Result{
		Name: strings.TrimSpace(j.Name), FullName: strings.TrimSpace(j.FullName), Status: status(j.Status),
		Message: strings.TrimSpace(j.StatusDetails.Message), Trace: strings.TrimSpace(j.StatusDetails.Trace),
		Description: strings.TrimSpace(j.Description), Start: j.Start, Stop: j.Stop,
		Steps: steps(fsys, j.Steps), Attachments: attachments(fsys, j.Attachments),
		historyID: j.HistoryID, uuid: j.UUID,
	}
	if r.Name == "" {
		r.Name = r.FullName
	}
	if r.Name == "" {
		r.Name = "(unnamed test)"
	}
	var parent, suite, sub string
	seen := map[string]bool{}
	for _, l := range j.Labels {
		v := strings.TrimSpace(l.Value)
		switch l.Name {
		case "parentSuite":
			parent = v
		case "suite":
			suite = v
		case "subSuite":
			sub = v
		case "framework":
			if r.Framework == "" {
				r.Framework = v
			}
		case "tag", "feature", "story", "epic":
			if v != "" && !seen[strings.ToLower(v)] {
				seen[strings.ToLower(v)] = true
				r.Tags = append(r.Tags, v)
			}
		}
	}
	var parts []string
	for _, p := range []string{parent, suite, sub} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	r.Suite = strings.Join(parts, " / ")
	var params []string
	for _, p := range j.Parameters {
		params = append(params, p.Name+"="+p.Value)
	}
	r.Params = strings.Join(params, ", ")
	return r, nil
}

// status maps Allure statuses: broken (an error, not an assertion) is a failure too, and
// unknown (the test did not finish its report) is not a pass.
func status(s string) string {
	switch strings.ToLower(s) {
	case "passed":
		return Pass
	case "failed", "broken":
		return Fail
	case "skipped":
		return Skip
	}
	return Skip
}

func steps(fsys fs.FS, in []jsonStep) []Step {
	out := make([]Step, 0, len(in))
	for _, s := range in {
		out = append(out, Step{Name: strings.TrimSpace(s.Name), Status: status(s.Status),
			Message: strings.TrimSpace(s.StatusDetails.Message), Start: s.Start, Stop: s.Stop,
			Steps: steps(fsys, s.Steps), Attachments: attachments(fsys, s.Attachments)})
	}
	return out
}

func attachments(fsys fs.FS, in []jsonAttachment) []Attachment {
	out := make([]Attachment, 0, len(in))
	for _, a := range in {
		out = append(out, Attachment{Name: strings.TrimSpace(a.Name), Type: a.Type, Source: a.Source, fsys: fsys})
	}
	return out
}
