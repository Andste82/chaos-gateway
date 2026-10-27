package domain

import (
	"bytes"
	"fmt"
	"net/netip"
	"time"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The limits of a calibration request (plan §2.12, M25).
const (
	// MinCalibrationDuration and MaxCalibrationDuration bound `duration`; the request is synchronous.
	MinCalibrationDuration = 5 * time.Second
	MaxCalibrationDuration = 5 * time.Minute
	// DefaultCalibrationDuration is the duration of a request that names none.
	DefaultCalibrationDuration = 30 * time.Second
	// DefaultCalibrationPort is the echo port of a request that names none (RFC 862).
	DefaultCalibrationPort = 7
)

// CodeCalibrationRequest is the code of a calibration request that cannot be run as it is.
const CodeCalibrationRequest = "invalid_calibration"

// DecodeCalibrationRequest reads the body of `POST /probes/{probeId}/calibrate`: syntax, schema and the rules the schema
// cannot say.
func DecodeCalibrationRequest(raw []byte) (*model.CalibrationRequest, error) {
	doc, err := ParseDocument(bytes.TrimSpace(raw), FormatJSON)
	if err != nil {
		return nil, err
	}
	s, err := Schemas()
	if err != nil {
		return nil, err
	}
	if errs := s.Validate("CalibrationRequest", doc); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	var req model.CalibrationRequest
	if err := decodeInto(doc, &req); err != nil {
		return nil, fmt.Errorf("domain: decode validated calibration request: %w", err)
	}
	if errs := ValidateCalibrationRequest(&req); len(errs) > 0 {
		return nil, ValidationErrors(errs)
	}
	return &req, nil
}

// CalibrationDuration is the duration of a request, with the default.
func CalibrationDuration(req *model.CalibrationRequest) time.Duration {
	if req.Duration != nil {
		if d, ok := parseDuration(*req.Duration); ok {
			return d
		}
	}
	return DefaultCalibrationDuration
}

// CalibrationPort is the echo port of a request, with the default.
func CalibrationPort(req *model.CalibrationRequest) int {
	if req.Port != nil {
		return *req.Port
	}
	return DefaultCalibrationPort
}

// ValidateCalibrationRequest checks what the schema cannot: the echo server is an IPv4 address, a profile and a fault are
// not both given, the duration is in range and the fault's parameters are a valid impairment.
func ValidateCalibrationRequest(req *model.CalibrationRequest) []model.ValidationError {
	v := &validator{}
	if a, err := netip.ParseAddr(req.Host); err != nil || !a.Is4() {
		v.add("/host", CodeCalibrationRequest, "the echo server must be given as an IPv4 address")
	}
	if req.Profile != nil && req.Fault != nil {
		v.add("/fault", CodeExclusive, "a profile and a fault are exclusive: measure one of them")
	}
	if req.Duration != nil {
		d, ok := parseDuration(*req.Duration)
		switch {
		case !ok:
			v.add("/duration", CodeInvalidDuration, "%q is not a duration", *req.Duration)
		case d < MinCalibrationDuration || d > MaxCalibrationDuration:
			v.add("/duration", CodeInvalidDuration, "the duration must be %s to %s", MinCalibrationDuration, MaxCalibrationDuration)
		}
	}
	if f := req.Fault; f != nil {
		v.impairment("/fault", convert[model.NetemParams](f), f.Upload, f.Download, false)
	}
	return v.errs
}
