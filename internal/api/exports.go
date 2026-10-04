package api

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/Andste82/chaos-gateway/internal/audit"
	"github.com/Andste82/chaos-gateway/internal/linkexport"
	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/wireguard"
)

// exportInput is what the WireGuard exports need: the active configuration, the keys and the
// address clients reach the gateway at when the network names no endpoint.
func (s *Server) exportInput(c *gin.Context) (wireguard.ExportInput, bool) {
	_, cfg, ok := s.configAt(c, nil)
	if !ok {
		return wireguard.ExportInput{}, false
	}
	in := wireguard.ExportInput{Config: cfg, Secrets: s.cfg.Secrets}
	if snap := s.cfg.Engine.Snapshot(); snap.Applied != nil && snap.Applied.Uplink.Addr.IsValid() {
		in.UplinkAddress = snap.Applied.Uplink.Addr.Addr().String()
	}
	return in, true
}

func attachment(c *gin.Context, name, ext string) {
	c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.%s"`, safeName(name), ext))
}

func safeName(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "export"
	}
	return b.String()
}

// problemFromExport maps the errors of the wireguard package: they name what the configuration
// lacks (an endpoint, a key), which the client can fix.
func (s *Server) problemFromExport(err error) *problem {
	return newProblem(model.ErrorCodeValidationFailed, "%v", err)
}

// ExportWireGuardClient implements GET /networks/{networkId}/clients/{clientId}/export. The
// download is a secret: it is audit-logged, and with `export_once` the private key is gone after it.
func (s *Server) ExportWireGuardClient(c *gin.Context, networkId model.NetworkId, clientId model.ClientId, params model.ExportWireGuardClientParams) {
	in, ok := s.exportInput(c)
	if !ok {
		return
	}
	v := view{cfg: in.Config, active: true}
	netID, wg, ok := s.hubOf(c, v, networkId)
	if !ok {
		return
	}
	cid, cl, found := findByRef(wg.Clients, clientId, func(x model.WireGuardClient) string { return x.Name })
	if !found {
		s.write(c, notFound("client", clientId))
		return
	}
	e, err := wireguard.ClientConfig(in, netID, cid)
	if err != nil {
		s.write(c, s.problemFromExport(err))
		return
	}
	format := "conf"
	if params.Format != nil {
		format = string(*params.Format)
	}
	var contentType string
	var body []byte
	switch format {
	case "conf":
		contentType, body = "text/plain; charset=utf-8", []byte(e.Conf)
	case "png":
		b, err := wireguard.QRPNG(e.Conf, 512)
		if err != nil {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "the configuration does not fit into a QR code: %v", err))
			return
		}
		contentType, body = "image/png", b
	case "svg":
		svg, err := wireguard.QRSVG(e.Conf)
		if err != nil {
			s.write(c, newProblem(model.ErrorCodeValidationFailed, "the configuration does not fit into a QR code: %v", err))
			return
		}
		contentType, body = "image/svg+xml", []byte(svg)
	default:
		s.write(c, newProblem(model.ErrorCodeBadRequest, "format must be conf, png or svg"))
		return
	}
	// every download is recorded (plan §2.16); the entry is written before the key is sent, not
	// after, so a failing audit log never lets a key out unrecorded (M5-16)
	if err := s.tryRecord(c, "wireguard.export", &audit.Object{Kind: "client", ID: cid, Name: cl.Name}, 0, "format "+format); err != nil {
		s.write(c, newProblem(model.ErrorCodeUnavailable, "the audit log cannot be written"))
		return
	}
	attachment(c, cl.Name, format)
	c.Data(http.StatusOK, contentType, body)
	if done, err := wireguard.ConsumePrivateKey(in, netID, cid); err != nil {
		s.log.Error("cannot delete the private key after the export", "client", cid, "error", err)
	} else if done {
		s.record(c, "wireguard.private_key_consumed", &audit.Object{Kind: "client", ID: cid, Name: cl.Name}, 0, "export once")
	}
}

// DeleteWireGuardClientPrivateKey implements DELETE /networks/{networkId}/clients/{clientId}/private-key.
func (s *Server) DeleteWireGuardClientPrivateKey(c *gin.Context, networkId model.NetworkId, clientId model.ClientId) {
	in, ok := s.exportInput(c)
	if !ok {
		return
	}
	netID, wg, ok := s.hubOf(c, view{cfg: in.Config, active: true}, networkId)
	if !ok {
		return
	}
	cid, cl, found := findByRef(wg.Clients, clientId, func(x model.WireGuardClient) string { return x.Name })
	if !found {
		s.write(c, notFound("client", clientId))
		return
	}
	if _, err := wireguard.DeletePrivateKey(in, netID, cid); err != nil {
		s.fail(c, err)
		return
	}
	s.record(c, "wireguard.private_key_delete", &audit.Object{Kind: "client", ID: cid, Name: cl.Name}, 0, "")
	c.Status(http.StatusNoContent)
}

// ExportWireGuardNetwork implements GET /networks/{networkId}/export: a hub gives a zip of client
// configurations, a link the remote side's configuration or its BIRD snippet.
func (s *Server) ExportWireGuardNetwork(c *gin.Context, networkId model.NetworkId, params model.ExportWireGuardNetworkParams) {
	in, ok := s.exportInput(c)
	if !ok {
		return
	}
	netID, wg, ok := s.hubOf(c, view{cfg: in.Config, active: true}, networkId)
	if !ok {
		return
	}
	format := ""
	if params.Format != nil {
		format = string(*params.Format)
	}
	if wg.Kind == model.Link {
		switch format {
		case "", "conf":
			e, err := wireguard.LinkRemoteConfig(in, netID)
			if err != nil {
				s.write(c, s.problemFromExport(err))
				return
			}
			attachment(c, wg.Name, "conf")
			c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(e.Conf))
			s.record(c, "wireguard.export", &audit.Object{Kind: "network", ID: netID, Name: wg.Name}, 0, "remote side of the link")
		case "bird":
			text, err := linkexport.RemoteBird(in.Config, s.cfg.Secrets, netID, "wg0")
			switch {
			case errors.Is(err, linkexport.ErrNoRouting), errors.Is(err, linkexport.ErrNoProtocol):
				s.write(c, newProblem(model.ErrorCodeNotFound, "%v", err))
				return
			case err != nil:
				s.fail(c, err)
				return
			}
			attachment(c, wg.Name+"-bird", "conf")
			c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(text))
		default:
			s.write(c, newProblem(model.ErrorCodeBadRequest, "a link exports as conf or bird"))
		}
		return
	}
	if format != "" && format != "zip" {
		s.write(c, newProblem(model.ErrorCodeBadRequest, "a hub exports as zip"))
		return
	}
	var ids []string
	if params.Clients != nil && *params.Clients != "" {
		for _, ref := range strings.Split(*params.Clients, ",") {
			id, _, found := findByRef(wg.Clients, strings.TrimSpace(ref), func(x model.WireGuardClient) string { return x.Name })
			if !found {
				s.write(c, notFound("client", ref))
				return
			}
			ids = append(ids, id)
		}
	} else if wg.Clients != nil {
		for id := range *wg.Clients {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	var exports []wireguard.Export
	for _, id := range ids {
		e, err := wireguard.ClientConfig(in, netID, id)
		if err != nil {
			s.write(c, s.problemFromExport(err))
			return
		}
		exports = append(exports, e)
	}
	zipped, err := wireguard.Zip(exports)
	if err != nil {
		s.fail(c, err)
		return
	}
	attachment(c, wg.Name, "zip")
	c.Data(http.StatusOK, "application/zip", zipped)
	for _, id := range ids {
		if _, err := wireguard.ConsumePrivateKey(in, netID, id); err != nil {
			s.log.Error("cannot delete the private key after the export", "client", id, "error", err)
		}
	}
	s.record(c, "wireguard.export", &audit.Object{Kind: "network", ID: netID, Name: wg.Name}, 0, fmt.Sprintf("%d client configurations", len(ids)))
}
