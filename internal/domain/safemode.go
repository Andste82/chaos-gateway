package domain

import "github.com/Andste82/chaos-gateway/internal/model"

// SafeModeConfiguration is the configuration the gateway runs in safe mode (plan §3.9): management access only, no
// forwarding for the test networks. It keeps what reaches the control plane (the uplink and the management interface, the
// management network's allowed sources and the UI port, the settings) and nothing else: no network, device, rule, fault, port
// forward, probe, profile, scenario or routing. Without a test network there is no bridge, no route between networks and no
// masquerade, so no device of a test network is forwarded anywhere; the management network keeps reaching the UI and the API
// through gateway protection, which is compiled from the management part alone, and keeps its normal path to the uplink
// (plan §2.14). A WireGuard network of the role management is dropped with the other networks: the way in is the management
// interface (docs/open-items.md, P7-M27-01). cfg may be the configuration that failed; it is not modified.
func SafeModeConfiguration(cfg *model.Configuration) *model.Configuration {
	safe := &model.Configuration{SchemaVersion: cfg.SchemaVersion, Uplink: cfg.Uplink, Management: cfg.Management}
	if cfg.Settings != nil {
		s := *cfg.Settings
		safe.Settings = &s
	}
	return safe
}
