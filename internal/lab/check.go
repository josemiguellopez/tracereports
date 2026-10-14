package lab

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Expect is what a good answer looks like: the status (0 = any below 400) and texts the body must
// contain.
type Expect struct {
	Status   int      `json:"status"`
	Contains []string `json:"contains,omitempty"`
}

// Failure is one expectation the answer did not meet.
type Failure struct {
	Kind string `json:"kind"` // status | contains | network
	Want string `json:"want,omitempty"`
	Got  string `json:"got,omitempty"`
}

// Validate normalizes and checks an expectation.
func (e *Expect) Validate() error {
	if e.Status != 0 && (e.Status < 100 || e.Status > 599) {
		return fmt.Errorf("invalid expected status %d", e.Status)
	}
	var keep []string
	for _, c := range e.Contains {
		if c = strings.TrimSpace(c); c != "" {
			keep = append(keep, c)
		}
	}
	if len(keep) > 20 {
		return errors.New("at most 20 expected texts")
	}
	e.Contains = keep
	return nil
}

// Eval compares an answer with the expectation; no failures means the check passed.
func (e Expect) Eval(r Response) []Failure {
	if r.Status == 0 {
		return []Failure{{Kind: "network", Got: r.Error}}
	}
	var out []Failure
	switch {
	case e.Status == 0 && r.Status >= 400:
		out = append(out, Failure{Kind: "status", Want: "< 400", Got: strconv.Itoa(r.Status)})
	case e.Status != 0 && r.Status != e.Status:
		out = append(out, Failure{Kind: "status", Want: strconv.Itoa(e.Status), Got: strconv.Itoa(r.Status)})
	}
	for _, c := range e.Contains {
		if !strings.Contains(r.Body, c) {
			out = append(out, Failure{Kind: "contains", Want: c})
		}
	}
	return out
}
