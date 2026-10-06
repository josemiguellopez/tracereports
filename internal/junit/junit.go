// Package junit reads JUnit XML reports, the format almost every test runner can write (pytest
// --junitxml, Maven Surefire/Failsafe, Gradle, Playwright, Jest, Cypress, Go's gotestsum, .NET
// loggers...). It only parses: it knows nothing about the database or the API.
//
// It accepts a <testsuites> root or a single <testsuite>, suites nested at any depth, <failure>
// and <error> (both mean the test failed), <skipped>, the Surefire rerun elements
// (<flakyFailure>, <rerunFailure>...) as attempts, and documents in a non-UTF-8 encoding
// declared in the XML header.
package junit

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/encoding/htmlindex"
)

// Status of a test case, the same values the TraceReports API uses.
const (
	Pass = "PASS"
	Fail = "FAIL"
	Skip = "SKIP"
)

// Suite is a <testsuite> with its test cases (nested suites are flattened into their own Suite).
type Suite struct {
	Name      string
	Timestamp time.Time // zero when the report does not say when it ran
	Duration  time.Duration
	Cases     []Case
}

// Case is a <testcase>.
type Case struct {
	Name      string
	Classname string
	File      string
	Duration  time.Duration
	Status    string // Pass, Fail or Skip
	Message   string // failure/error message (with its type), or the skip reason
	Trace     string // text of <failure>/<error>: usually the stack trace
	Attempts  int    // 1, or more when the runner retried it (Surefire flaky/rerun elements)
	SystemOut string
	SystemErr string
}

// Key is the stable identity of the case: "classname#name", or the name alone without a class.
func (c Case) Key() string {
	if c.Classname == "" {
		return c.Name
	}
	return c.Classname + "#" + c.Name
}

// ---- XML shape ----

type xmlSuite struct {
	Name      string     `xml:"name,attr"`
	Timestamp string     `xml:"timestamp,attr"`
	Time      string     `xml:"time,attr"`
	Cases     []xmlCase  `xml:"testcase"`
	Suites    []xmlSuite `xml:"testsuite"`
	SystemOut []string   `xml:"system-out"`
	SystemErr []string   `xml:"system-err"`
}

type xmlCase struct {
	Name      string      `xml:"name,attr"`
	Classname string      `xml:"classname,attr"`
	File      string      `xml:"file,attr"`
	Time      string      `xml:"time,attr"`
	Failures  []xmlResult `xml:"failure"`
	Errors    []xmlResult `xml:"error"`
	Skipped   *xmlResult  `xml:"skipped"`
	Flaky     []xmlResult `xml:"flakyFailure"`
	FlakyErr  []xmlResult `xml:"flakyError"`
	Rerun     []xmlResult `xml:"rerunFailure"`
	RerunErr  []xmlResult `xml:"rerunError"`
	SystemOut []string    `xml:"system-out"`
	SystemErr []string    `xml:"system-err"`
}

type xmlResult struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

// Parse reads one JUnit XML document.
func Parse(r io.Reader) ([]Suite, error) {
	d := xml.NewDecoder(r)
	d.Entity = xml.HTMLEntity // &nbsp; y similares, que algunos generadores escriben
	d.CharsetReader = func(label string, in io.Reader) (io.Reader, error) {
		enc, err := htmlindex.Get(label)
		if err != nil {
			return nil, fmt.Errorf("unsupported encoding %q", label)
		}
		return enc.NewDecoder().Reader(in), nil
	}
	for {
		tok, err := d.Token()
		if err == io.EOF {
			return nil, fmt.Errorf("not a JUnit report: no <testsuites> or <testsuite> element")
		}
		if err != nil {
			return nil, fmt.Errorf("invalid XML: %w", err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		var root xmlSuite
		switch start.Name.Local {
		case "testsuites", "testsuite":
			if err := d.DecodeElement(&root, &start); err != nil {
				return nil, fmt.Errorf("invalid XML: %w", err)
			}
		default:
			return nil, fmt.Errorf("not a JUnit report: root element is <%s>", start.Name.Local)
		}
		var out []Suite
		if start.Name.Local == "testsuite" {
			flatten(root, "", &out)
		} else {
			for _, s := range root.Suites {
				flatten(s, "", &out)
			}
			// algunos generadores ponen testcase directo bajo <testsuites>
			if len(root.Cases) > 0 {
				flatten(xmlSuite{Name: root.Name, Timestamp: root.Timestamp, Time: root.Time, Cases: root.Cases}, "", &out)
			}
		}
		return out, nil
	}
}

// flatten appends s (and its nested suites, named "parent / child") to out.
func flatten(s xmlSuite, parent string, out *[]Suite) {
	name := strings.TrimSpace(s.Name)
	if parent != "" && name != "" {
		name = parent + " / " + name
	} else if name == "" {
		name = parent
	}
	suite := Suite{Name: name, Timestamp: parseTimestamp(s.Timestamp), Duration: parseSeconds(s.Time)}
	for _, c := range s.Cases {
		suite.Cases = append(suite.Cases, toCase(c))
	}
	if len(suite.Cases) > 0 {
		if suite.Duration == 0 {
			for _, c := range suite.Cases {
				suite.Duration += c.Duration
			}
		}
		*out = append(*out, suite)
	}
	for _, child := range s.Suites {
		flatten(child, name, out)
	}
}

func toCase(c xmlCase) Case {
	out := Case{
		Name:      strings.TrimSpace(c.Name),
		Classname: strings.TrimSpace(c.Classname),
		File:      strings.TrimSpace(c.File),
		Duration:  parseSeconds(c.Time),
		Status:    Pass,
		Attempts:  1 + len(c.Flaky) + len(c.FlakyErr) + len(c.Rerun) + len(c.RerunErr),
		SystemOut: strings.TrimSpace(strings.Join(c.SystemOut, "\n")),
		SystemErr: strings.TrimSpace(strings.Join(c.SystemErr, "\n")),
	}
	if out.Name == "" {
		out.Name = "(unnamed test)"
	}
	switch {
	case len(c.Failures) > 0 || len(c.Errors) > 0:
		res := append(append([]xmlResult{}, c.Failures...), c.Errors...)
		out.Status = Fail
		out.Message = message(res[0])
		var traces []string
		for _, r := range res {
			if t := strings.TrimSpace(r.Text); t != "" {
				traces = append(traces, t)
			}
		}
		out.Trace = strings.Join(traces, "\n\n")
		if out.Message == "" && out.Trace != "" {
			out.Message = firstLine(out.Trace)
		}
	case c.Skipped != nil:
		out.Status = Skip
		out.Message = strings.TrimSpace(c.Skipped.Message)
		if out.Message == "" {
			out.Message = firstLine(c.Skipped.Text)
		}
	}
	return out
}

// message is "Type: message", without repeating the type when the message already has it.
func message(r xmlResult) string {
	msg, typ := strings.TrimSpace(r.Message), strings.TrimSpace(r.Type)
	switch {
	case typ == "":
		return msg
	case msg == "":
		return typ
	case strings.HasPrefix(msg, typ):
		return msg
	}
	return typ + ": " + msg
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// parseSeconds reads a JUnit time attribute ("1.25", "1,234.5"); anything else is 0.
func parseSeconds(v string) time.Duration {
	v = strings.ReplaceAll(strings.TrimSpace(v), ",", "")
	f, err := strconv.ParseFloat(v, 64)
	if err != nil || f < 0 || f > 1e7 {
		return 0
	}
	return time.Duration(f * float64(time.Second))
}

// parseTimestamp reads the testsuite timestamp: ISO 8601 with or without zone (no zone = UTC,
// which is what Surefire and pytest write).
func parseTimestamp(v string) time.Time {
	v = strings.TrimSpace(v)
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02 15:04:05.999999999"} {
		if t, err := time.Parse(layout, v); err == nil {
			return t
		}
	}
	return time.Time{}
}
