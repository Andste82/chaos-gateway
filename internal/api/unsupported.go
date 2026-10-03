package api

import (
	"github.com/gin-gonic/gin"
	openapi_types "github.com/oapi-codegen/runtime/types"

	"github.com/Andste82/chaos-gateway/internal/model"
)

// The operations whose milestone is not in this build. They answer 422 `unsupported_feature`
// with the milestone from the spec; a test checks that this list and the spec agree.

func (s *Server) AbortRun(c *gin.Context, _ model.RunId)                    { s.unsupported(c) }
func (s *Server) CalibrateProbe(c *gin.Context, _ model.Ref)                { s.unsupported(c) }
func (s *Server) CreateOverlay(c *gin.Context, _ model.CreateOverlayParams) { s.unsupported(c) }
func (s *Server) CreateRun(c *gin.Context, _ model.CreateRunParams)         { s.unsupported(c) }
func (s *Server) DeleteCapture(c *gin.Context, _ model.CaptureId)           { s.unsupported(c) }
func (s *Server) DeleteOverlay(c *gin.Context, _ openapi_types.UUID)        { s.unsupported(c) }
func (s *Server) DownloadCapture(c *gin.Context, _ model.CaptureId, _ model.DownloadCaptureParams) {
	s.unsupported(c)
}
func (s *Server) Explain(c *gin.Context, _ model.ExplainParams) { s.unsupported(c) }
func (s *Server) GetAccessRule(c *gin.Context, _ model.Ref, _ model.GetAccessRuleParams) {
	s.unsupported(c)
}
func (s *Server) GetBusy(c *gin.Context)                                           { s.unsupported(c) }
func (s *Server) GetCapture(c *gin.Context, _ model.CaptureId)                     { s.unsupported(c) }
func (s *Server) GetCertificate(c *gin.Context)                                    { s.unsupported(c) }
func (s *Server) GetFault(c *gin.Context, _ model.Ref, _ model.GetFaultParams)     { s.unsupported(c) }
func (s *Server) GetMetrics(c *gin.Context)                                        { s.unsupported(c) }
func (s *Server) GetOverlay(c *gin.Context, _ openapi_types.UUID)                  { s.unsupported(c) }
func (s *Server) GetProfile(c *gin.Context, _ model.Ref, _ model.GetProfileParams) { s.unsupported(c) }
func (s *Server) GetRun(c *gin.Context, _ model.RunId)                             { s.unsupported(c) }
func (s *Server) GetRunEvents(c *gin.Context, _ model.RunId)                       { s.unsupported(c) }
func (s *Server) GetRunReportJson(c *gin.Context, _ model.RunId)                   { s.unsupported(c) }
func (s *Server) GetRunReportJunit(c *gin.Context, _ model.RunId)                  { s.unsupported(c) }
func (s *Server) GetScenario(c *gin.Context, _ model.Ref, _ model.GetScenarioParams) {
	s.unsupported(c)
}
func (s *Server) GetTestCa(c *gin.Context) { s.unsupported(c) }
func (s *Server) GetTlsServiceConfig(c *gin.Context, _ model.GetTlsServiceConfigParams) {
	s.unsupported(c)
}
func (s *Server) ListAccessRules(c *gin.Context, _ model.ListAccessRulesParams) { s.unsupported(c) }
func (s *Server) ListCaptures(c *gin.Context, _ model.ListCapturesParams)       { s.unsupported(c) }
func (s *Server) ListFaults(c *gin.Context, _ model.ListFaultsParams)           { s.unsupported(c) }
func (s *Server) ListOverlays(c *gin.Context, _ model.ListOverlaysParams)       { s.unsupported(c) }
func (s *Server) ListProbes(c *gin.Context, _ model.ListProbesParams)           { s.unsupported(c) }
func (s *Server) ListProfiles(c *gin.Context, _ model.ListProfilesParams)       { s.unsupported(c) }
func (s *Server) ListRuns(c *gin.Context, _ model.ListRunsParams)               { s.unsupported(c) }
func (s *Server) ListScenarios(c *gin.Context, _ model.ListScenariosParams)     { s.unsupported(c) }
func (s *Server) PostDnsResolutions(c *gin.Context)                             { s.unsupported(c) }
func (s *Server) PostTlsHandshakes(c *gin.Context)                              { s.unsupported(c) }
func (s *Server) RegenerateTestCa(c *gin.Context)                               { s.unsupported(c) }
func (s *Server) RenewOverlay(c *gin.Context, _ openapi_types.UUID)             { s.unsupported(c) }
func (s *Server) RenewRun(c *gin.Context, _ model.RunId)                        { s.unsupported(c) }
func (s *Server) ReplaceCertificate(c *gin.Context)                             { s.unsupported(c) }
func (s *Server) Reset(c *gin.Context, _ model.ResetParams)                     { s.unsupported(c) }
func (s *Server) RunDiagnostic(c *gin.Context)                                  { s.unsupported(c) }
func (s *Server) RunScenario(c *gin.Context, _ model.Ref, _ model.RunScenarioParams) {
	s.unsupported(c)
}
func (s *Server) StartCapture(c *gin.Context, _ model.StartCaptureParams) { s.unsupported(c) }
func (s *Server) StopCapture(c *gin.Context, _ model.CaptureId)           { s.unsupported(c) }
