package run

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// sized completes the artifacts of an ended run with the size of its event file.
func (m *Manager) sized(v model.Run) model.Run {
	if v.Artifacts == nil {
		return v
	}
	as := append([]model.Artifact(nil), *v.Artifacts...)
	for i := range as {
		if as[i].Kind == model.ArtifactKindEvents {
			as[i].SizeBytes = ptr(m.eventFileSize(v.Id))
		}
	}
	v.Artifacts = &as
	return v
}

// Report returns the report of a run that has ended: the run with its timeline as executed and the final
// counters of the overlays it created.
func (m *Manager) Report(id uuid.UUID) (model.RunReport, error) {
	m.mu.Lock()
	r, ok := m.runs[id]
	if !ok {
		m.mu.Unlock()
		return model.RunReport{}, ErrNotFound
	}
	if !ended(r.rec.Status) {
		m.mu.Unlock()
		return model.RunReport{}, ErrNotEnded
	}
	run, counters := r.view(), append([]counterRecord(nil), r.counters...)
	m.mu.Unlock()
	m.awaitRecorded(r) // the size of the event file is the final one
	return reportOf(m.sized(run), counters)
}

func reportOf(run model.Run, counters []counterRecord) (model.RunReport, error) {
	raw, err := json.Marshal(run)
	if err != nil {
		return model.RunReport{}, err
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return model.RunReport{}, err
	}
	if len(counters) > 0 {
		list := make([]map[string]any, 0, len(counters))
		for _, c := range counters {
			list = append(list, map[string]any{"step": c.Step, "overlay": c.Overlay, "counters": c.Counters})
		}
		doc["counters"] = list
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		return model.RunReport{}, err
	}
	var out model.RunReport
	err = json.Unmarshal(raw, &out)
	return out, err
}

// ---- JUnit

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Errors   int          `xml:"errors,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name       string          `xml:"name,attr"`
	ID         string          `xml:"id,attr"`
	Tests      int             `xml:"tests,attr"`
	Failures   int             `xml:"failures,attr"`
	Errors     int             `xml:"errors,attr"`
	Skipped    int             `xml:"skipped,attr"`
	Time       string          `xml:"time,attr"`
	Timestamp  string          `xml:"timestamp,attr,omitempty"`
	Properties []junitProperty `xml:"properties>property"`
	Cases      []junitCase     `xml:"testcase"`
	SystemOut  string          `xml:"system-out,omitempty"`
}

type junitProperty struct {
	Name  string `xml:"name,attr"`
	Value string `xml:"value,attr"`
}

type junitCase struct {
	Name      string        `xml:"name,attr"`
	ClassName string        `xml:"classname,attr"`
	Time      string        `xml:"time,attr"`
	Failure   *junitProblem `xml:"failure,omitempty"`
	Error     *junitProblem `xml:"error,omitempty"`
	Skipped   *junitSkip    `xml:"skipped,omitempty"`
}

type junitProblem struct {
	Message string `xml:"message,attr"`
	Type    string `xml:"type,attr"`
	Text    string `xml:",chardata"`
}

type junitSkip struct {
	Message string `xml:"message,attr"`
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.3f", d.Seconds()) }

// JUnit returns the report of a run that has ended as JUnit XML for CI: one test suite, a test case for the
// run itself (an error when the run ended as `error` or `aborted`, with its reason), one for each step that
// could not be executed and one for each check (a failure when it failed, an error when it could not be
// decided). The warnings, the timeline and the final counters are the suite's output.
func (m *Manager) JUnit(id uuid.UUID) ([]byte, error) {
	rep, err := m.Report(id)
	if err != nil {
		return nil, err
	}
	return junitOf(rep)
}

func junitOf(rep model.RunReport) ([]byte, error) {
	class := rep.Scenario.Name
	var total time.Duration
	if rep.StartedAt != nil && rep.EndedAt != nil {
		total = rep.EndedAt.Sub(*rep.StartedAt)
	}
	suite := junitSuite{Name: class, ID: rep.Id.String(), Time: seconds(total)}
	if rep.StartedAt != nil {
		suite.Timestamp = rep.StartedAt.UTC().Format(time.RFC3339)
	} else {
		suite.Timestamp = rep.CreatedAt.UTC().Format(time.RFC3339)
	}
	suite.Properties = []junitProperty{
		{"run", rep.Id.String()}, {"status", string(rep.Status)}, {"revision", fmt.Sprint(rep.Revision)},
		{"inline", fmt.Sprint(rep.Scenario.Inline)}, {"owner", string(rep.Owner.Type) + ":" + rep.Owner.Id},
	}
	if rep.Reason != nil {
		suite.Properties = append(suite.Properties, junitProperty{"reason", *rep.Reason})
	}

	whole := junitCase{Name: "run", ClassName: class, Time: seconds(total)}
	switch rep.Status {
	case model.RunStatusError:
		whole.Error = &junitProblem{Message: deref(rep.Reason), Type: "run_error", Text: deref(rep.Reason)}
	case model.RunStatusAborted:
		whole.Error = &junitProblem{Message: "aborted: " + deref(rep.Reason), Type: "run_aborted", Text: deref(rep.Reason)}
	}
	suite.Cases = append(suite.Cases, whole)
	for _, st := range deref(rep.Steps) {
		if st.Status != model.RunStepStatusError {
			continue
		}
		suite.Cases = append(suite.Cases, junitCase{Name: "step " + st.Id, ClassName: class, Time: "0.000",
			Error: &junitProblem{Message: deref(st.Error), Type: "step_error", Text: deref(st.Error)}})
	}
	for _, c := range deref(rep.Checks) {
		tc := junitCase{Name: c.Name, ClassName: class, Time: "0.000"}
		switch c.Status {
		case model.CheckResultStatusFailed:
			tc.Failure = &junitProblem{Message: deref(c.Message), Type: string(c.Type), Text: deref(c.Message)}
		case model.CheckResultStatusError:
			tc.Error = &junitProblem{Message: deref(c.Message), Type: string(c.Type), Text: deref(c.Message)}
		case model.CheckResultStatusPending:
			tc.Skipped = &junitSkip{Message: "not decided before the run ended"}
		}
		suite.Cases = append(suite.Cases, tc)
	}
	for _, c := range suite.Cases {
		suite.Tests++
		switch {
		case c.Failure != nil:
			suite.Failures++
		case c.Error != nil:
			suite.Errors++
		case c.Skipped != nil:
			suite.Skipped++
		}
	}
	suite.SystemOut = junitOutput(rep)

	all := junitSuites{Name: "chaos-gateway", Tests: suite.Tests, Failures: suite.Failures, Errors: suite.Errors, Skipped: suite.Skipped,
		Time: suite.Time, Suites: []junitSuite{suite}}
	out, err := xml.MarshalIndent(all, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(out, '\n')...), nil
}

func junitOutput(rep model.RunReport) string {
	var b strings.Builder
	for _, w := range deref(rep.Warnings) {
		fmt.Fprintf(&b, "warning %s: %s\n", w.Code, w.Message)
	}
	for _, st := range deref(rep.Steps) {
		fmt.Fprintf(&b, "step %s", st.Id)
		if st.Type != nil {
			fmt.Fprintf(&b, " (%s)", *st.Type)
		}
		fmt.Fprintf(&b, ": %s", st.Status)
		if st.OffsetMs != nil {
			fmt.Fprintf(&b, " at +%d ms", *st.OffsetMs)
		}
		if st.ScheduledOffset != nil {
			fmt.Fprintf(&b, " (scheduled %s)", *st.ScheduledOffset)
		}
		if st.QueueWaitMs != nil {
			fmt.Fprintf(&b, ", queue wait %d ms", *st.QueueWaitMs)
		}
		if st.Generation != nil {
			fmt.Fprintf(&b, ", generation %d", *st.Generation)
		}
		if st.Error != nil {
			fmt.Fprintf(&b, ": %s", *st.Error)
		}
		b.WriteByte('\n')
	}
	for _, c := range deref(rep.Counters) {
		if c.Counters == nil {
			continue
		}
		fmt.Fprintf(&b, "counters of step %s: %d packets, %d bytes\n", deref(c.Step), c.Counters.Packets, c.Counters.Bytes)
	}
	return b.String()
}
