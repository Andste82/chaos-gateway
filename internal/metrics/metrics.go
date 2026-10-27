// Package metrics writes the Prometheus text exposition format (version 0.0.4) for the metrics endpoint
// (plan §2.12, M26). The endpoint reads its values from the engine at the moment of the scrape, so there is
// nothing to register or keep: a Set collects the series of one scrape and writes them out, sorted, so two
// scrapes of the same state are the same bytes.
package metrics

import (
	"fmt"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// ContentType is the media type of the text format.
const ContentType = "text/plain; version=0.0.4; charset=utf-8"

// Kind is the type of a metric family.
type Kind string

// The kinds of metric the endpoint reports.
const (
	KindCounter Kind = "counter"
	KindGauge   Kind = "gauge"
)

// Label is one label of a series.
type Label struct{ Name, Value string }

// L makes a label.
func L(name, value string) Label { return Label{name, value} }

var (
	nameRE  = regexp.MustCompile(`^[a-zA-Z_:][a-zA-Z0-9_:]*$`)
	labelRE = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)
)

type series struct {
	labels string // the rendered label set, `{a="b",c="d"}` or empty: also the key of the series
	value  float64
}

type family struct {
	name, help string
	kind       Kind
	series     map[string]series
}

// Set is the metrics of one scrape.
type Set struct {
	families map[string]*family
	err      error
}

// NewSet returns an empty set.
func NewSet() *Set { return &Set{families: map[string]*family{}} }

// Counter adds a series of a counter: a value that only grows (until the thing it counts starts over).
func (s *Set) Counter(name, help string, v float64, labels ...Label) {
	s.add(KindCounter, name, help, v, labels)
}

// Gauge adds a series of a gauge.
func (s *Set) Gauge(name, help string, v float64, labels ...Label) {
	s.add(KindGauge, name, help, v, labels)
}

// Err is the first thing that was wrong with what was added (a name or label the format does not allow, or a family
// added with two kinds): the series is left out, the rest is written. A series added twice with the same labels
// keeps the later value.
func (s *Set) Err() error { return s.err }

func (s *Set) fail(format string, a ...any) {
	if s.err == nil {
		s.err = fmt.Errorf("metrics: "+format, a...)
	}
}

func (s *Set) add(kind Kind, name, help string, v float64, labels []Label) {
	if !nameRE.MatchString(name) {
		s.fail("invalid metric name %q", name)
		return
	}
	f, ok := s.families[name]
	if !ok {
		f = &family{name: name, help: help, kind: kind, series: map[string]series{}}
		s.families[name] = f
	} else if f.kind != kind {
		s.fail("%s is a %s and a %s", name, f.kind, kind)
		return
	}
	sorted := append([]Label(nil), labels...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
	var b strings.Builder
	for i, l := range sorted {
		if !labelRE.MatchString(l.Name) || strings.HasPrefix(l.Name, "__") || (i > 0 && sorted[i-1].Name == l.Name) {
			s.fail("invalid or repeated label %q of %s", l.Name, name)
			return
		}
		if i == 0 {
			b.WriteByte('{')
		} else {
			b.WriteByte(',')
		}
		b.WriteString(l.Name + `="` + escapeValue(l.Value) + `"`)
	}
	if len(sorted) > 0 {
		b.WriteByte('}')
	}
	f.series[b.String()] = series{labels: b.String(), value: v}
}

// WriteTo writes the families by name, each with its HELP and TYPE lines and its series by label set.
func (s *Set) WriteTo(w io.Writer) (int64, error) {
	names := make([]string, 0, len(s.families))
	for n := range s.families {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	for _, n := range names {
		f := s.families[n]
		if f.help != "" {
			b.WriteString("# HELP " + n + " " + escapeHelp(f.help) + "\n")
		}
		b.WriteString("# TYPE " + n + " " + string(f.kind) + "\n")
		keys := make([]string, 0, len(f.series))
		for k := range f.series {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString(n + k + " " + formatValue(f.series[k].value) + "\n")
		}
	}
	m, err := io.WriteString(w, b.String())
	return int64(m), err
}

// escapeValue escapes a label value: backslash, double quote and line feed.
func escapeValue(v string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(v)
}

// escapeHelp escapes a HELP text: backslash and line feed.
func escapeHelp(v string) string { return strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(v) }

func formatValue(v float64) string {
	switch {
	case math.IsNaN(v):
		return "NaN"
	case math.IsInf(v, 1):
		return "+Inf"
	case math.IsInf(v, -1):
		return "-Inf"
	}
	if v == math.Trunc(v) && math.Abs(v) < 1e15 {
		return strconv.FormatInt(int64(v), 10) // the counters: whole numbers, without an exponent
	}
	return strconv.FormatFloat(v, 'g', -1, 64)
}
