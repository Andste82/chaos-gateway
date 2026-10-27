package metrics

import (
	"math"
	"strings"
	"testing"
)

func render(t *testing.T, s *Set) string {
	t.Helper()
	var b strings.Builder
	if _, err := s.WriteTo(&b); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

// The text format: HELP and TYPE per family, families and series sorted, labels sorted by name, whole numbers
// without an exponent, and the escapes the format asks for.
func TestTheSetWritesTheTextFormatSorted(t *testing.T) {
	s := NewSet()
	s.Gauge("chaosgw_b", "A gauge.", 1.5, L("z", "1"), L("a", "2"))
	s.Counter("chaosgw_a_total", "Packets with a \\ and a\nline feed.", 1234567, L("fault", `x"y\z`+"\n"))
	s.Counter("chaosgw_a_total", "", 3, L("fault", "a"))
	s.Gauge("chaosgw_c", "", math.Inf(1))
	s.Gauge("chaosgw_d", "", math.NaN())
	s.Gauge("chaosgw_e", "", 1e21)
	got := render(t, s)
	want := `# HELP chaosgw_a_total Packets with a \\ and a\nline feed.
# TYPE chaosgw_a_total counter
chaosgw_a_total{fault="a"} 3
chaosgw_a_total{fault="x\"y\\z\n"} 1234567
# HELP chaosgw_b A gauge.
# TYPE chaosgw_b gauge
chaosgw_b{a="2",z="1"} 1.5
# TYPE chaosgw_c gauge
chaosgw_c +Inf
# TYPE chaosgw_d gauge
chaosgw_d NaN
# TYPE chaosgw_e gauge
chaosgw_e 1e+21
`
	if got != want {
		t.Errorf("got:\n%s\nwant:\n%s", got, want)
	}
	if s.Err() != nil {
		t.Errorf("err %v", s.Err())
	}
	if again := render(t, s); again != got {
		t.Error("two writes of one set differ")
	}
}

// What the format does not allow is left out and reported; a series added twice keeps the later value.
func TestTheSetLeavesOutWhatTheFormatDoesNotAllow(t *testing.T) {
	s := NewSet()
	s.Gauge("1bad", "", 1)
	s.Gauge("chaosgw_ok", "", 1, L("bad-label", "x"))
	s.Gauge("chaosgw_ok", "", 1, L("__reserved", "x"))
	s.Gauge("chaosgw_ok", "", 1, L("a", "1"), L("a", "2"))
	s.Counter("chaosgw_ok", "", 1)
	s.Gauge("chaosgw_ok", "", 5, L("a", "1"))
	s.Gauge("chaosgw_ok", "", 6, L("a", "1"))
	if got, want := render(t, s), "# TYPE chaosgw_ok gauge\nchaosgw_ok{a=\"1\"} 6\n"; got != want {
		t.Errorf("got %q want %q", got, want)
	}
	if s.Err() == nil || !strings.Contains(s.Err().Error(), "1bad") {
		t.Errorf("the first problem is reported: %v", s.Err())
	}
}
