package executor

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// Operation types: the closed set the executor accepts.
const (
	TypeNftApply       = "nft_apply"
	TypeNftAddElements = "nft_add_elements"
	TypeNftDelElements = "nft_del_elements"
	TypeRouting        = "routing"
	TypeTC             = "tc"
	TypeOffloads       = "offloads"
	TypeDockerUser     = "docker_user"
	TypeAssign         = "assign_interfaces"
	TypeLinks          = "links"
	TypeSysctl         = "sysctl"
	TypeWireGuard      = "wireguard"
	TypeBird           = "bird"
	TypeRead           = "read"
)

// Operation is one typed request. Exactly one of the implementations below.
type Operation interface {
	// OpType is the wire name.
	OpType() string
	// Namespace is the target network namespace; empty means the executor's own.
	Namespace() string
	// Mutates reports whether the operation changes kernel state (it bumps the generation).
	Mutates() bool
	// Validate checks the operation by itself, independent of any scope.
	validate() error
}

// Target carries the optional network namespace every operation takes.
type Target struct {
	NS string `json:"namespace,omitempty"`
}

// Namespace returns the target namespace.
func (t Target) Namespace() string { return t.NS }

var nsName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func (t Target) validate() error {
	if t.NS != "" && !nsName.MatchString(t.NS) {
		return fmt.Errorf("invalid namespace name %q", t.NS)
	}
	return nil
}

// NftApply replaces objects of table `inet chaosgw` atomically (`nft -j -f`). Ruleset is an
// nftables JSON document ({"nftables":[...]}).
type NftApply struct {
	Target
	Ruleset json.RawMessage `json:"ruleset"`
}

// NftAddElements adds elements to an existing set of the table: the narrow incremental update
// used for DNS-derived address sets and identity changes (plan §3.4).
type NftAddElements struct {
	Target
	Set      string   `json:"set"`
	Elements []string `json:"elements"`
	// TimeoutSeconds is the lifetime of the elements; 0 means the set's default.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// Route is a route in one of Chaos Gateway's tables. The executor tags it with its own protocol.
type Route struct {
	Action string `json:"action"` // replace | delete
	Family int    `json:"family"` // 4 | 6
	Table  int    `json:"table"`
	Dst    string `json:"dst"`            // CIDR, address or "default"
	Via    string `json:"via,omitempty"`  // gateway address
	Dev    string `json:"dev,omitempty"`  // assigned interface
	Type   string `json:"type,omitempty"` // unicast (default) | blackhole | unreachable | prohibit
	Metric *int   `json:"metric,omitempty"`
}

// Rule is a policy-routing rule that sends matching traffic to one of Chaos Gateway's tables.
// The executor tags it with its own protocol, so it can never delete or change foreign rules.
type Rule struct {
	Action   string `json:"action"` // add | delete
	Family   int    `json:"family"` // 4 | 6
	Priority int    `json:"priority"`
	From     string `json:"from,omitempty"`
	To       string `json:"to,omitempty"`
	Fwmark   string `json:"fwmark,omitempty"` // value or value/mask, hex or decimal
	Iif      string `json:"iif,omitempty"`
	Oif      string `json:"oif,omitempty"`
	Table    int    `json:"table"`
}

// NftDelElements deletes elements from a set of the table: the other half of an identity change,
// when a device loses an address. nft refuses to delete an element that is not in the set, and the
// whole request fails then.
type NftDelElements struct {
	Target
	Set      string   `json:"set"`
	Elements []string `json:"elements"`
}

// Routing changes routes and rules.
type Routing struct {
	Target
	Routes []Route `json:"routes,omitempty"`
	Rules  []Rule  `json:"rules,omitempty"`
}

// TCEntry is one tc command. Args are validated tokens; the interface is a field of its own.
type TCEntry struct {
	Object string `json:"object"` // qdisc | class | filter
	Action string `json:"action"` // add | replace | change | delete
	Dev    string `json:"dev"`
	Parent string `json:"parent,omitempty"` // root | ingress | clsact | handle
	Handle string `json:"handle,omitempty"`
	// ClassID names a class (major:minor); classes have no handle. It precedes the kind in the command.
	ClassID string   `json:"classid,omitempty"`
	Args    []string `json:"args,omitempty"` // kind and parameters, e.g. ["netem","delay","50ms"]
}

// TC changes the qdisc tree of assigned interfaces (`tc -batch`).
type TC struct {
	Target
	Entries []TCEntry `json:"entries"`
}

// Offloads switches GRO, GSO, TSO and LRO off on interfaces (`ethtool -K`) and verifies it.
type Offloads struct {
	Target
	Devs []string `json:"devs"`
}

// DockerUser maintains the accept rules for Chaos Gateway's interfaces in Docker's DOCKER-USER
// chain, the only place outside the own table the executor may write (plan §3.4, spike S7).
type DockerUser struct {
	Target
	Action string   `json:"action"` // ensure | remove
	Devs   []string `json:"devs"`
	// OptionalChain skips the operation without error when the chain does not exist (a host
	// without Docker).
	OptionalChain bool `json:"optional_chain,omitempty"`
}

// AssignInterfaces replaces the set of interfaces that belong to Chaos Gateway (test networks,
// uplink, helper devices). tc, routing and DOCKER-USER operations are limited to this set.
type AssignInterfaces struct {
	Target
	Devs []string `json:"devs"`
}

// LinkEntry is one change of the links Chaos Gateway owns: its bridges, their ports and addresses.
type LinkEntry struct {
	// Action: add_bridge (if missing) | delete_bridge | enslave (Name becomes a port of Master) |
	// release (Name leaves its bridge) | up | down | addr_replace | addr_delete (CIDR on Name).
	Action string `json:"action"`
	Name   string `json:"name"`
	Master string `json:"master,omitempty"`
	CIDR   string `json:"cidr,omitempty"`
}

// Links creates and deletes bridges, attaches ports, sets link state and addresses. Every
// interface it names must be assigned.
type Links struct {
	Target
	Entries []LinkEntry `json:"entries"`
}

// SysctlEntry sets one kernel parameter of a closed list.
type SysctlEntry struct {
	Name  string `json:"name"` // ip_forward | accept_ra | disable_ipv6
	Dev   string `json:"dev,omitempty"`
	Value int    `json:"value"`
}

// Sysctl sets IPv4 forwarding and the IPv6 switches of assigned interfaces.
type Sysctl struct {
	Target
	Entries []SysctlEntry `json:"entries"`
}

// WGPeer is a peer of a WireGuard interface. Keys are given as public keys and references: the
// preshared key of a peer is looked up in the key provider under PresharedKeyRef, so no secret
// passes through an operation.
type WGPeer struct {
	PublicKey       string   `json:"public_key"`
	PresharedKeyRef string   `json:"preshared_key_ref,omitempty"`
	AllowedIPs      []string `json:"allowed_ips"`
	Keepalive       int      `json:"keepalive,omitempty"` // seconds, 0 off
	Endpoint        string   `json:"endpoint,omitempty"`  // host:port; set when the gateway initiates
}

// WireGuard creates, updates or deletes a WireGuard interface (plan §2.2.1). `ensure` creates the
// device when it is missing, sets its MTU and synchronizes key, port and peers with `wg syncconf`:
// unchanged peers keep their session, so a re-apply does not interrupt a tunnel. Address and link
// state are set with the links operation. The private key is looked up under KeyRef.
type WireGuard struct {
	Target
	Action     string   `json:"action"` // ensure | delete
	Name       string   `json:"name"`
	ListenPort int      `json:"listen_port,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
	KeyRef     string   `json:"key_ref,omitempty"`
	Peers      []WGPeer `json:"peers,omitempty"`
}

// Bird checks or applies the configuration of Chaos Gateway's BIRD instance (plan §2.2.2). `check`
// parses the text with `bird -p` and changes nothing: the preview shows BIRD's own error message.
// `apply` writes <bird dir>/<instance>.conf, parses it and has the running instance read it with
// `birdc configure`, which keeps established sessions. The text is checked before: no `include`, and
// `kernel table` only for Chaos Gateway's tables and the ones listed in ImportTables.
type Bird struct {
	Target
	Action   string `json:"action"` // check | apply
	Instance string `json:"instance"`
	Config   string `json:"config"`
	// ImportTables are the kernel tables the configuration may read besides Chaos Gateway's own.
	ImportTables []int `json:"import_tables,omitempty"`
}

// Read queries kernel state through the standard tools.
type Read struct {
	Target
	What  string `json:"what"` // links | addrs | routes | rules | nft | qdiscs | classes | filters | offloads
	Dev   string `json:"dev,omitempty"`
	Table string `json:"table,omitempty"` // routes: table name or number, empty means all
	// Name selects the parameter of a sysctl read (ip_forward | accept_ra | disable_ipv6).
	Name string `json:"name,omitempty"`
	// Instance names the BIRD instance of a bird read.
	Instance string `json:"instance,omitempty"`
}

// Read targets.
const (
	ReadLinks    = "links"
	ReadAddrs    = "addrs"
	ReadRoutes   = "routes"
	ReadRules    = "rules"
	ReadNft      = "nft"
	ReadQdiscs   = "qdiscs"
	ReadClasses  = "classes"
	ReadFilters  = "filters"
	ReadOffloads = "offloads"
	// ReadSysctl returns the value of one parameter (Name, Dev) as a number.
	ReadSysctl = "sysctl"
	// ReadAssigned returns the interfaces assigned to Chaos Gateway; ReadDockerUser the accept
	// rules of Chaos Gateway in DOCKER-USER (linux.DockerUserState).
	// ReadNeighbors returns the IPv4 neighbor table (ARP), optionally of one interface.
	ReadNeighbors = "neighbors"
	// ReadConntrack returns the tracked IPv4 connections.
	ReadConntrack = "conntrack"
	// ReadBird returns the state of the BIRD instance Instance (BirdState): whether it runs, the hash
	// of its configuration file and its protocols.
	ReadBird = "bird"
	// ReadWireGuard returns a WireGuard interface and its peers (linux.WGInfo), without any secret.
	ReadWireGuard  = "wireguard"
	ReadAssigned   = "assigned"
	ReadDockerUser = "docker_user"
)

func (NftApply) OpType() string         { return TypeNftApply }
func (NftAddElements) OpType() string   { return TypeNftAddElements }
func (NftDelElements) OpType() string   { return TypeNftDelElements }
func (Routing) OpType() string          { return TypeRouting }
func (TC) OpType() string               { return TypeTC }
func (Offloads) OpType() string         { return TypeOffloads }
func (DockerUser) OpType() string       { return TypeDockerUser }
func (AssignInterfaces) OpType() string { return TypeAssign }
func (Links) OpType() string            { return TypeLinks }
func (Sysctl) OpType() string           { return TypeSysctl }
func (WireGuard) OpType() string        { return TypeWireGuard }
func (Bird) OpType() string             { return TypeBird }
func (Read) OpType() string             { return TypeRead }

func (NftApply) Mutates() bool         { return true }
func (NftAddElements) Mutates() bool   { return true }
func (NftDelElements) Mutates() bool   { return true }
func (Routing) Mutates() bool          { return true }
func (TC) Mutates() bool               { return true }
func (Offloads) Mutates() bool         { return true }
func (DockerUser) Mutates() bool       { return true }
func (AssignInterfaces) Mutates() bool { return true }
func (Links) Mutates() bool            { return true }
func (Sysctl) Mutates() bool           { return true }
func (WireGuard) Mutates() bool        { return true }
func (o Bird) Mutates() bool           { return o.Action == "apply" }
func (Read) Mutates() bool             { return false }

// Envelope is the wire form of an operation: the type and the operation's own fields side by side.
type envelope struct {
	Type string `json:"type"`
}

// ErrDecode is wrapped by every decoder error.
var ErrDecode = errors.New("invalid operation")

const maxOperationBytes = 8 << 20

// Decode parses and validates one operation. It is strict: unknown types, unknown fields, trailing
// data, duplicate keys and malformed values are errors. It never panics on any input.
func Decode(data []byte) (Operation, error) {
	if len(data) > maxOperationBytes {
		return nil, fmt.Errorf("%w: %d bytes exceed the limit of %d", ErrDecode, len(data), maxOperationBytes)
	}
	if err := checkNoDuplicateKeys(data); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	var op Operation
	switch env.Type {
	case TypeNftApply:
		op = &NftApply{}
	case TypeNftAddElements:
		op = &NftAddElements{}
	case TypeNftDelElements:
		op = &NftDelElements{}
	case TypeRouting:
		op = &Routing{}
	case TypeTC:
		op = &TC{}
	case TypeOffloads:
		op = &Offloads{}
	case TypeDockerUser:
		op = &DockerUser{}
	case TypeAssign:
		op = &AssignInterfaces{}
	case TypeLinks:
		op = &Links{}
	case TypeSysctl:
		op = &Sysctl{}
	case TypeWireGuard:
		op = &WireGuard{}
	case TypeBird:
		op = &Bird{}
	case TypeRead:
		op = &Read{}
	default:
		return nil, fmt.Errorf("%w: unknown operation type %q", ErrDecode, env.Type)
	}
	// the type field is part of the envelope, not of the operation: strip it, then decode strictly
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	delete(fields, "type")
	body, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrDecode, err)
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(op); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrDecode, env.Type, err)
	}
	if err := op.validate(); err != nil {
		return nil, fmt.Errorf("%w: %s: %w", ErrDecode, env.Type, err)
	}
	return op, nil
}

// Encode returns the wire form of an operation.
func Encode(op Operation) ([]byte, error) {
	body, err := json.Marshal(op)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return nil, err
	}
	t, _ := json.Marshal(op.OpType())
	fields["type"] = t
	return json.Marshal(fields)
}

// checkNoDuplicateKeys rejects objects with a repeated key at any depth: the two decoders of a
// pipeline could otherwise disagree about which value counts.
func checkNoDuplicateKeys(data []byte) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := walkJSON(dec); err != nil {
		return err
	}
	if _, err := dec.Token(); err == nil {
		return errors.New("trailing data after the operation")
	}
	return nil
}

func walkJSON(dec *json.Decoder) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			kt, err := dec.Token()
			if err != nil {
				return err
			}
			k, _ := kt.(string)
			if seen[k] {
				return fmt.Errorf("duplicate key %q", k)
			}
			seen[k] = true
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walkJSON(dec); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token() // closing delimiter
	return err
}
