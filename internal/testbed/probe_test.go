package testbed

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAProbeRunIsParsedFromTheOutputOfTheSenderAndTheEcho(t *testing.T) {
	// ten datagrams: the echo got eight of run 7 (two lost in the upload) and one of another run, the
	// sender got six answers (two lost in the download)
	var echo strings.Builder
	for _, seq := range []int{0, 1, 2, 4, 5, 6, 8, 9} {
		echo.WriteString("u 7 " + strconv.Itoa(seq) + " 100000000\n")
	}
	echo.WriteString("u 8 0 5000\n")
	echo.WriteString("u 7 9 100000000\n") // a duplicate counts once
	sender := "d 0 30000000\nd 1 31000000\nd 2 29000000\nd 4 30000000\nd 5 30000000\nd 9 30000000\nsent 10\n"
	r, err := parseProbe(sender, echo.String(), "7")
	if err != nil {
		t.Fatal(err)
	}
	if r.Sent != 10 || r.Delivered != 8 || r.Replied != 6 {
		t.Fatalf("%+v", r)
	}
	if r.UpLoss() != 0.2 {
		t.Errorf("upload loss %v, want 0.2", r.UpLoss())
	}
	if r.DownLoss() != 0.25 {
		t.Errorf("download loss %v, want 0.25 (2 of the 8 that arrived)", r.DownLoss())
	}
	if r.UpMedian() != 100*time.Millisecond || r.DownMedian() != 30*time.Millisecond {
		t.Errorf("medians %v %v", r.UpMedian(), r.DownMedian())
	}
	if !strings.Contains(r.String(), "up median 100ms") {
		t.Errorf("%s", r)
	}
}

func TestAProbeRunWithoutAReportFails(t *testing.T) {
	if _, err := parseProbe("Traceback (most recent call last):\n", "", "1"); err == nil {
		t.Error("a sender that crashed is an error, not a loss of 100 %")
	}
	if _, err := parseProbe("d 0 x\nsent 1\n", "", "1"); err == nil {
		t.Error("a garbled line is an error")
	}
	r, err := parseProbe("sent 5\n", "", "1")
	if err != nil || r.Delivered != 0 || r.UpLoss() != 1 || r.DownLoss() != 0 {
		t.Errorf("nothing delivered: %+v %v", r, err)
	}
}

func TestDuplicatesReorderingAndLossesAreReadFromTheArrivals(t *testing.T) {
	// sent 10: 3 and 6 are lost in the upload, 4 is overtaken by 5, 7 arrives twice, 9 gets no answer
	echo := "u 7 0 1000\nu 7 1 1000\nu 7 2 1000\nu 7 5 1000\nu 7 4 1000\nu 7 7 1000\nu 7 7 1000\nu 7 8 1000\nu 7 9 1000\n"
	sender := "d 0 100\nd 1 100\nd 2 100\nd 5 100\nd 4 100\nd 7 100\nd 7 100\nd 8 100\nsent 10\n"
	r, err := parseProbe(sender, echo, "7")
	if err != nil {
		t.Fatal(err)
	}
	if r.Delivered != 8 || r.Replied != 7 {
		t.Fatalf("delivered %d, replied %d: a copy is neither a delivery nor a reply", r.Delivered, r.Replied)
	}
	if r.UpDuplicates() != 1 || r.DownDuplicates() != 1 {
		t.Errorf("duplicates %d %d, want 1 1", r.UpDuplicates(), r.DownDuplicates())
	}
	if r.UpReordered() != 1 || r.DownReordered() != 1 {
		t.Errorf("reordered %d %d, want 1 1 (4 came after 5)", r.UpReordered(), r.DownReordered())
	}
	if got := fmt.Sprint(r.UpLost()); got != "[3 6]" {
		t.Errorf("lost in the upload: %s", got)
	}
	if got := fmt.Sprint(r.DownLost()); got != "[9]" {
		t.Errorf("lost in the download: %s", got)
	}
}
