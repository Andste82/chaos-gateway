package api

import (
	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The internal endpoints of the NTP server (M34, plan §2.18). Stage 1 of the milestone (D37) only has the specification: the
// server, the compiler and the engine are not there yet, so there is no configuration to hand out and no log to take. The
// handlers answer with a problem that says so, instead of 404 (which would look like a wrong path), until stage 2 replaces
// them with the long poll of the configuration and the intake of the query log, after GetDnsServiceConfig and PostDnsQueries.

const ntpNotWired = "the NTP service is not wired yet: the gateway has no NTP server in this build (M34, stage 2)"

// GetNtpServiceConfig implements GET /internal/ntp/config.
func (s *Server) GetNtpServiceConfig(c *gin.Context, _ model.GetNtpServiceConfigParams) {
	s.write(c, newProblem(model.ErrorCodeUnsupportedFeature, "%s", ntpNotWired))
}

// PostNtpQueries implements POST /internal/ntp/queries.
func (s *Server) PostNtpQueries(c *gin.Context) {
	s.write(c, newProblem(model.ErrorCodeUnsupportedFeature, "%s", ntpNotWired))
}
