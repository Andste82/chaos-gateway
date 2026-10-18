package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The operations whose milestone is not in this build. They answer 422 `unsupported_feature`
// with the milestone from the spec; a test checks that this list and the spec agree.

func (s *Server) CalibrateProbe(c *gin.Context, _ model.Ref)      { s.unsupported(c) }
func (s *Server) DeleteCapture(c *gin.Context, _ model.CaptureId) { s.unsupported(c) }
func (s *Server) DownloadCapture(c *gin.Context, _ model.CaptureId, _ model.DownloadCaptureParams) {
	s.unsupported(c)
}
func (s *Server) GetCapture(c *gin.Context, _ model.CaptureId) { s.unsupported(c) }
func (s *Server) GetCertificate(c *gin.Context)                { s.unsupported(c) }
func (s *Server) GetMetrics(c *gin.Context)                    { s.unsupported(c) }
func (s *Server) GetTestCa(c *gin.Context)                     { s.unsupported(c) }
func (s *Server) GetTlsServiceConfig(c *gin.Context, _ model.GetTlsServiceConfigParams) {
	s.unsupported(c)
}
func (s *Server) ListCaptures(c *gin.Context, _ model.ListCapturesParams) { s.unsupported(c) }
func (s *Server) ListProbes(c *gin.Context, _ model.ListProbesParams)     { s.unsupported(c) }
func (s *Server) PostDnsResolutions(c *gin.Context)                       { s.unsupported(c) }
func (s *Server) PostTlsHandshakes(c *gin.Context)                        { s.unsupported(c) }
func (s *Server) RegenerateTestCa(c *gin.Context)                         { s.unsupported(c) }
func (s *Server) ReplaceCertificate(c *gin.Context)                       { s.unsupported(c) }
func (s *Server) RunDiagnostic(c *gin.Context)                            { s.unsupported(c) }
func (s *Server) StartCapture(c *gin.Context, _ model.StartCaptureParams) { s.unsupported(c) }
func (s *Server) StopCapture(c *gin.Context, _ model.CaptureId)           { s.unsupported(c) }
