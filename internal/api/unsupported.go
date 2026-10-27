package api

import (
	"github.com/gin-gonic/gin"
)

// The operations whose milestone is not in this build. They answer 422 `unsupported_feature`
// with the milestone from the spec; a test checks that this list and the spec agree.

func (s *Server) GetCertificate(c *gin.Context)     { s.unsupported(c) }
func (s *Server) GetMetrics(c *gin.Context)         { s.unsupported(c) }
func (s *Server) ReplaceCertificate(c *gin.Context) { s.unsupported(c) }
