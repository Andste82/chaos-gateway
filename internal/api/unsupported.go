package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The operations whose milestone is not in this build. They answer 422 `unsupported_feature`
// with the milestone from the spec; a test checks that this list and the spec agree.

func (s *Server) CalibrateProbe(c *gin.Context, _ model.Ref)          { s.unsupported(c) }
func (s *Server) GetCertificate(c *gin.Context)                       { s.unsupported(c) }
func (s *Server) GetMetrics(c *gin.Context)                           { s.unsupported(c) }
func (s *Server) ListProbes(c *gin.Context, _ model.ListProbesParams) { s.unsupported(c) }
func (s *Server) RegenerateTestCa(c *gin.Context)                     { s.unsupported(c) }
func (s *Server) ReplaceCertificate(c *gin.Context)                   { s.unsupported(c) }
func (s *Server) RunDiagnostic(c *gin.Context)                        { s.unsupported(c) }
