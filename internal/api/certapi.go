package api

import (
	"errors"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/model"
)

// certificateObject is the audit object of the HTTPS certificate.
var certificateObject = &audit.Object{Kind: "certificate", ID: "https", Name: "HTTPS certificate"}

// GetCertificate implements GET /system/certificate.
func (s *Server) GetCertificate(c *gin.Context) {
	if !s.haveCertificates(c) {
		return
	}
	c.JSON(200, s.cfg.Certificates.Info())
}

// ReplaceCertificate implements PUT /system/certificate. The audit entry names the certificate by its fingerprint and
// never holds the key.
func (s *Server) ReplaceCertificate(c *gin.Context) {
	if !s.haveCertificates(c) {
		return
	}
	var in model.CertificateUpload
	if p := decodeJSON(c, &in); p != nil {
		s.write(c, p)
		return
	}
	info, err := s.cfg.Certificates.Install(in.Certificate, in.PrivateKey)
	var ce *CertificateError
	switch {
	case errors.As(err, &ce):
		errs := []model.ValidationError{{Path: "/" + ce.Field, Code: "invalid_certificate", Message: ce.Msg}}
		s.write(c, newProblem(model.ErrorCodeValidationFailed, "the certificate is refused: %s", ce).with(func(b *model.Problem) { b.Errors = &errs }))
		return
	case err != nil:
		s.log.Error("cannot install the HTTPS certificate", "error", err)
		s.write(c, newProblem(model.ErrorCodeInternal, "the certificate cannot be stored"))
		return
	}
	s.record(c, "certificate.install", certificateObject, 0, "sha256 "+*info.FingerprintSha256+", "+*info.Subject)
	c.JSON(200, info)
}

// ResetCertificate implements DELETE /system/certificate.
func (s *Server) ResetCertificate(c *gin.Context) {
	if !s.haveCertificates(c) {
		return
	}
	info, err := s.cfg.Certificates.Reset()
	if err != nil {
		s.log.Error("cannot remove the installed HTTPS certificate", "error", err)
		s.write(c, newProblem(model.ErrorCodeInternal, "the installed certificate cannot be removed"))
		return
	}
	s.record(c, "certificate.reset", certificateObject, 0, "sha256 "+*info.FingerprintSha256)
	c.JSON(200, info)
}

// haveCertificates answers a deployment without a certificate store (the tests of other parts of the API).
func (s *Server) haveCertificates(c *gin.Context) bool {
	if s.cfg.Certificates == nil {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "this deployment has no certificate store"))
		return false
	}
	return true
}
