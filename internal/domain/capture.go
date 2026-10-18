package domain

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The limits of a capture request (plan §2.11, M17). A file smaller than this holds too few packets to be of
// use, and a snap length of 262144 would not fit it.
const (
	// MinCaptureFile is the smallest `max_size` and ring buffer `file_size`.
	MinCaptureFile = 64 << 10
	// DefaultCaptureMaxSize bounds a capture that names no `max_size`, `duration` or `ring_buffer`, so that one that
	// is forgotten cannot fill the volume.
	DefaultCaptureMaxSize = 1 << 30
)

// CodeCaptureLimit is the code of a capture limit that is out of range.
const CodeCaptureLimit = "invalid_capture_limit"

// ParseByteSize reads a spec `ByteSize` ("64KiB", "5GiB", "1024B") into bytes.
func ParseByteSize(s string) (int64, bool) {
	units := []struct {
		suffix string
		mul    int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}, {"B", 1}}
	for _, u := range units {
		if num, ok := strings.CutSuffix(s, u.suffix); ok {
			n, err := strconv.ParseInt(num, 10, 64)
			if err != nil || n < 0 || n > (1<<62)/u.mul {
				return 0, false
			}
			return n * u.mul, true
		}
	}
	return 0, false
}

// FormatByteSize writes bytes as a spec `ByteSize`, in the largest unit that divides them.
func FormatByteSize(n int64) string {
	for _, u := range []struct {
		suffix string
		mul    int64
	}{{"TiB", 1 << 40}, {"GiB", 1 << 30}, {"MiB", 1 << 20}, {"KiB", 1 << 10}} {
		if n >= u.mul && n%u.mul == 0 {
			return fmt.Sprintf("%d%s", n/u.mul, u.suffix)
		}
	}
	return fmt.Sprintf("%dB", n)
}

// DecodeCaptureRequest reads the body of `POST /captures`: syntax, schema and the limits that need no
// configuration. What the scope names is resolved against the active configuration by whoever starts the capture.
func DecodeCaptureRequest(raw []byte) (*model.CaptureRequest, error) {
	doc, err := ParseDocument(bytes.TrimSpace(raw), FormatJSON)
	if err != nil {
		return nil, err
	}
	s, err := Schemas()
	if err != nil {
		return nil, err
	}
	if errs := s.Validate("CaptureRequest", doc); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var req model.CaptureRequest
	if err := decodeInto(doc, &req); err != nil {
		return nil, fmt.Errorf("domain: decode validated capture request: %w", err)
	}
	if errs := ValidateCaptureRequest(&req); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	return &req, nil
}

// ValidateCaptureRequest checks the limits and the selector of a capture request.
func ValidateCaptureRequest(req *model.CaptureRequest) []model.ValidationError {
	v := &validator{}
	if req.Duration != nil {
		if d, ok := parseDuration(*req.Duration); !ok || d < time.Second {
			v.add("/duration", CodeInvalidDuration, "the duration must be at least 1s")
		}
	}
	if req.MaxSize != nil {
		n, ok := ParseByteSize(*req.MaxSize)
		if !ok || n < MinCaptureFile {
			v.add("/max_size", CodeCaptureLimit, "the maximum size must be at least %s", FormatByteSize(MinCaptureFile))
		}
	}
	if rb := req.RingBuffer; rb != nil {
		if n, ok := ParseByteSize(rb.FileSize); !ok || n < MinCaptureFile {
			v.add("/ring_buffer/file_size", CodeCaptureLimit, "a file of the ring buffer must be at least %s", FormatByteSize(MinCaptureFile))
		}
		if req.MaxSize != nil {
			v.add("/max_size", CodeExclusive, "a ring buffer is bounded by its files: it takes no maximum size")
		}
	}
	if sel := req.Scope.Selector; sel != nil {
		m := model.TrafficMatch{Destination: sel.Destination, Protocol: sel.Protocol, Ports: sel.Ports, PortRanges: sel.PortRanges}
		v.match("/scope/selector", m)
	}
	return v.errs
}
