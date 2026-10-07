package domain

import (
	"sort"

	"github.com/google/uuid"

	"github.com/Andste82/chaos-gateway/internal/model"
	"github.com/Andste82/chaos-gateway/internal/schema"
)

// refFunc is called for every reference in a document: its JSON pointer, the namespace it
// points into and a pointer to the string, which the function may rewrite.
type refFunc func(path string, kind Kind, ref *string)

// eachEntry calls f for every value of a map in key order. f gets a copy of the value; with
// rewrite the (possibly changed) copy is stored back, so f may rewrite references in place. A
// read-only visit never writes: the configuration it looks at may be shared between goroutines (a
// published snapshot), and even storing an unchanged value back into a map is a data race.
func eachEntry[V any](m *map[string]V, base string, rewrite bool, f func(path string, v *V)) {
	if m == nil || *m == nil {
		return
	}
	keys := make([]string, 0, len(*m))
	for k := range *m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := (*m)[k]
		f(schema.Pointer(base, k), &v)
		if rewrite {
			(*m)[k] = v
		}
	}
}

func visitRef(path string, kind Kind, ref *model.Ref, fn refFunc) {
	if ref != nil {
		fn(path, kind, ref)
	}
}

func visitScope(path string, s *model.Scope, fn refFunc) {
	if s == nil {
		return
	}
	visitRef(path+"/device", KindDevice, s.Device, fn)
	visitRef(path+"/group", KindGroup, s.Group, fn)
	visitRef(path+"/network", KindNetwork, s.Network, fn)
	if s.RemoteNetwork != nil {
		visitRef(path+"/remote_network/client", KindClient, s.RemoteNetwork.Client, fn)
		visitRef(path+"/remote_network/link", KindLink, s.RemoteNetwork.Link, fn)
	}
}

func visitDestination(path string, d *model.Destination, fn refFunc) {
	if d != nil {
		visitRef(path+"/network", KindNetwork, d.Network, fn)
	}
}

func visitTunnel(path string, t *model.TunnelRef, fn refFunc) {
	if t != nil {
		visitRef(path+"/client", KindClient, t.Client, fn)
		visitRef(path+"/link", KindLink, t.Link, fn)
	}
}

func visitMatch(path string, m *model.TrafficMatch, fn refFunc) {
	if m != nil {
		visitDestination(path+"/destination", m.Destination, fn)
	}
}

func visitFaultBody(path string, f *model.FaultBody, fn refFunc) {
	if f == nil {
		return
	}
	visitDestination(path+"/destination", f.Destination, fn)
	visitTunnel(path+"/tunnel", f.Tunnel, fn)
}

func visitRuleBody(path string, r *model.AccessRuleBody, fn refFunc) {
	if r != nil {
		visitDestination(path+"/destination", r.Destination, fn)
	}
}

func visitTLS(path string, t *model.TlsCase, fn refFunc) {
	if t != nil {
		visitDestination(path+"/destination", t.Destination, fn)
	}
}

func visitWireGuardAction(path string, a *model.WireGuardAction, fn refFunc) {
	if a != nil {
		visitRef(path+"/client", KindClient, a.Client, fn)
		visitRef(path+"/link", KindLink, a.Link, fn)
	}
}

func visitEndpoint(path string, e *model.MatrixEndpoint, fn refFunc) {
	visitRef(path+"/network", KindNetwork, e.Network, fn)
	visitRef(path+"/client", KindClient, e.Client, fn)
}

func visitStep(path string, st *model.Step, fn refFunc) {
	visitScope(path+"/target", st.Target, fn)
	visitRef(path+"/profile", KindProfile, st.Profile, fn)
	visitFaultBody(path+"/fault", st.Fault, fn)
	visitRuleBody(path+"/rule", st.Rule, fn)
	visitTLS(path+"/tls", st.Tls, fn)
	visitWireGuardAction(path+"/wireguard", st.Wireguard, fn)
	if st.Wait != nil {
		visitDestination(path+"/wait/destination", st.Wait.Destination, fn)
	}
}

func visitCheck(path string, c *model.Check, fn refFunc) {
	for name, m := range map[string]*model.TrafficMatch{
		"reconnected": c.Reconnected, "no_connection": c.NoConnection, "traffic_seen": c.TrafficSeen,
		"tls_rejected": c.TlsRejected, "tls_accepted": c.TlsAccepted,
	} {
		visitMatch(path+"/"+name, m, fn)
	}
	if c.TrafficNotSeen != nil {
		visitDestination(path+"/traffic_not_seen/destination", c.TrafficNotSeen.Destination, fn)
		if c.TrafficNotSeen.Except != nil {
			for i := range *c.TrafficNotSeen.Except {
				visitDestination(schema.Pointer(path+"/traffic_not_seen/except", itoa(i)), &(*c.TrafficNotSeen.Except)[i], fn)
			}
		}
	}
}

func visitScenario(path string, s *model.Scenario, fn refFunc) {
	visitScope(path+"/target", &s.Target, fn)
	for i := range s.Steps {
		visitStep(schema.Pointer(path+"/steps", itoa(i)), &s.Steps[i], fn)
	}
	if s.Checks != nil {
		for i := range *s.Checks {
			visitCheck(schema.Pointer(path+"/checks", itoa(i)), &(*s.Checks)[i], fn)
		}
	}
}

func visitProfile(path string, p *model.Profile, fn refFunc) {
	visitTLS(path+"/parts/tls", p.Parts.Tls, fn)
}

// visitConfiguration calls fn for every reference of a configuration. Network is a union, so a
// hub network is decoded, visited and stored back. Without rewrite, fn only reads and the
// configuration is not written to.
func visitConfiguration(cfg *model.Configuration, rewrite bool, fn refFunc) {
	eachEntry(cfg.Networks, "/networks", rewrite, func(path string, n *model.Network) {
		disc, _ := n.Discriminator()
		if disc != "wireguard" {
			return
		}
		wg, err := n.AsWireGuardNetwork()
		if err != nil || wg.Clients == nil {
			return
		}
		eachEntry(wg.Clients, path+"/clients", rewrite, func(cpath string, c *model.WireGuardClient) {
			if c.Reachable != nil {
				for i := range *c.Reachable {
					visitEndpoint(schema.Pointer(cpath+"/reachable", itoa(i)), &(*c.Reachable)[i], fn)
				}
			}
		})
		_ = n.FromWireGuardNetwork(wg)
	})
	if cfg.AccessMatrix != nil && cfg.AccessMatrix.Entries != nil {
		for i := range *cfg.AccessMatrix.Entries {
			e := &(*cfg.AccessMatrix.Entries)[i]
			base := schema.Pointer("/access_matrix/entries", itoa(i))
			visitEndpoint(base+"/from", &e.From, fn)
			visitEndpoint(base+"/to", &e.To, fn)
		}
	}
	if cfg.Routing != nil {
		eachEntry(cfg.Routing.Protocols, "/routing/protocols", rewrite, func(path string, p *model.RoutingProtocol) {
			visitRef(path+"/link", KindLink, &p.Link, fn)
			if p.Announce != nil {
				for i := range *p.Announce {
					a := &(*p.Announce)[i]
					base := schema.Pointer(path+"/announce", itoa(i))
					visitRef(base+"/network", KindNetwork, a.Network, fn)
					visitRef(base+"/client", KindClient, a.Client, fn)
				}
			}
		})
	}
	eachEntry(cfg.Devices, "/devices", rewrite, func(path string, d *model.Device) {
		visitRef(path+"/network", KindNetwork, d.Network, fn)
	})
	eachEntry(cfg.Groups, "/groups", rewrite, func(path string, g *model.Group) {
		if g.Members != nil {
			for i := range *g.Members {
				visitRef(schema.Pointer(path+"/members", itoa(i)), KindDevice, &(*g.Members)[i], fn)
			}
		}
	})
	eachEntry(cfg.Probes, "/probes", rewrite, func(path string, p *model.Probe) {
		fn(path+"/network", KindNetwork, &p.Network)
	})
	eachEntry(cfg.AccessRules, "/access_rules", rewrite, func(path string, r *model.AccessRule) {
		visitScope(path+"/source", &r.Source, fn)
		visitDestination(path+"/destination", r.Destination, fn)
	})
	eachEntry(cfg.Faults, "/faults", rewrite, func(path string, f *model.ConfigFault) {
		visitScope(path+"/source", f.Source, fn)
		visitDestination(path+"/destination", f.Destination, fn)
		visitTunnel(path+"/tunnel", f.Tunnel, fn)
	})
	eachEntry(cfg.Profiles, "/profiles", rewrite, func(path string, p *model.Profile) { visitProfile(path, p, fn) })
	eachEntry(cfg.Scenarios, "/scenarios", rewrite, func(path string, s *model.Scenario) { visitScenario(path, s, fn) })
}

// visitOverlayRequest calls fn for every reference of an overlay request.
func visitOverlayRequest(r *model.OverlayRequest, fn refFunc) {
	visitScope("/target", r.Target, fn)
	visitRef("/profile", KindProfile, r.Profile, fn)
	visitFaultBody("/fault", r.Fault, fn)
	visitRuleBody("/rule", r.Rule, fn)
	visitTLS("/tls", r.Tls, fn)
	visitWireGuardAction("/wireguard", r.Wireguard, fn)
}

// IsNormalized reports whether every reference in cfg is already a UUID rather than a name (plan
// convention: stored configurations contain UUIDs only, see Normalize). It only checks syntax,
// not whether a reference resolves to an existing object.
func IsNormalized(cfg *model.Configuration) bool {
	normalized := true
	visitConfiguration(cfg, false, func(_ string, _ Kind, ref *string) {
		if _, err := uuid.Parse(*ref); err != nil || len(*ref) != 36 {
			normalized = false
		}
	})
	return normalized
}

// Normalize returns a copy of the configuration in which every reference is the UUID of the
// object it names (plan conventions: stored configurations contain UUIDs only). References that
// do not resolve are reported as unknown_reference and left as they are.
func Normalize(cfg *model.Configuration, opts ...Option) (*model.Configuration, []model.ValidationError) {
	out := clone(*cfg)
	idx, errs := newIndex(&out, collectOptions(opts))
	errs = append(errs, resolveRefs(&out, idx)...)
	return &out, errs
}

// resolveRefs rewrites the references of cfg in place and reports those that do not resolve.
func resolveRefs(cfg *model.Configuration, idx *Index) []model.ValidationError {
	var errs []model.ValidationError
	visitConfiguration(cfg, true, func(path string, kind Kind, ref *string) {
		if id, ok := idx.Resolve(kind, *ref); ok {
			*ref = id
			return
		}
		errs = append(errs, unknownRef(path, kind, *ref))
	})
	return errs
}

func unknownRef(path string, kind Kind, ref string) model.ValidationError {
	return model.ValidationError{
		Path: path, Code: CodeUnknownReference,
		Message: "there is no " + kind.String() + " " + quote(ref),
	}
}
