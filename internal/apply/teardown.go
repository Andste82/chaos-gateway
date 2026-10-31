package apply

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Andste82/chaos-gateway/internal/compiler"
	"github.com/Andste82/chaos-gateway/internal/executor"
)

// Teardown removes everything Chaos Gateway created in the kernel (plan §3.8 "Stop", D35, variant A): the `inet chaosgw`
// table and the duplication hook, its policy rules and the routes of its tables, the test bridges with their addresses
// (their ports are released), the WireGuard interfaces and the IFB device, the service veth pair and the name of the
// service namespace, the probes with their namespaces, pairs and DHCP clients, the tc trees on the interfaces that belong to
// it, its rules in DOCKER-USER, and the BIRD instance's protocols. It reads the kernel, plans the difference to nothing and
// carries it out; what is not there is left alone, so it can be repeated and finishes what an earlier run left. serviceNS
// is the name of the service namespace (empty is compiler.ServiceNSDefault). It returns the plan that ran and, when something of the gateway's
// is still there afterwards, an error naming it.
//
// The ownership is the executor's scope (the interfaces assigned to it) and the names the gateway gives its own objects: an
// interface that was never assigned is never touched. What the host's own configuration owns (the addresses of the uplink and
// the management interface, the networkd rule and the netplan entry of a test interface, the offloads of a port) stays.
func Teardown(ctx context.Context, ex Exec, ns, serviceNS string) (*Plan, error) {
	if serviceNS == "" {
		serviceNS = compiler.ServiceNSDefault
	}
	want := Want{BirdInstance: compiler.BirdInstance, ServiceNS: serviceNS, ServicePeerIf: compiler.ServicePeerIf}
	s, err := ReadState(ctx, ex, ns, want)
	if err != nil {
		return nil, &Error{Stage: "read", Err: err}
	}
	p, err := BuildTeardown(s, ns, serviceNS)
	if err != nil {
		return nil, &Error{Stage: "plan", Err: err}
	}
	// BIRD's container stops before the executor does (it depends on it): its protocols are withdrawn in the file it reads at its
	// next start, which is all there is to do then
	var birdDown *executor.BirdDownError
	if _, err := ex.Do(ctx, p.Ops...); err != nil && !errors.As(err, &birdDown) {
		return p, &Error{Stage: "execute", Err: err}
	}
	// the tc trees are read on the interfaces the gateway owned: they are released now, and a tree is only read on an interface
	// that is assigned or named, so the check would not see one that the plan missed
	want.TCDevs = union(s.Assigned, []string{compiler.IFBDev})
	after, err := ReadState(ctx, ex, ns, want)
	if err != nil {
		return p, &Error{Stage: "verify", Err: err}
	}
	if left := Leftovers(after, union(s.Assigned, []string{compiler.ServiceHostIf, compiler.IFBDev})); len(left) > 0 {
		return p, &Error{Stage: "verify", Err: fmt.Errorf("%d objects of Chaos Gateway are still there: %s", len(left), strings.Join(left, "; "))}
	}
	return p, nil
}

// BuildTeardown plans the removal of everything Teardown lists, from the state s. The order is the apply's own: routes and
// rules before the links they name, the pair of the service namespace after the routes that lead through it, links, probes,
// tc, the table, DOCKER-USER, BIRD, the scope last.
func BuildTeardown(s *State, ns, serviceNS string) (*Plan, error) {
	if serviceNS == "" {
		serviceNS = compiler.ServiceNSDefault
	}
	// the service pair and the IFB are Chaos Gateway's by their fixed names; the executor can delete them only when they
	// are assigned, which a gateway that was killed between two requests may not have recorded
	st := *s
	st.Assigned = append([]string(nil), s.Assigned...)
	for _, name := range []string{compiler.ServiceHostIf, compiler.IFBDev} {
		if _, ok := s.Links[name]; ok {
			st.Assigned = union(st.Assigned, []string{name})
		}
	}
	// the service pair is deleted below whatever the plan found, so that the name of its namespace goes too
	st.Assigned = union(st.Assigned, []string{compiler.ServiceHostIf})
	p, err := buildPlan(&compiler.Target{}, &st, ns, false, nil)
	if err != nil {
		return nil, err
	}
	tg := executor.Target{NS: ns}
	haveService := false
	for i, op := range p.Ops {
		switch o := op.(type) {
		case *executor.NftApply:
			// the table goes with everything in it: a table made and deleted in one transaction also deletes a table that is
			// not there, which nft would refuse to delete alone
			p.Ops[i] = &executor.NftApply{Target: tg, Ruleset: json.RawMessage(nftDeleteTable)}
			p.Summary = replaceSummary(p.Summary, "nftables: table inet chaosgw", "nftables: delete table inet chaosgw")
		case *executor.ServiceNS:
			haveService = haveService || o.Action == "delete"
		}
	}
	if !haveService {
		if _, pair := s.Links[compiler.ServiceHostIf]; pair || (s.Service != nil && s.Service.Exists) {
			// the pair is gone or was never there, but the name of the namespace is not
			op := &executor.ServiceNS{Target: tg, Action: "delete", Name: serviceNS, HostIf: compiler.ServiceHostIf, PeerIf: compiler.ServicePeerIf,
				HostCIDR: compiler.ServiceHostCIDR.String(), PeerCIDR: compiler.ServicePeerCIDR.String()}
			p.Ops = append([]executor.Operation{p.Ops[0], op}, p.Ops[1:]...)
			p.Summary = append([]string{p.Summary[0], "service namespace: delete " + serviceNS}, p.Summary[1:]...)
		}
	}
	// the withdrawal of BIRD's protocols goes last: it fails when BIRD does not run, and a failed operation ends the request
	var bird, rest []executor.Operation
	for _, op := range p.Ops {
		if _, ok := op.(*executor.Bird); ok {
			bird = append(bird, op)
		} else {
			rest = append(rest, op)
		}
	}
	p.Ops = append(rest, bird...)
	// a delete of the service pair names the namespace the configuration used; the teardown knows the name of the one it
	// is given
	for _, op := range p.Ops {
		if o, ok := op.(*executor.ServiceNS); ok && o.Action == "delete" {
			o.Name = serviceNS
		}
	}
	return p, nil
}

// nftDeleteTable is the transaction that deletes the table of Chaos Gateway, whether it exists or not.
const nftDeleteTable = `{"nftables":[{"add":{"table":{"family":"inet","name":"chaosgw"}}},{"delete":{"table":{"family":"inet","name":"chaosgw"}}}]}`

func replaceSummary(l []string, prefix, with string) []string {
	for i, s := range l {
		if strings.HasPrefix(s, prefix) {
			l[i] = with
			return l
		}
	}
	return l
}

// Leftovers lists what of Chaos Gateway's is still in the kernel state s: the objects Teardown removes. owned are the
// interfaces that belonged to the gateway before (the scope is empty once the teardown has run). Empty means the kernel holds
// nothing of the gateway that changes the host's or the devices' traffic.
func Leftovers(s *State, owned []string) []string {
	var left []string
	if s.Nft != nil && len(s.Nft.Tables()) > 0 {
		left = append(left, "the nftables table inet chaosgw")
	}
	if s.NftDup != nil && len(s.NftDup.Tables()) > 0 {
		left = append(left, "the nftables table "+executor.NftDupTable)
	}
	for _, r := range s.Rules {
		if r.Protocol == ownProto {
			left = append(left, fmt.Sprintf("the policy rule with priority %d", r.Priority))
		}
	}
	for _, r := range s.Routes {
		if r.Protocol == ownProto && ownTable(r.Table) {
			left = append(left, fmt.Sprintf("a route to %s in table %s", r.Dst, r.Table))
		}
	}
	for _, name := range sortedNames(s.Links) {
		switch l := s.Links[name]; {
		case contains(owned, name) && l.Kind() == "bridge":
			left = append(left, "the bridge "+name)
		case contains(owned, name) && l.Kind() == "wireguard":
			left = append(left, "the WireGuard interface "+name)
		case name == compiler.IFBDev && l.Kind() == "ifb":
			left = append(left, "the IFB device "+name)
		case name == compiler.ServiceHostIf && l.Kind() == "veth":
			left = append(left, "the service interface "+name)
		}
	}
	if s.Service != nil && s.Service.Exists {
		left = append(left, "the name of the service namespace")
	}
	for _, name := range sortedStrings(s.Probes) {
		p := s.Probes[name]
		left = append(left, fmt.Sprintf("the probe namespace %s (its end %s)", p.Name, p.HostIf))
	}
	for dev, tree := range s.TC {
		if tree != nil && contains(owned, dev) {
			left = append(left, "a tc tree on "+dev)
		}
	}
	if s.DockerUser.ChainExists && (len(s.DockerUser.In) > 0 || len(s.DockerUser.Out) > 0) {
		left = append(left, "rules in DOCKER-USER for "+strings.Join(union(s.DockerUser.In, s.DockerUser.Out), ", "))
	}
	if s.Bird != nil && s.Bird.ConfigHash != "" && s.Bird.ConfigHash != TextHash(idleText) {
		left = append(left, "protocols of the BIRD instance")
	}
	return left
}

func sortedStrings[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sortStrings(out)
	return out
}
