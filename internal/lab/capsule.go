package lab

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/josemiguellopez/tracereports/internal/repro"
)

// Capsule is everything needed to reproduce a failure away from the report: the run's context,
// how to run the test locally, and the plan of each call to resend with its saved checks. It only
// carries masked evidence (the {{variables}} are typed when it is used), so it can be shared like
// the report itself. `tracereports repro` and `tracereports verify` read it.
type Capsule struct {
	Format      string        `json:"format"`
	Version     int           `json:"version"`
	Source      string        `json:"source,omitempty"` // dónde se generó (link al reporte)
	Run         CapsuleRun    `json:"run"`
	Incident    string        `json:"incident,omitempty"`
	IncidentKey string        `json:"incident_key,omitempty"`
	Calls       []CapsuleCall `json:"calls"`
}

// CapsuleRun is the context of the run the failure comes from.
type CapsuleRun struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Project     string `json:"project,omitempty"`
	Environment string `json:"environment,omitempty"`
	Branch      string `json:"branch,omitempty"`
	Commit      string `json:"commit,omitempty"`
	Framework   string `json:"framework,omitempty"`
}

// CapsuleTest is the failed test and the commands that run it locally.
type CapsuleTest struct {
	ID       int64           `json:"id"`
	Name     string          `json:"name"`
	Key      string          `json:"key,omitempty"`
	Error    string          `json:"error,omitempty"`
	Commands []repro.Command `json:"commands,omitempty"`
}

// CapsuleCheck is a saved check: the request to send (nil = the captured call) and the good answer.
type CapsuleCheck struct {
	Name    string   `json:"name"`
	Request *Request `json:"request,omitempty"`
	BaseURL string   `json:"base_url,omitempty"`
	Expect  Expect   `json:"expect"`
}

// CapsuleCall is one call to resend.
type CapsuleCall struct {
	Test   CapsuleTest    `json:"test"`
	Plan   *Plan          `json:"plan"`
	Checks []CapsuleCheck `json:"checks,omitempty"`
}

// Capsule format identifiers.
const (
	CapsuleFormat  = "tracereports-capsule"
	CapsuleVersion = 1
)

// ReadCapsule parses and checks a capsule.
func ReadCapsule(data []byte) (*Capsule, error) {
	var c Capsule
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("not a TraceReports capsule: %w", err)
	}
	if c.Format != CapsuleFormat {
		return nil, errors.New("not a TraceReports capsule (format must be \"" + CapsuleFormat + "\")")
	}
	if c.Version < 1 || c.Version > CapsuleVersion {
		return nil, fmt.Errorf("capsule version %d is not supported by this version of tracereports (up to %d): update it", c.Version, CapsuleVersion)
	}
	for i, call := range c.Calls {
		if call.Plan == nil || call.Plan.Target.URL == "" {
			return nil, fmt.Errorf("capsule call %d has no request", i+1)
		}
		for j := range call.Checks {
			if err := call.Checks[j].Expect.Validate(); err != nil {
				return nil, fmt.Errorf("capsule call %d, check %q: %w", i+1, call.Checks[j].Name, err)
			}
		}
	}
	return &c, nil
}

// Needs lists the secrets a request of the plan asks for (target nil = the captured call).
func (p *Plan) Needs(target *Request) []string {
	t := p.Target
	if target != nil {
		t = *target
	}
	reqs := []Request{t}
	if p.Login != nil {
		reqs = append(reqs, p.Login.Request)
	}
	used := map[string]bool{}
	for _, q := range reqs {
		for _, m := range varRe.FindAllStringSubmatch(q.URL+"\n"+q.Body+"\n"+headerText(q.Headers), -1) {
			used[m[1]] = true
		}
	}
	var out []string
	for _, v := range p.Vars {
		if used[v.Name] && v.Kind == KindSecret && !v.Optional {
			out = append(out, v.Name)
		}
	}
	return out
}
